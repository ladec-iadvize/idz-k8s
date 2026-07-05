// Package ui implements the read-only Bubble Tea program. For the v1 MVP the
// US1 screens (list, detail, logs, pickers) are consolidated here; they can be
// split into internal/ui/views/ later without touching the data layers.
package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/components"
	"github.com/iadvize/idz-k8s/internal/ui/keys"
	"github.com/iadvize/idz-k8s/internal/ui/theme"
)

type screen int

const (
	screenList screen = iota
	screenDetail
	screenLogs
	screenPicker
	screenTop
	screenDiag
	screenTopology
	screenEvents
	screenHelm
	screenHelmHist
)

type pickerKind int

const (
	pickType pickerKind = iota
	pickNamespace
	pickContext
	pickEventKind
)

// allKindsLabel is the sentinel option that clears the event kind filter.
const allKindsLabel = "◆ all kinds"

// helmReleasesLabel is the virtual entry in the ':' type picker that opens the
// Helm release view — so ":helm" works like any resource type.
const helmReleasesLabel = "◆ helm releases"

// logBufMax bounds the in-memory log buffer (keeps the tail).
const logBufMax = 5000

// allNamespacesLabel is the sentinel option that lists across all namespaces.
const allNamespacesLabel = "◆ all namespaces"

// Model is the root Bubble Tea model.
type Model struct {
	cfg            config.Config
	kubeconfigPath string
	configPath     string
	initialTypeKey string

	client  *kube.Client
	metrics *metrics.Client
	helm    *helm.Client
	types   []model.ResourceType
	curType model.ResourceType
	objects []model.ResourceObject

	// UI components
	keys     keys.KeyMap
	theme    theme.Theme
	help     help.Model
	table    table.Model
	detail   viewport.Model
	logsView viewport.Model
	usage    viewport.Model
	diag     viewport.Model
	topo     viewport.Model
	events   viewport.Model
	filter   textinput.Model
	picker   table.Model

	screen      screen
	pickerKind  pickerKind
	pickerOpts  []string
	pickerQuery string

	width, height int
	filtering     bool
	revealSecret  bool
	statusMsg     string
	errMsg        string

	logCancel context.CancelFunc
	logCh     <-chan kube.LogLine
	logBuf    []string // accumulated log lines (bounded by logBufMax)
	logPaused bool     // paused: keep buffering but stop auto-scrolling (FR-005)

	// Detail usage panel (pods) and top-consumers view.
	detailObj            model.ResourceObject
	detailNS, detailName string
	detailCPU, detailMem model.Usage
	detailHasUsage       bool
	topKind              model.MetricKind

	// Events timeline (US5).
	eventRows       []model.Event
	eventsQuery     string
	eventsFiltering bool            // typing a filter (Enter commits, Esc cancels)
	eventsKind      string          // kind filter ("" = all kinds)
	eventsWarnOnly  bool            // severity filter: warnings only (FR-014)
	recentSel       int             // selected index in the Recent list (highlighted on the timeline)
	eventsScope     map[string]bool // "ns/name" allow-list (set when opened from a drill)
	eventsScopeFor  string          // label, e.g. "Deployment/back"
	pickerReturn    screen          // screen to return to when the picker closes

	// Drill-down: viewing the pods owned by a workload (US9 ownership).
	drillSelector  string             // label selector; "" = not drilling
	drillFor       string             // e.g. "Deployment/back"
	drillNamespace string             // workload namespace (query scope only — the user's namespace filter is untouched)
	drillPrevType  model.ResourceType // type to restore on Esc

	recentWin int // first visible row of the events detail window

	// Helm release overview (US12, read-only).
	helmTable      table.Model
	helmHist       viewport.Model
	helmRows       []model.HelmRelease
	helmValuesOnly bool // 'v': show only the values of a release

	// Click zones of the events header line (T048); set by renderEvents.
	eventsKindZone  clickZone
	eventsFilterHit clickZone

	// disconnected marks a lost cluster connection; the periodic tick keeps
	// retrying and we announce recovery (FR-016).
	disconnected bool
	bodyH        int // content height, set by layout (used for click zones)

	// mouseOn tracks mouse capture; toggled with 'm' so the terminal's native
	// text selection (copy/paste) can be used when needed.
	mouseOn bool

	// Row windowing (exact mouse click→row mapping, US7).
	win       winTable // main list
	pickerWin winTable
	helmWin   winTable

	// Double-click detection.
	lastClickScreen screen
	lastClickRow    int
	lastClickAt     time.Time

	// Events view: content line of the first Recent row + rows shown (for
	// click-to-select on the timeline's detail list).
	recentBaseLine int
	recentShown    int

	// Row health per visible row (whole-row coloring) and per-node pod
	// readiness (Node view column).
	rowLevels []model.HealthLevel
	nodePods  map[string][2]int

	// Marked resources (Space): scope for 'f' and 'v'. Key = ns/name.
	marked map[string]model.ResourceObject

	// Column sorting of the main list (session-only; saved views are US8/v2).
	sortCol int // visual column index (1=NAMESPACE … 5=AGE); -1 = none
	sortAsc bool
}

// Option customizes the initial model.
type Option func(*Model)

// WithInitialType starts the client on a specific resource type instead of the
// discovered default. Useful when discovery is limited and for deterministic
// startup (e.g. tests, or a future "--view" that opens a saved type).
func WithInitialType(t model.ResourceType) Option {
	return func(m *Model) { m.curType = t }
}

// WithMetrics attaches the Prometheus-backed metrics client (single source for
// gauges, trend charts, and top consumers).
func WithMetrics(mc *metrics.Client) Option {
	return func(m *Model) { m.metrics = mc }
}

// WithHelm attaches the read-only Helm release reader (US12).
func WithHelm(hc *helm.Client) Option {
	return func(m *Model) { m.helm = hc }
}

// WithMouse records whether mouse capture is initially enabled (the 'm' key
// toggles it at runtime to allow native text selection / copy-paste).
func WithMouse(on bool) Option {
	return func(m *Model) { m.mouseOn = on }
}

// WithConfigPath sets where preferences (last context/namespace/type) persist.
func WithConfigPath(path string) Option {
	return func(m *Model) { m.configPath = path }
}

// WithInitialTypeKey restores the last-used resource type once discovery runs.
func WithInitialTypeKey(key string) Option {
	return func(m *Model) { m.initialTypeKey = key }
}

// New builds the initial model for the given client and config.
func New(client *kube.Client, cfg config.Config, kubeconfigPath string, opts ...Option) Model {
	fi := textinput.New()
	fi.Placeholder = "filter…"
	fi.Prompt = "/"

	m := Model{
		cfg:            cfg,
		kubeconfigPath: kubeconfigPath,
		client:         client,
		keys:           keys.Default(),
		theme:          theme.Default(),
		help:           help.New(),
		table:          table.New(table.WithFocused(true)),
		detail:         viewport.New(0, 0),
		logsView:       viewport.New(0, 0),
		usage:          viewport.New(0, 0),
		diag:           viewport.New(0, 0),
		topo:           viewport.New(0, 0),
		events:         viewport.New(0, 0),
		filter:         fi,
		picker:         table.New(table.WithFocused(true)),
		helmTable:      table.New(table.WithFocused(true)),
		helmHist:       viewport.New(0, 0),
		screen:         screenList,
		marked:         map[string]model.ResourceObject{},
		sortCol:        -1,
		sortAsc:        true,
	}
	// Table look & feel: colored headers, background-highlighted selection.
	// Purely visual — row geometry is unchanged (mouse mapping intact).
	ts := table.DefaultStyles()
	ts.Header = m.theme.TableHeader.BorderStyle(lipgloss.NormalBorder()).BorderBottom(false)
	ts.Selected = m.theme.TableSelected
	ts.Cell = lipgloss.NewStyle()
	m.table.SetStyles(ts)
	m.picker.SetStyles(ts)
	m.helmTable.SetStyles(ts)

	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// ---- Messages ----

type typesMsg struct {
	types []model.ResourceType
	err   error
}
type objectsMsg struct {
	objects  []model.ResourceObject
	nodePods map[string][2]int // only set when listing nodes
	err      error
}
type logLineMsg kube.LogLine
type tickMsg struct{}
type errMsg struct{ err error }
type topMsg struct {
	rows []model.TopConsumer
	kind model.MetricKind
}
type usageMsg struct {
	ns, name string
	cpu, mem model.Usage
}
type metricsMsg struct {
	client *metrics.Client
	note   string
}
type diagMsg struct {
	rows []model.Diagnostic
	err  error
}
type topologyMsg struct {
	nodes []model.TopologyNode
	err   error
}
type eventsMsg struct {
	rows []model.Event
	err  error
}
type describeMsg struct {
	ns, name string
	events   []model.Event
	extra    string // e.g. Service endpoints summary
}
type helmMsg struct {
	rows []model.HelmRelease
	err  error
}
type helmDetailMsg struct {
	ns, name string
	detail   helm.ReleaseDetail
	live     []helmResLive // mirrors detail.Resources: live state of each object
	err      error
}

type helmResLive struct {
	status model.StatusSummary
	found  bool
	known  bool // type resolvable via discovery
}

// ---- Init ----

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadTypes()}
	// Auto-discover the cluster's Prometheus unless an explicit URL was given.
	if !m.metrics.Enabled() && m.cfg.PrometheusURL == "" {
		cmds = append(cmds, m.discoverMetrics())
	}
	return tea.Batch(cmds...)
}

func (m Model) loadTypes() tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.ResourceTypes()
		return typesMsg{types: ts, err: err}
	}
}

func (m Model) listObjects() tea.Cmd {
	c, t, ns, sel := m.client, m.curType, m.client.Namespace, m.drillSelector
	if sel != "" && m.drillNamespace != "" {
		// Drilling: query in the workload's namespace so its selector cannot
		// match same-labelled pods elsewhere. The user's ns filter is untouched.
		ns = m.drillNamespace
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		objs, err := c.ListSelected(ctx, t, ns, sel)
		// Services get a real status from their backends (one extra LIST).
		if err == nil && t.Group == "" && t.Resource == "services" {
			if eps, eerr := c.EndpointsByService(ctx, ns); eerr == nil {
				for i := range objs {
					objs[i].Status = kube.ServiceStatus(objs[i].Raw, eps, objs[i].Namespace, objs[i].Name)
				}
			}
		}
		var nodePods map[string][2]int
		// Nodes: count ready/total pods per node (one extra LIST) for the
		// PODS READY column.
		if err == nil && t.Group == "" && t.Resource == "nodes" {
			podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
			if pods, perr := c.List(ctx, podType, ""); perr == nil {
				nodePods = map[string][2]int{}
				for _, p := range pods {
					node := kube.PodNode(p.Raw)
					if node == "" {
						continue
					}
					cnt := nodePods[node]
					cnt[1]++
					if r, d, ok := kube.ReadyCount("Pod", p.Raw); ok && d > 0 && r == d {
						cnt[0]++
					}
					nodePods[node] = cnt
				}
			}
		}
		return objectsMsg{objects: objs, nodePods: nodePods, err: err}
	}
}

func (m Model) tick() tea.Cmd {
	d := time.Duration(m.cfg.RefreshIntervalSeconds) * time.Second
	if d < time.Second {
		d = 5 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

// discoverMetrics finds the current cluster's Prometheus and builds a client
// that reaches it through the API server proxy (autonomous, per-context).
func (m Model) discoverMetrics() tea.Cmd {
	cl := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ref, ok, err := cl.DiscoverPrometheus(ctx)
		if err != nil || !ok {
			return metricsMsg{client: &metrics.Client{}, note: "no in-cluster Prometheus found (press 'u' shows unavailable)"}
		}
		mc, err := metrics.NewViaProxy(cl.RESTConfig(), ref.Namespace, ref.Name, ref.Port)
		if err != nil {
			return metricsMsg{client: &metrics.Client{}, note: "prometheus link failed: " + err.Error()}
		}
		return metricsMsg{client: mc, note: fmt.Sprintf("metrics via %s/%s:%d", ref.Namespace, ref.Name, ref.Port)}
	}
}

func (m Model) fetchDiag() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	marked := make([]model.ResourceObject, 0, len(m.marked))
	for _, o := range m.marked {
		marked = append(marked, o)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := cl.Diagnostics(ctx, ns)
		if err == nil && len(marked) > 0 {
			// Scope findings to the marked resources: marked pods directly,
			// marked workloads through the pods their selector owns.
			allowed, aerr := kube.ResolveMarkedPods(ctx, cl, marked)
			if aerr == nil {
				kept := rows[:0]
				for _, d := range rows {
					if allowed[d.Namespace+"/"+d.Pod] {
						kept = append(kept, d)
					}
				}
				rows = kept
			}
		}
		return diagMsg{rows: rows, err: err}
	}
}

func (m Model) fetchTopology() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		nodes, err := cl.Topology(ctx, ns)
		return topologyMsg{nodes: nodes, err: err}
	}
}

func (m Model) fetchEvents() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	if m.drillSelector != "" && m.drillNamespace != "" {
		ns = m.drillNamespace // drilled: fetch only the workload's namespace
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := cl.Events(ctx, ns)
		return eventsMsg{rows: rows, err: err}
	}
}

func (m Model) fetchTop(kind model.MetricKind) tea.Cmd {
	mc := m.metrics
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows := mc.TopN(ctx, metrics.TopPods(15, kind), kind)
		return topMsg{rows: rows, kind: kind}
	}
}

func (m Model) fetchPodUsage(ns, name string, raw map[string]interface{}) tea.Cmd {
	mc := m.metrics
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cpuReq, cpuLim, memReq, memLim := kube.PodResources(raw)

		cpuVal, cpuOK := mc.InstantScalar(ctx, metrics.PodUsage(ns, name, model.MetricCPU))
		cpuSeries := mc.Range(ctx, metrics.PodUsageRange(ns, name, model.MetricCPU), metrics.TrendWindow, time.Minute)
		memVal, memOK := mc.InstantScalar(ctx, metrics.PodUsage(ns, name, model.MetricMemory))
		memSeries := mc.Range(ctx, metrics.PodUsageRange(ns, name, model.MetricMemory), metrics.TrendWindow, time.Minute)

		return usageMsg{
			ns:   ns,
			name: name,
			cpu:  model.Usage{Kind: model.MetricCPU, Current: cpuVal, Request: cpuReq, Limit: cpuLim, Series: cpuSeries, Available: cpuOK},
			mem:  model.Usage{Kind: model.MetricMemory, Current: memVal, Request: memReq, Limit: memLim, Series: memSeries, Available: memOK},
		}
	}
}

// ---- Update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case typesMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.types = msg.types
		if len(m.types) > 0 && m.curType.Resource == "" {
			if t, ok := findTypeByKey(m.types, m.initialTypeKey); ok {
				m.curType = t
			} else {
				m.curType = defaultType(m.types)
			}
		}
		return m, tea.Batch(m.listObjects(), m.tick())

	case objectsMsg:
		if msg.err != nil {
			// Keep the last data on screen; the tick keeps retrying (FR-016).
			m.disconnected = true
			m.errMsg = "cluster unreachable — retrying every " +
				fmt.Sprintf("%ds", m.cfg.RefreshIntervalSeconds) + " (" + truncate(msg.err.Error(), 60) + ")"
		} else {
			if m.disconnected {
				m.disconnected = false
				m.statusMsg = "✓ reconnected"
			}
			m.errMsg = ""
			m.objects = msg.objects
			if msg.nodePods != nil {
				m.nodePods = msg.nodePods
			}
			m.applyRows()
			// Broken-link visibility (US9): a workload/service whose selector
			// matches no pods is the classic "why is nothing routing" case.
			if m.drillSelector != "" && len(m.objects) == 0 {
				m.statusMsg = "⚠ " + m.drillFor + ": selector matches NO pods (broken link?) — Esc to go back"
			}
		}
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{m.tick()}
		if m.screen == screenList {
			cmds = append(cmds, m.listObjects())
		}
		if len(m.types) == 0 {
			// Startup discovery failed (cluster was unreachable): retry it.
			cmds = append(cmds, m.loadTypes())
		}
		return m, tea.Batch(cmds...)

	case logLineMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		} else if msg.Text != "" {
			line := msg.Text
			if msg.Pod != "" {
				// Merged multi-pod stream: color-code each pod's prefix so
				// interleaved lines are attributable at a glance.
				line = podPrefixStyle(msg.Pod).Render("["+msg.Pod+"]") + " " + msg.Text
			}
			// Accumulate in a real buffer — NEVER in the viewport's rendered
			// View(), which is windowed/padded and destroys the content.
			m.logBuf = append(m.logBuf, line)
			if len(m.logBuf) > logBufMax {
				m.logBuf = m.logBuf[len(m.logBuf)-logBufMax:]
			}
			m.logsView.SetContent(strings.Join(m.logBuf, "\n"))
			if !m.logPaused {
				m.logsView.GotoBottom()
			}
		}
		if !msg.Done {
			return m, m.nextLogLine()
		}
		return m, nil

	case metricsMsg:
		// Silent: where metrics come from is an implementation detail. The
		// views themselves show "unavailable" when Prometheus is missing.
		m.metrics = msg.client
		return m, nil

	case topMsg:
		m.topKind = msg.kind
		m.renderTop(msg.rows)
		return m, nil

	case diagMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.renderDiag(msg.rows)
		return m, nil

	case topologyMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.renderTopology(msg.nodes)
		return m, nil

	case eventsMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.eventRows = msg.rows
		m.recentSel = 0
		m.recentWin = 0
		m.renderEvents()
		return m, nil

	case helmMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.helmWin.SetRows(nil)
			m.helmWin.Sync(&m.helmTable)
		} else {
			m.errMsg = ""
			m.helmRows = msg.rows
			m.renderHelm()
		}
		return m, nil

	case helmDetailMsg:
		m.renderHelmDetail(msg)
		return m, nil

	case describeMsg:
		if msg.ns == m.detailNS && msg.name == m.detailName && m.screen == screenDetail {
			m.detail.SetContent(describeContent(m.detailObj, msg.events, m.theme) + msg.extra)
		}
		return m, nil

	case usageMsg:
		// Only apply if it matches the currently open detail object.
		if msg.ns == m.detailNS && msg.name == m.detailName {
			m.detailCPU, m.detailMem = msg.cpu, msg.mem
			m.detailHasUsage = true
			m.renderDetail()
		}
		return m, nil

	case errMsg:
		m.errMsg = msg.err.Error()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	// Delegate to the focused component.
	return m.delegate(msg)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Events filter typing mode captures ALL keys (so 'q', 'n', Esc… are text
	// or edit actions, never global shortcuts). Enter commits, Esc cancels.
	if m.screen == screenEvents && m.eventsFiltering {
		switch msg.Type {
		case tea.KeyEnter:
			m.eventsFiltering = false // keep the query (saved)
			m.renderEvents()
			return m, nil
		case tea.KeyEscape:
			m.eventsFiltering = false
			m.eventsQuery = "" // cancel
			m.renderEvents()
			return m, nil
		case tea.KeyBackspace:
			if m.eventsQuery != "" {
				m.eventsQuery = m.eventsQuery[:len(m.eventsQuery)-1]
				m.renderEvents()
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.eventsQuery += string(msg.Runes)
			m.renderEvents()
			return m, nil
		}
		return m, nil
	}

	// Picker type-to-filter captures printable keys BEFORE global shortcuts
	// (typing "configmaps" must not trigger 'm' mouse-toggle or 'q' quit).
	if m.screen == screenPicker {
		switch msg.Type {
		case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace:
			return m.handlePickerKey(msg)
		}
	}

	// Filtering mode captures typing.
	if m.filtering {
		switch msg.String() {
		case "enter", "esc":
			m.filtering = false
			m.filter.Blur()
			m.applyRows()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyRows()
		return m, cmd
	}

	switch {
	case hit(msg, m.keys.Quit):
		return m, tea.Quit
	case hit(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.layout()
		return m, nil
	case hit(msg, m.keys.Mouse):
		// Toggle mouse capture: OFF hands selection back to the terminal so
		// text can be copied; ON restores click/scroll support.
		m.mouseOn = !m.mouseOn
		if m.mouseOn {
			m.statusMsg = "mouse ON — clicks & scroll active"
			return m, tea.EnableMouseCellMotion
		}
		m.statusMsg = "mouse OFF — select & copy text freely ('m' to re-enable)"
		return m, tea.DisableMouse
	case hit(msg, m.keys.Back):
		return m.goBack()
	}

	switch m.screen {
	case screenList:
		return m.handleListKey(msg)
	case screenDetail, screenLogs:
		return m.handleScrollKey(msg)
	case screenTop:
		return m.handleTopKey(msg)
	case screenDiag:
		var cmd tea.Cmd
		m.diag, cmd = m.diag.Update(msg)
		return m, cmd
	case screenTopology:
		var cmd tea.Cmd
		m.topo, cmd = m.topo.Update(msg)
		return m, cmd
	case screenEvents:
		return m.handleEventsKey(msg)
	case screenHelm:
		return m.handleHelmKey(msg)
	case screenHelmHist:
		var cmd tea.Cmd
		m.helmHist, cmd = m.helmHist.Update(msg)
		return m, cmd
	case screenPicker:
		return m.handlePickerKey(msg)
	}
	return m, nil
}

func (m Model) handleTopKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if hit(msg, m.keys.Top) {
		// Toggle CPU <-> memory and refetch.
		if m.topKind == model.MetricCPU {
			m.topKind = model.MetricMemory
		} else {
			m.topKind = model.MetricCPU
		}
		if !m.metrics.Enabled() {
			return m, nil
		}
		m.usage.SetContent("loading top consumers…")
		return m, m.fetchTop(m.topKind)
	}
	var cmd tea.Cmd
	m.usage, cmd = m.usage.Update(msg)
	return m, cmd
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
		return m, nil
	case hit(msg, m.keys.Open):
		return m.openListSelection()
	case hit(msg, m.keys.Yaml):
		cmd := m.openDetail()
		return m, cmd
	case hit(msg, m.keys.Describe):
		return m, m.openDescribe()
	case hit(msg, m.keys.Owner):
		return m, m.gotoOwner()
	case hit(msg, m.keys.Logs):
		return m.openLogs()
	case hit(msg, m.keys.Namespace):
		return m.openPicker(pickNamespace)
	case hit(msg, m.keys.Context):
		return m.openPicker(pickContext)
	case hit(msg, m.keys.Jump):
		return m.openPicker(pickType)
	case hit(msg, m.keys.Top):
		return m.openTop()
	case hit(msg, m.keys.Diag):
		return m.openDiag()
	case hit(msg, m.keys.Topology):
		return m.openTopology()
	case hit(msg, m.keys.Events):
		return m.openEvents()
	case hit(msg, m.keys.Mark):
		m.toggleMark()
		return m, nil
	case hit(msg, m.keys.Sort):
		// Cycle through the current type's columns, then back to none.
		m.sortCol++
		if m.sortCol > len(m.columnsForType()) {
			m.sortCol = -1
		} else if m.sortCol < 1 {
			m.sortCol = 1
		}
		m.sortAsc = true
		m.applyRows()
		return m, nil
	case hit(msg, m.keys.SortDir):
		if m.sortCol >= 1 {
			m.sortAsc = !m.sortAsc
			m.applyRows()
		}
		return m, nil
	}
	m.navigate(&m.win, msg)
	return m, nil
}

// toggleMark marks/unmarks the resource under the cursor (Space). Marked
// resources scope the failures ('f') and events ('v') views.
func (m *Model) toggleMark() {
	obj, ok := m.selectedObject()
	if !ok {
		return
	}
	key := obj.Namespace + "/" + obj.Name
	if _, ok := m.marked[key]; ok {
		delete(m.marked, key)
	} else {
		m.marked[key] = obj
	}
	if len(m.marked) > 0 {
		m.statusMsg = fmt.Sprintf("%d marked — 'f'/'v' scope to the selection, Space to unmark", len(m.marked))
	} else {
		m.statusMsg = ""
	}
	m.applyRows()
}

// openListSelection is the Enter action of the list: drill into a workload's
// pods, or open the YAML detail (k9s-like).
func (m Model) openListSelection() (tea.Model, tea.Cmd) {
	if cmd, ok := m.drillIntoPods(); ok {
		return m, cmd
	}
	cmd := m.openDetail()
	return m, cmd
}

// handleMouse implements click/double-click/wheel across screens (US7).
// Row mapping is exact thanks to winTable windowing.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Wheel: tables move the selection; viewports scroll natively.
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -3
		}
		switch m.screen {
		case screenList:
			m.win.Move(delta)
			return m, nil
		case screenHelm:
			m.helmWin.Move(delta)
			m.helmWin.Sync(&m.helmTable)
			return m, nil
		case screenPicker:
			m.pickerWin.Move(delta)
			m.pickerWin.Sync(&m.picker)
			return m, nil
		default:
			return m.delegate(msg)
		}
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m.delegate(msg)
	}

	// Clickable on-screen controls (T048): header chips and footer labels act
	// exactly like pressing their key.
	if msg.Y == 0 && m.screen != screenPicker {
		for _, z := range m.headerZones() {
			if msg.X >= z.x0 && msg.X < z.x1 {
				// Direct actions so the chips work from every view.
				switch z.key {
				case "c":
					return m.openPicker(pickContext)
				case "n":
					return m.openPicker(pickNamespace)
				case ":":
					return m.openPicker(pickType)
				}
			}
		}
	}
	if msg.Y == m.bodyH+4 && !m.help.ShowAll { // shortcuts line
		for _, z := range m.footerZones() {
			if msg.X >= z.x0 && msg.X < z.x1 {
				return m.handleKey(keyMsgFor(z.key))
			}
		}
		return m, nil
	}

	// Double-click detection (same screen + same row within 500 ms).
	now := time.Now()
	doubleClick := func(row int) bool {
		is := m.lastClickScreen == m.screen && m.lastClickRow == row && now.Sub(m.lastClickAt) < 500*time.Millisecond
		m.lastClickScreen, m.lastClickRow, m.lastClickAt = m.screen, row, now
		return is
	}

	switch m.screen {
	case screenList:
		// Column header (y=2): click sorts by that column, again = reverse.
		if msg.Y == 2 {
			if col, ok := m.columnAt(msg.X); ok && col >= 1 {
				if m.sortCol == col {
					m.sortAsc = !m.sortAsc
				} else {
					m.sortCol, m.sortAsc = col, true
				}
				m.applyRows()
			}
			return m, nil
		}
		// y0 header, y1 rule, y2 column header → rows start at y=3.
		if m.win.ClickVisible(msg.Y - 3) {
			if doubleClick(m.win.cursor) {
				return m.openListSelection()
			}
		}
		return m, nil
	case screenHelm:
		if m.helmWin.ClickVisible(msg.Y - 3) {
			m.helmWin.Sync(&m.helmTable)
			if doubleClick(m.helmWin.cursor) {
				return m.openHelmDetail(false)
			}
		}
		return m, nil
	case screenPicker:
		_, geom := m.pickerModal()
		inBox := msg.X >= geom.x && msg.X < geom.x+geom.w &&
			msg.Y >= geom.y && msg.Y < geom.y+geom.h
		if !inBox {
			return m.goBack() // click outside the modal closes it
		}
		if rel := msg.Y - geom.optTop; rel >= 0 && rel < geom.optRows {
			if m.pickerWin.ClickVisible(rel) {
				m.pickerWin.Sync(&m.picker)
				if doubleClick(m.pickerWin.cursor) {
					return m.pickerSelect()
				}
			}
		}
		return m, nil
	case screenEvents:
		contentLine := m.events.YOffset + msg.Y - 2 // y0 header, y1 rule → viewport at y=2
		// The first content line holds the kind selector and filter controls.
		if contentLine == 0 {
			if msg.X >= m.eventsKindZone.x0 && msg.X < m.eventsKindZone.x1 {
				return m.openKindPicker()
			}
			if msg.X >= m.eventsFilterHit.x0 && msg.X < m.eventsFilterHit.x1 {
				m.eventsFiltering = true
				m.renderEvents()
				return m, nil
			}
			return m, nil
		}
		// Click a line of the detail list to select it on the timeline.
		k := contentLine - m.recentBaseLine
		if k >= 0 && k < m.recentShown {
			m.recentSel = m.recentWin + k
			m.renderEvents()
		}
		return m, nil
	default:
		return m.delegate(msg)
	}
}

// navigate applies standard movement keys to a windowed table.
func (m *Model) navigate(w *winTable, msg tea.KeyMsg) {
	switch {
	case hit(msg, m.keys.Up):
		w.Move(-1)
	case hit(msg, m.keys.Down):
		w.Move(1)
	case hit(msg, m.keys.PageUp):
		w.Page(-1)
	case hit(msg, m.keys.PageDown):
		w.Page(1)
	case hit(msg, m.keys.Home):
		w.Home()
	case hit(msg, m.keys.End):
		w.End()
	}
}

func (m Model) handleScrollKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.screen == screenDetail {
		// Toggle secret reveal with 'x' on the detail of a Secret.
		if msg.String() == "x" && strings.EqualFold(m.curType.Kind, "Secret") {
			m.revealSecret = !m.revealSecret
			m.renderDetail()
			return m, nil
		}
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
	// Logs: Space pauses/resumes the follow; scrolling up pauses so the view
	// stops being yanked to the bottom; End resumes at the tail (FR-005).
	switch {
	case hit(msg, m.keys.Pause):
		m.logPaused = !m.logPaused
		if m.logPaused {
			m.statusMsg = "logs paused — Space or End to resume"
		} else {
			m.statusMsg = ""
			m.logsView.GotoBottom()
		}
		return m, nil
	case hit(msg, m.keys.End):
		m.logPaused = false
		m.statusMsg = ""
		m.logsView.GotoBottom()
		return m, nil
	case hit(msg, m.keys.Up) || hit(msg, m.keys.PageUp):
		if !m.logPaused {
			m.logPaused = true
			m.statusMsg = "logs paused — Space or End to resume"
		}
	}
	m.logsView, cmd = m.logsView.Update(msg)
	return m, cmd
}

func (m Model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Enter selects the highlighted option.
	if hit(msg, m.keys.Open) {
		return m.pickerSelect()
	}
	// Arrow/page navigation on the windowed table.
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		m.navigate(&m.pickerWin, msg)
		m.pickerWin.Sync(&m.picker)
		return m, nil
	case tea.KeyBackspace:
		if m.pickerQuery != "" {
			m.pickerQuery = m.pickerQuery[:len(m.pickerQuery)-1]
			m.applyPickerRows()
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		// Type-to-filter: narrow the options as the user types (k9s-style ":").
		m.pickerQuery += string(msg.Runes)
		m.applyPickerRows()
		return m, nil
	}
	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	return m, cmd
}

func (m Model) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	case screenLogs:
		m.logsView, cmd = m.logsView.Update(msg)
	case screenTop:
		m.usage, cmd = m.usage.Update(msg)
	case screenDiag:
		m.diag, cmd = m.diag.Update(msg)
	case screenTopology:
		m.topo, cmd = m.topo.Update(msg)
	case screenEvents:
		m.events, cmd = m.events.Update(msg)
	case screenHelm:
		m.helmTable, cmd = m.helmTable.Update(msg)
	case screenHelmHist:
		m.helmHist, cmd = m.helmHist.Update(msg)
	case screenPicker:
		m.picker, cmd = m.picker.Update(msg)
	}
	return m, cmd
}

// ---- Actions ----

func (m *Model) goBack() (tea.Model, tea.Cmd) {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	m.revealSecret = false
	// Esc on the helm history returns to the helm release list.
	if m.screen == screenHelmHist {
		m.screen = screenHelm
		m.layout()
		return m, nil
	}
	// Esc on a drilled pod list returns to the workload list it came from.
	if m.screen == screenList && m.drillSelector != "" {
		cmd := m.exitDrill()
		m.layout()
		return m, cmd
	}
	if m.screen == screenPicker && m.pickerReturn != screenPicker {
		// A picker opened from a sub-view (e.g. the timeline) returns there.
		m.screen = m.pickerReturn
	} else {
		m.screen = screenList
	}
	m.layout()
	return m, nil
}

// describeContent builds a kubectl-describe-like summary from the object plus
// its own events (nil while they load).
func describeContent(obj model.ResourceObject, events []model.Event, th theme.Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", th.Title.Render("Describe: "+obj.Type.Kind+"/"+obj.Name))
	fmt.Fprintf(&b, "Namespace:  %s\n", orDash(obj.Namespace))
	fmt.Fprintf(&b, "Created:    %s (%s ago)\n", obj.CreatedAt.Format(time.RFC3339), kube.Age(obj.CreatedAt, time.Now()))
	fmt.Fprintf(&b, "Status:     %s %s", obj.Status.Level.Symbol(), obj.Status.Level.Label())
	if obj.Status.Reason != "" {
		fmt.Fprintf(&b, " (%s)", obj.Status.Reason)
	}
	b.WriteString("\n")

	if labels, found, _ := unstructured.NestedStringMap(obj.Raw, "metadata", "labels"); found && len(labels) > 0 {
		b.WriteString("\nLabels:\n")
		writeSortedMap(&b, labels)
	}
	if ann, found, _ := unstructured.NestedStringMap(obj.Raw, "metadata", "annotations"); found && len(ann) > 0 {
		b.WriteString("\nAnnotations:\n")
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		writeSortedMap(&b, ann)
	}

	if conds, found, _ := unstructured.NestedSlice(obj.Raw, "status", "conditions"); found && len(conds) > 0 {
		b.WriteString("\nConditions:\n")
		fmt.Fprintf(&b, "  %-28s %-7s %-24s %s\n", "TYPE", "STATUS", "REASON", "MESSAGE")
		for _, c := range conds {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			ctype, _ := cm["type"].(string)
			cstatus, _ := cm["status"].(string)
			reason, _ := cm["reason"].(string)
			message, _ := cm["message"].(string)
			fmt.Fprintf(&b, "  %-28s %-7s %-24s %s\n", ctype, cstatus, orDash(reason), truncate(orDash(message), 60))
		}
	}

	b.WriteString("\nEvents:\n")
	if len(events) == 0 {
		b.WriteString(th.Faint.Render("  none in the current retention window"))
		b.WriteString("\n")
	} else {
		now := time.Now()
		for i, e := range events {
			if i >= 15 {
				break
			}
			cnt := ""
			if e.Count > 1 {
				cnt = fmt.Sprintf(" x%d", e.Count)
			}
			line := fmt.Sprintf("  %-5s %s %s%s — %s", kube.Age(e.Time, now), eventBadge(e), e.Reason, cnt, truncate(e.Message, 80))
			if e.Warning() {
				b.WriteString(th.Warning.Render(line))
			} else {
				b.WriteString(th.Faint.Render(line))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeSortedMap(b *strings.Builder, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "  %s: %s\n", k, m[k])
	}
}

// drillIntoPods switches the list to the pods owned by the selected workload
// (Deployment, ReplicaSet, StatefulSet, DaemonSet, Job, Service) via its label
// selector. ok=false when the selection has no pod selector (e.g. a Pod).
func (m *Model) drillIntoPods() (tea.Cmd, bool) {
	obj, found := m.selectedObject()
	if !found || strings.EqualFold(m.curType.Kind, "Pod") || m.drillSelector != "" {
		return nil, false
	}
	sel, ok := kube.PodSelector(obj.Raw)
	if !ok {
		return nil, false
	}
	pods, ok := findTypeByKey(m.types, "v1/pods")
	if !ok {
		pods = model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	}
	m.drillPrevType = m.curType
	m.drillSelector = sel
	m.drillFor = m.curType.Kind + "/" + obj.Name
	m.drillNamespace = obj.Namespace // query scope only; ns filter untouched
	m.curType = pods
	m.statusMsg = "pods of " + m.drillFor + " — Esc to go back"
	return m.listObjects(), true
}

// gotoOwner walks one step UP the ownership chain (US9): from a Pod to its
// ReplicaSet, from a ReplicaSet to its Deployment, etc. It switches the list
// to the owner's type with the filter pre-set to the owner's name, so pressing
// 'o' repeatedly climbs the chain.
func (m *Model) gotoOwner() tea.Cmd {
	obj, found := m.selectedObject()
	if !found {
		return nil
	}
	ref, ok := kube.Owner(obj.Raw)
	if !ok {
		m.statusMsg = m.curType.Kind + "/" + obj.Name + " has no owner (top of the chain)"
		return nil
	}
	var ownerType model.ResourceType
	okType := false
	for _, t := range m.types {
		if t.Group == ref.Group && t.Version == ref.Version && t.Kind == ref.Kind {
			ownerType, okType = t, true
			break
		}
	}
	if !okType {
		m.statusMsg = fmt.Sprintf("owner %s/%s: type not browsable", ref.Kind, ref.Name)
		return nil
	}
	// Leave any drill; land on the owner's list, filtered to its name.
	m.drillSelector, m.drillFor, m.drillNamespace = "", "", ""
	m.curType = ownerType
	m.filter.SetValue(ref.Name)
	m.statusMsg = "owner of " + obj.Name + ": " + ref.Kind + "/" + ref.Name
	return m.listObjects()
}

// exitDrill restores the workload list the drill-down came from.
func (m *Model) exitDrill() tea.Cmd {
	m.curType = m.drillPrevType
	m.drillSelector = ""
	m.drillFor = ""
	m.drillNamespace = ""
	m.statusMsg = ""
	return m.listObjects()
}

// openDescribe shows a describe-style summary (metadata, conditions) plus the
// object's own events — like `kubectl describe`, read-only.
func (m *Model) openDescribe() tea.Cmd {
	obj, found := m.selectedObject()
	if !found {
		return nil
	}
	m.detailObj = obj
	m.detailNS, m.detailName = obj.Namespace, obj.Name
	m.detailHasUsage = false
	m.screen = screenDetail
	m.detail.SetContent(describeContent(obj, nil, m.theme) + "\nEvents: loading…")
	m.detail.GotoTop()
	m.layout()
	cl, ns, name := m.client, obj.Namespace, obj.Name
	kind := m.curType.Kind
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows, _ := cl.Events(ctx, ns)
		var own []model.Event
		for _, e := range rows {
			if e.ObjName == name {
				own = append(own, e)
			}
		}
		// Services: surface the backends so a broken link (0 ready endpoints)
		// is visible at describe time (US9, SC-015).
		extra := ""
		if strings.EqualFold(kind, "Service") {
			ready, notReady, err := cl.EndpointsSummary(ctx, ns, name)
			switch {
			case err != nil:
				extra = "\nEndpoints: unavailable (" + err.Error() + ")\n"
			case ready == 0:
				extra = fmt.Sprintf("\nEndpoints: ⚠ 0 ready (%d not ready) — service has NO backends\n", notReady)
			default:
				extra = fmt.Sprintf("\nEndpoints: %d ready, %d not ready\n", ready, notReady)
			}
		}
		return describeMsg{ns: ns, name: name, events: own, extra: extra}
	}
}

func (m *Model) openDetail() tea.Cmd {
	obj, ok := m.selectedObject()
	if !ok {
		return nil
	}
	m.detailObj = obj
	m.detailNS, m.detailName = obj.Namespace, obj.Name
	m.detailHasUsage = false
	m.detailCPU, m.detailMem = model.Usage{}, model.Usage{}
	m.screen = screenDetail
	m.renderDetail()
	m.detail.GotoTop()
	m.layout()
	// For pods, fetch usage (gauges + 1h trend) from Prometheus, if configured.
	if m.metrics.Enabled() && strings.EqualFold(m.curType.Kind, "Pod") {
		return m.fetchPodUsage(obj.Namespace, obj.Name, obj.Raw)
	}
	return nil
}

// renderDetail composes the detail content: an optional usage panel (pods) plus
// the cleaned object YAML. It does not change scroll position (so a late-arriving
// usage update does not jump the view).
func (m *Model) renderDetail() {
	raw := cleanForDisplay(m.detailObj.Raw)
	if strings.EqualFold(m.curType.Kind, "Secret") && !m.revealSecret {
		raw = maskSecret(raw)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		m.errMsg = err.Error()
		return
	}
	var b strings.Builder
	if m.detailHasUsage {
		b.WriteString(m.usagePanel())
		b.WriteString("\n")
	}
	if strings.EqualFold(m.curType.Kind, "Secret") {
		if m.revealSecret {
			b.WriteString("# secret values REVEALED — press 'x' to mask\n")
		} else {
			b.WriteString("# secret values masked — press 'x' to reveal\n")
		}
	}
	b.WriteString(m.colorizeYAML(string(out)))
	m.detail.SetContent(b.String())
}

func (m Model) usagePanel() string {
	w := 20
	return "Usage (last 1h):\n" +
		"  " + components.UsageLine("CPU", m.detailCPU, w) + "\n" +
		"  " + components.UsageLine("MEM", m.detailMem, w) + "\n"
}

func (m *Model) openTop() (tea.Model, tea.Cmd) {
	m.screen = screenTop
	m.topKind = model.MetricCPU
	if !m.metrics.Enabled() {
		m.usage.SetContent("Top consumers — metrics unavailable.\n\n" +
			"No in-cluster Prometheus was auto-discovered for this context, or it is\n" +
			"not reachable via the API server proxy. You can force one with\n" +
			"--prometheus-url (Prometheus is the single metrics source).")
		m.layout()
		return m, nil
	}
	m.usage.SetContent("loading top consumers…")
	m.layout()
	return m, m.fetchTop(model.MetricCPU)
}

func (m *Model) openEvents() (tea.Model, tea.Cmd) {
	m.screen = screenEvents
	m.eventsQuery = ""
	// Contextual: opening the timeline from a list pre-sets the kind filter to
	// the type being browsed (e.g. Deployment from the deployments list).
	// Clear or change it with 'k'.
	m.eventsKind = m.curType.Kind
	// Marked resources (Space) scope the timeline first; otherwise a drilled
	// pod list scopes to exactly those pods.
	m.eventsScope = nil
	m.eventsScopeFor = ""
	if len(m.marked) > 0 {
		scope := make(map[string]bool, len(m.marked))
		for k := range m.marked {
			scope[k] = true
		}
		m.eventsScope = scope
		m.eventsScopeFor = fmt.Sprintf("%d marked", len(m.marked))
	} else if m.drillSelector != "" && len(m.objects) > 0 {
		scope := make(map[string]bool, len(m.objects))
		for _, o := range m.objects {
			scope[o.Namespace+"/"+o.Name] = true
		}
		m.eventsScope = scope
		m.eventsScopeFor = m.drillFor
	}
	m.events.SetContent("loading events…")
	m.layout()
	return m, m.fetchEvents()
}

// handleEventsKey (outside typing mode): '/' edits the filter, 'k' opens the
// kind selector, 'n' the namespace picker. ↑/↓ move the Recent selection
// (highlighted on the timeline); PgUp/PgDn scroll the view.
func (m Model) handleEventsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Filter):
		m.eventsFiltering = true
		m.renderEvents()
		return m, nil
	case hit(msg, m.keys.Kind):
		return m.openKindPicker()
	case hit(msg, m.keys.WarnOnly):
		m.eventsWarnOnly = !m.eventsWarnOnly
		m.recentSel, m.recentWin = 0, 0
		m.renderEvents()
		return m, nil
	case hit(msg, m.keys.Namespace):
		return m.openPicker(pickNamespace)
	}
	switch msg.Type {
	case tea.KeyUp:
		if m.recentSel > 0 {
			m.recentSel--
			m.renderEvents()
		}
		return m, nil
	case tea.KeyDown:
		m.recentSel++ // clamped in renderEvents
		m.renderEvents()
		return m, nil
	}
	var cmd tea.Cmd
	m.events, cmd = m.events.Update(msg)
	return m, cmd
}

// openKindPicker offers the object kinds present in the current events — the
// TUI equivalent of a dropdown at the top of the timeline.
func (m Model) openKindPicker() (tea.Model, tea.Cmd) {
	seen := map[string]bool{}
	opts := []string{}
	for _, e := range m.eventRows {
		if e.ObjKind != "" && !seen[e.ObjKind] {
			seen[e.ObjKind] = true
			opts = append(opts, e.ObjKind)
		}
	}
	sort.Strings(opts)
	m.pickerKind = pickEventKind
	m.pickerReturn = screenEvents
	m.pickerQuery = ""
	m.pickerOpts = append([]string{allKindsLabel}, opts...)
	rows := make([]table.Row, len(m.pickerOpts))
	for i, o := range m.pickerOpts {
		rows[i] = table.Row{o}
	}
	m.picker.SetColumns([]table.Column{{Title: "select kind (type to filter)", Width: max(20, m.width-4)}})
	m.pickerWin.SetRows(rows)
	m.pickerWin.Sync(&m.picker)
	m.screen = screenPicker
	m.layout()
	return m, nil
}

const (
	timelineLaneWidth = 30 // width of the object-name column
	timelineMaxLanes  = 25 // lanes shown before "+N more"
)

// renderEvents draws a visual timeline: a time axis, one lane per object, and
// markers placed proportionally to when each event happened — so you can SEE
// what happened when. The most recent events are detailed below the graph.
func (m *Model) renderEvents() {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	q := strings.ToLower(strings.TrimSpace(m.eventsQuery))

	// Filter: kind selector ('k') + text query ('/'). The text query matches
	// the OBJECT identity only (namespace/kind/name) — not reasons/messages —
	// so typing "back" matches pods named *back*, not every BackOff event.
	var evs []model.Event
	for _, e := range m.eventRows {
		if m.eventsWarnOnly && !e.Warning() {
			continue // severity filter: warnings only (FR-014)
		}
		if m.eventsScope != nil && !m.eventsScope[e.Namespace+"/"+e.ObjName] {
			continue // scoped to a drilled workload's own pods
		}
		if m.eventsKind != "" && e.ObjKind != m.eventsKind {
			continue
		}
		ident := e.Namespace + "/" + e.ObjKind + "/" + e.ObjName
		if q != "" && !strings.Contains(strings.ToLower(ident), q) {
			continue
		}
		if e.Time.IsZero() {
			continue
		}
		evs = append(evs, e)
	}

	kindLabel := m.eventsKind
	if kindLabel == "" {
		kindLabel = "All"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Events timeline — %s   ", scope)
	if m.eventsScopeFor != "" {
		b.WriteString(m.theme.StatusBar.Render("scope:["+m.eventsScopeFor+"]") + "  ")
	}
	// The kind selector and filter hint are clickable (zones by width, T048).
	m.eventsKindZone = clickZone{x0: lipgloss.Width(b.String())}
	b.WriteString(m.theme.StatusBar.Render("kind:[" + kindLabel + " ▾]"))
	m.eventsKindZone.x1 = lipgloss.Width(b.String())
	if m.eventsWarnOnly {
		b.WriteString("  " + m.theme.Warning.Render("▲ warnings only ('w' toggles)"))
	}
	m.eventsFilterHit = clickZone{x0: lipgloss.Width(b.String())}
	switch {
	case m.eventsFiltering:
		b.WriteString("  filter: " + m.eventsQuery + "▏" + m.theme.Faint.Render("  (Enter: save, Esc: cancel)"))
	case q != "":
		b.WriteString("  filter: " + m.eventsQuery + m.theme.Faint.Render("  (/ to edit)"))
	default:
		b.WriteString(m.theme.Faint.Render("  (/ filter, k kind, w warnings, n namespace)"))
	}
	m.eventsFilterHit.x1 = lipgloss.Width(b.String())
	b.WriteString("\n\n")

	if len(evs) == 0 {
		b.WriteString(m.theme.Faint.Render("no events match"))
		m.events.SetContent(b.String())
		m.events.GotoTop()
		return
	}

	// Time window across the filtered events (they arrive most-recent first).
	start, end := evs[0].Time, evs[0].Time
	for _, e := range evs {
		if e.Time.Before(start) {
			start = e.Time
		}
		if e.Time.After(end) {
			end = e.Time
		}
	}
	span := end.Sub(start)
	if span < time.Minute {
		span = time.Minute
		start = end.Add(-span)
	}

	axisW := m.width - timelineLaneWidth - 6
	if axisW < 20 {
		axisW = 20
	}
	pos := func(t time.Time) int {
		p := int(float64(t.Sub(start)) / float64(span) * float64(axisW-1))
		if p < 0 {
			p = 0
		}
		if p >= axisW {
			p = axisW - 1
		}
		return p
	}

	// Group events into lanes (one per object), most recent activity first.
	type lane struct {
		name   string
		latest time.Time
		events []model.Event
	}
	byObj := map[string]*lane{}
	var order []*lane
	for _, e := range evs {
		key := e.Namespace + "/" + e.ObjKind + "/" + e.ObjName
		l, ok := byObj[key]
		if !ok {
			l = &lane{name: e.ObjKind + "/" + e.ObjName}
			byObj[key] = l
			order = append(order, l)
		}
		l.events = append(l.events, e)
		if e.Time.After(l.latest) {
			l.latest = e.Time
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].latest.After(order[j].latest) })

	// Time labels: start / mid / end above the axis.
	lbl := func(t time.Time) string { return t.Local().Format("15:04") }
	labels := make([]byte, axisW)
	for i := range labels {
		labels[i] = ' '
	}
	placeLabel := func(p int, s string) {
		if p+len(s) > axisW {
			p = axisW - len(s)
		}
		if p < 0 {
			p = 0
		}
		copy(labels[p:], s)
	}
	// \x01 marks label padding so the dash-filler leaves a space around times.
	placeLabel(0, "\x01"+lbl(start)+"\x01")
	placeLabel(axisW/2-3, "\x01"+lbl(start.Add(span/2))+"\x01")
	placeLabel(axisW-7, "\x01"+lbl(end)+"\x01")
	// Section rule with the time labels embedded: ── Timeline ── 11:49 ── …
	lead := "── "
	title := "Timeline"
	gap := timelineLaneWidth + 2 - len(lead) - len(title) - 1
	if gap < 1 {
		gap = 1
	}
	axisRule := make([]byte, axisW)
	for i := range axisRule {
		if labels[i] == ' ' {
			axisRule[i] = '-'
		} else {
			axisRule[i] = labels[i]
		}
	}
	ruleLine := strings.ReplaceAll(string(axisRule), "-", "─")
	ruleLine = strings.ReplaceAll(ruleLine, "\x01", " ")
	b.WriteString(m.theme.Faint.Render(lead) + m.theme.TableHeader.Render(title) +
		m.theme.Faint.Render(" "+strings.Repeat("─", gap)+ruleLine))
	b.WriteString("\n")

	// Selection: ↑/↓ walk ALL filtered events (most recent first); the detail
	// list below is a sliding window that follows the selection, and the
	// selected event lights up on the timeline.
	const recentWinSize = 8
	if m.recentSel >= len(evs) {
		m.recentSel = len(evs) - 1
	}
	if m.recentSel < 0 {
		m.recentSel = 0
	}
	if m.recentSel < m.recentWin {
		m.recentWin = m.recentSel
	}
	if m.recentSel >= m.recentWin+recentWinSize {
		m.recentWin = m.recentSel - recentWinSize + 1
	}
	if maxWin := len(evs) - recentWinSize; m.recentWin > maxWin {
		m.recentWin = maxWin
	}
	if m.recentWin < 0 {
		m.recentWin = 0
	}
	var sel *model.Event
	if len(evs) > 0 {
		sel = &evs[m.recentSel]
	}
	sameObj := func(e model.Event, l *model.Event) bool {
		return l != nil && e.Namespace == l.Namespace && e.ObjKind == l.ObjKind && e.ObjName == l.ObjName
	}

	// Lanes with proportionally placed markers.
	shown := order
	if len(shown) > timelineMaxLanes {
		shown = shown[:timelineMaxLanes]
	}
	for _, l := range shown {
		type cell struct {
			count   int
			warning bool
		}
		cells := make([]cell, axisW)
		laneSelected := len(l.events) > 0 && sameObj(l.events[0], sel)
		selPos := -1
		if laneSelected && sel != nil {
			selPos = pos(sel.Time)
		}
		for _, e := range l.events {
			p := pos(e.Time)
			cells[p].count += maxInt(e.Count, 1)
			cells[p].warning = cells[p].warning || e.Warning()
		}
		var row strings.Builder
		name := fmt.Sprintf("%-*s", timelineLaneWidth, truncate(l.name, timelineLaneWidth))
		if laneSelected {
			row.WriteString(m.theme.Selected.Render(name))
		} else {
			row.WriteString(name)
		}
		row.WriteString(" │")
		for i, c := range cells {
			switch {
			case i == selPos && c.count > 0:
				// The selected event's bucket: inverse video so it pops out.
				row.WriteString(m.theme.Highlight.Render(marker(c.count, markerSym(c.warning))))
			case c.count == 0:
				row.WriteString(m.theme.Faint.Render("·"))
			case c.warning:
				row.WriteString(m.theme.Error.Render(marker(c.count, "▲")))
			default:
				row.WriteString(m.theme.Ok.Render(marker(c.count, "•")))
			}
		}
		row.WriteString("│")
		b.WriteString(row.String())
		b.WriteString("\n")
	}
	if len(order) > timelineMaxLanes {
		fmt.Fprintf(&b, "%s\n", m.theme.Faint.Render(fmt.Sprintf("… +%d more objects (type to filter)", len(order)-timelineMaxLanes)))
	}

	// Legend + a sliding detail window over ALL events, following the selection.
	b.WriteString("\n")
	b.WriteString(m.theme.Faint.Render("▲ warning   • normal   (digit = repeated events)   ↑/↓ select"))
	b.WriteString("\n")
	b.WriteString(m.rule(fmt.Sprintf("Events (%d/%d)", m.recentSel+1, len(evs))))
	b.WriteString("\n")
	now := time.Now()
	winEnd := m.recentWin + recentWinSize
	if winEnd > len(evs) {
		winEnd = len(evs)
	}
	if m.recentWin > 0 {
		b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  ↑ %d more recent", m.recentWin)))
		b.WriteString("\n")
	}
	// Record where the detail rows start (content line) for click-to-select.
	m.recentBaseLine = strings.Count(b.String(), "\n")
	m.recentShown = winEnd - m.recentWin
	for i := m.recentWin; i < winEnd; i++ {
		e := evs[i]
		cnt := ""
		if e.Count > 1 {
			cnt = fmt.Sprintf(" x%d", e.Count)
		}
		cursor := "  "
		if i == m.recentSel {
			cursor = "▶ "
		}
		line := fmt.Sprintf("%s%-4s %s %-34s %s%s — %s",
			cursor, kube.Age(e.Time, now), eventBadge(e), truncate(e.ObjKind+"/"+e.ObjName, 34), e.Reason, cnt, truncate(e.Message, 60))
		switch {
		case i == m.recentSel:
			b.WriteString(m.theme.Selected.Render(line))
		case e.Warning():
			b.WriteString(m.theme.Warning.Render(line))
		default:
			b.WriteString(m.theme.Faint.Render(line))
		}
		b.WriteString("\n")
	}
	if winEnd < len(evs) {
		b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  ↓ %d older", len(evs)-winEnd)))
		b.WriteString("\n")
	}

	m.events.SetContent(b.String())
	m.events.GotoTop()
}

// markerSym picks the base symbol for a timeline cell.
func markerSym(warning bool) string {
	if warning {
		return "▲"
	}
	return "•"
}

// marker renders a single-cell marker: the symbol, or a digit when several
// events share the same time bucket.
func marker(count int, sym string) string {
	if count <= 1 {
		return sym
	}
	if count > 9 {
		return "+"
	}
	return string(rune('0' + count))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func eventBadge(e model.Event) string {
	if e.Warning() {
		return "▲"
	}
	return "•"
}

// ---- Helm (US12, read-only) ----

func (m *Model) openHelm() (tea.Model, tea.Cmd) {
	if m.helm == nil {
		m.statusMsg = "helm view unavailable (no helm reader configured)"
		return m, nil
	}
	m.screen = screenHelm
	m.helmTable.SetColumns([]table.Column{
		{Title: "NAMESPACE", Width: 22},
		{Title: "RELEASE", Width: 28},
		{Title: "CHART", Width: max(16, m.width-100)},
		{Title: "VERSION", Width: 12},
		{Title: "REV", Width: 5},
		{Title: "STATUS", Width: 14},
		{Title: "UPDATED", Width: 9},
	})
	m.helmWin.SetRows([]table.Row{{"", "loading helm releases…", "", "", "", "", ""}})
	m.helmWin.Sync(&m.helmTable)
	m.layout()
	hc, ns := m.helm, m.client.Namespace
	return m, func() tea.Msg {
		rows, err := hc.Releases(ns)
		return helmMsg{rows: rows, err: err}
	}
}

func (m *Model) renderHelm() {
	now := time.Now()
	rows := make([]table.Row, 0, len(m.helmRows))
	for _, r := range m.helmRows {
		rows = append(rows, table.Row{
			r.Namespace, r.Name, r.Chart, r.ChartVersion,
			fmt.Sprintf("%d", r.Revision),
			r.Health().Symbol() + " " + r.Status,
			kube.Age(r.Updated, now),
		})
	}
	m.helmWin.SetRows(rows)
	m.helmWin.Sync(&m.helmTable)
}

func (m Model) handleHelmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Open):
		return m.openHelmDetail(false)
	case hit(msg, m.keys.Values):
		return m.openHelmDetail(true) // values only
	}
	m.navigate(&m.helmWin, msg)
	m.helmWin.Sync(&m.helmTable)
	return m, nil
}

// openHelmDetail opens the release detail (history + resources + values), or
// only the values when valuesOnly is set ('v' — quick copy-friendly view).
func (m Model) openHelmDetail(valuesOnly bool) (tea.Model, tea.Cmd) {
	row, okRow := m.helmWin.Selected()
	_ = okRow
	if len(row) < 2 || m.helm == nil {
		return m, nil
	}
	ns, name := row[0], row[1]
	m.helmValuesOnly = valuesOnly
	m.helmHist.SetContent("loading release " + ns + "/" + name + "…")
	m.screen = screenHelmHist
	m.layout()
	hc, kc, types := m.helm, m.client, m.types
	return m, func() tea.Msg {
		det, err := hc.Detail(ns, name)
		out := helmDetailMsg{ns: ns, name: name, detail: det, err: err}
		if err != nil || valuesOnly {
			// Values-only view needs no live resource checks.
			return out
		}
		// Live-check each deployed resource against the cluster (read-only
		// GETs) so drift and broken deploys are visible.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out.live = make([]helmResLive, len(det.Resources))
		for i, r := range det.Resources {
			t, ok := typeForManifest(types, r)
			if !ok {
				continue // known=false
			}
			rns := r.Namespace
			if rns == "" {
				rns = ns
			}
			st, found, gerr := kc.GetObjectStatus(ctx, t, rns, r.Name)
			if gerr != nil {
				continue
			}
			out.live[i] = helmResLive{status: st, found: found, known: true}
		}
		return out
	}
}

// typeForManifest resolves a manifest head (apiVersion+kind) to a discovered,
// browsable resource type.
func typeForManifest(types []model.ResourceType, r model.HelmResource) (model.ResourceType, bool) {
	group, version := "", r.APIVersion
	if i := strings.Index(r.APIVersion, "/"); i >= 0 {
		group, version = r.APIVersion[:i], r.APIVersion[i+1:]
	}
	for _, t := range types {
		if t.Group == group && t.Version == version && t.Kind == r.Kind {
			return t, true
		}
	}
	return model.ResourceType{}, false
}

// renderHelmDetail shows everything about a release: history, the resources
// the chart deployed (with their LIVE state), and the user-supplied values.
// In values-only mode ('v') it renders just the values.
func (m *Model) renderHelmDetail(msg helmDetailMsg) {
	var b strings.Builder
	title := "Helm release — " + msg.ns + "/" + msg.name + " (read-only)"
	if m.helmValuesOnly {
		title = "Helm values — " + msg.ns + "/" + msg.name
	}
	fmt.Fprintf(&b, "%s\n\n", m.theme.Title.Render(title))
	if msg.err != nil {
		b.WriteString(m.theme.Error.Render("⚠ " + msg.err.Error()))
		m.helmHist.SetContent(b.String())
		return
	}
	if m.helmValuesOnly {
		if msg.detail.Values == "" {
			b.WriteString(m.theme.Faint.Render("(none — chart defaults)"))
			b.WriteString("\n")
		} else {
			b.WriteString(m.colorizeYAML(strings.TrimRight(msg.detail.Values, "\n")))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(m.theme.Faint.Render("tip: press 'm' to disable the mouse and copy text"))
		m.helmHist.SetContent(b.String())
		m.helmHist.GotoTop()
		return
	}
	now := time.Now()

	// History.
	b.WriteString(m.rule("History"))
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %-5s %-16s %-9s %s\n", "REV", "STATUS", "WHEN", "DESCRIPTION")
	for i, r := range msg.detail.History {
		if i >= 8 {
			b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  … %d older revisions", len(msg.detail.History)-i)))
			b.WriteString("\n")
			break
		}
		line := fmt.Sprintf("  %-5d %-16s %-9s %s", r.Revision, r.Status, kube.Age(r.Updated, now), truncate(r.Description, 66))
		switch {
		case r.Status == "failed":
			b.WriteString(m.theme.Error.Render(line))
		case strings.HasPrefix(r.Status, "pending"):
			b.WriteString(m.theme.Warning.Render(line))
		case r.Status == "deployed":
			b.WriteString(m.theme.Ok.Render(line))
		default:
			b.WriteString(m.theme.Faint.Render(line))
		}
		b.WriteString("\n")
	}

	// Resources deployed by the chart, with live status (drift detection).
	b.WriteString("\n")
	b.WriteString(m.rule(fmt.Sprintf("Resources (%d, live state)", len(msg.detail.Resources))))
	b.WriteString("\n")
	for i, r := range msg.detail.Resources {
		label := fmt.Sprintf("  %-60s", truncate(r.Kind+"/"+r.Name, 60))
		switch {
		case i >= len(msg.live) || !msg.live[i].known:
			b.WriteString(m.theme.Faint.Render(label + " —"))
		case !msg.live[i].found:
			b.WriteString(m.theme.Error.Render(label + " ✗ MISSING in cluster"))
		default:
			st := msg.live[i].status
			b.WriteString(label + " " + m.theme.Status(st))
			if st.Reason != "" {
				b.WriteString(m.theme.Faint.Render(" (" + st.Reason + ")"))
			}
		}
		b.WriteString("\n")
	}

	// User-supplied values.
	b.WriteString("\n")
	b.WriteString(m.rule("Values (user-supplied)"))
	b.WriteString("\n")
	if msg.detail.Values == "" {
		b.WriteString(m.theme.Faint.Render("  (none — chart defaults)"))
		b.WriteString("\n")
	} else {
		for _, line := range strings.Split(strings.TrimRight(msg.detail.Values, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}

	m.helmHist.SetContent(b.String())
	m.helmHist.GotoTop()
}

func (m *Model) openTopology() (tea.Model, tea.Cmd) {
	m.screen = screenTopology
	m.topo.SetContent("loading topology…")
	m.layout()
	return m, m.fetchTopology()
}

func (m *Model) renderTopology(nodes []model.TopologyNode) {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Topology — %d nodes, pods in %s", countRealNodes(nodes), scope)) + "\n\n")
	for _, n := range nodes {
		// Node header (colored by node health).
		header := fmt.Sprintf("%s %s  (%d pods)", n.Status.Symbol(), n.Name, len(n.Pods))
		b.WriteString(m.nodeStyle(n.Status).Render(header))
		b.WriteString("\n")

		// Capacity: colored gauges + reserved/allocatable + free.
		if n.AllocCPU > 0 {
			f := n.ReqCPU / n.AllocCPU
			fmt.Fprintf(&b, "    CPU %s %s / %s  free %s\n", m.coloredGauge(f, 12),
				components.FormatCPU(n.ReqCPU), components.FormatCPU(n.AllocCPU), components.FormatCPU(n.AllocCPU-n.ReqCPU))
		}
		if n.AllocMem > 0 {
			f := n.ReqMem / n.AllocMem
			fmt.Fprintf(&b, "    MEM %s %s / %s  free %s\n", m.coloredGauge(f, 12),
				components.FormatMemory(n.ReqMem), components.FormatMemory(n.AllocMem), components.FormatMemory(n.AllocMem-n.ReqMem))
		}

		// Pod table with aligned columns; % in dedicated columns.
		b.WriteString(m.theme.Faint.Render(fmt.Sprintf("      %-40s %9s %5s %10s %5s", "POD", "CPU", "CPU%", "MEM", "MEM%")))
		b.WriteString("\n")
		for _, p := range n.Pods {
			name := truncate(p.Namespace+"/"+p.Name, 40)
			cpuCell, memCell := "-", "-"
			cpuPct, memPct := "", ""
			if p.CPUReq > 0 {
				cpuCell = components.FormatCPU(p.CPUReq)
			}
			if p.MemReq > 0 {
				memCell = components.FormatMemory(p.MemReq)
			}
			if n.AllocCPU > 0 {
				cpuPct = m.pctCell(p.CPUReq / n.AllocCPU)
			}
			if n.AllocMem > 0 {
				memPct = m.pctCell(p.MemReq / n.AllocMem)
			}
			fmt.Fprintf(&b, "    %s %-40s %9s %5s %10s %5s\n",
				p.Status.Symbol(), name, cpuCell, cpuPct, memCell, memPct)
		}
		b.WriteString("\n")
	}
	m.topo.SetContent(b.String())
}

func (m Model) nodeStyle(l model.HealthLevel) lipgloss.Style {
	switch l {
	case model.HealthError:
		return m.theme.Error
	case model.HealthWarning:
		return m.theme.Warning
	default:
		return m.theme.Ok
	}
}

// coloredGauge renders a gauge colored by utilization: green <70%, yellow
// <90%, red otherwise — a quick visual of how full the node is.
func (m Model) coloredGauge(frac float64, width int) string {
	g := components.Gauge(frac, width)
	switch {
	case frac >= 0.9:
		return m.theme.Error.Render(g)
	case frac >= 0.7:
		return m.theme.Warning.Render(g)
	default:
		return m.theme.Ok.Render(g)
	}
}

// pctCell renders a right-aligned, threshold-colored percentage (fixed width;
// the ANSI color is zero-width so columns stay aligned in the terminal).
func (m Model) pctCell(frac float64) string {
	s := fmt.Sprintf("%3d%%", int(frac*100+0.5))
	switch {
	case frac >= 0.9:
		return m.theme.Error.Render(s)
	case frac >= 0.7:
		return m.theme.Warning.Render(s)
	default:
		return m.theme.Ok.Render(s)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func countRealNodes(nodes []model.TopologyNode) int {
	n := 0
	for _, nd := range nodes {
		if nd.Reason != "unscheduled" {
			n++
		}
	}
	return n
}

func (m *Model) openDiag() (tea.Model, tea.Cmd) {
	m.screen = screenDiag
	m.diag.SetContent("scanning for failing workloads…")
	m.layout()
	return m, m.fetchDiag()
}

func (m *Model) renderDiag(rows []model.Diagnostic) {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	if len(m.marked) > 0 {
		scope = fmt.Sprintf("%d marked", len(m.marked))
	}
	var b strings.Builder
	b.WriteString(m.rule("Workload failure diagnostics — "+scope) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.theme.Ok.Render("✓ no failing workloads detected"))
		m.diag.SetContent(b.String())
		return
	}
	for _, d := range rows {
		badge := d.Level.Symbol()
		who := d.Namespace + "/" + d.Pod
		if d.Container != "" {
			who += " [" + d.Container + "]"
		}
		line := fmt.Sprintf("%s %-45s %s", badge, who, d.Reason)
		switch d.Level {
		case model.HealthError:
			b.WriteString(m.theme.Error.Render(line))
		case model.HealthWarning:
			b.WriteString(m.theme.Warning.Render(line))
		default:
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	m.diag.SetContent(b.String())
}

func (m *Model) renderTop(rows []model.TopConsumer) {
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Top consumers by %s — 'u' switches cpu/mem", m.topKind)) + "\n\n")
	if len(rows) == 0 {
		b.WriteString("— no data / metrics unavailable")
		m.usage.SetContent(b.String())
		return
	}
	max := rows[0].Value
	for _, r := range rows {
		if r.Value > max {
			max = r.Value
		}
	}
	for _, r := range rows {
		frac := 0.0
		if max > 0 {
			frac = r.Value / max
		}
		label := r.Namespace + "/" + r.Name
		b.WriteString(fmt.Sprintf("%s %10s  %s\n",
			components.Gauge(frac, 15), components.FormatValue(r.Kind, r.Value), label))
	}
	m.usage.SetContent(b.String())
}

func (m Model) openLogs() (tea.Model, tea.Cmd) {
	obj, ok := m.selectedObject()
	if !ok {
		return m, nil
	}
	if m.logCancel != nil {
		m.logCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())

	switch {
	case strings.EqualFold(m.curType.Kind, "Pod"):
		m.logCh = m.client.StreamPodLogs(ctx, obj.Namespace, obj.Name, "", 200, true)
		m.statusMsg = "logs: " + obj.Name
	default:
		// Workloads: merge the logs of every pod the selector owns (FR-034).
		sel, hasSel := kube.PodSelector(obj.Raw)
		if !hasSel {
			cancel()
			m.statusMsg = "logs are available on pods and workloads with a selector"
			return m, nil
		}
		m.logCh = m.client.StreamWorkloadLogs(ctx, obj.Namespace, sel, 100, true)
		m.statusMsg = "merged logs: " + m.curType.Kind + "/" + obj.Name + " (one prefix per pod)"
	}

	m.logCancel = cancel
	m.logBuf = nil
	m.logPaused = false
	m.logsView.SetContent("waiting for logs…")
	m.screen = screenLogs
	m.layout()
	return m, m.nextLogLine()
}

func (m Model) nextLogLine() tea.Cmd {
	ch := m.logCh
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return logLineMsg{Done: true}
		}
		return logLineMsg(line)
	}
}

func (m Model) openPicker(kind pickerKind) (tea.Model, tea.Cmd) {
	m.pickerKind = kind
	m.pickerReturn = m.screen // return here when the picker closes
	var opts []string
	switch kind {
	case pickType:
		for _, t := range m.types {
			opts = append(opts, typeOptionLabel(t))
		}
	case pickContext:
		ctxs, err := kube.Contexts(m.kubeconfigPath, m.client.Namespace)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		for _, c := range ctxs {
			opts = append(opts, c.Name)
		}
	case pickNamespace:
		// List the cluster's namespaces read-only.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		nss, err := m.client.Namespaces(ctx)
		cancel()
		if err != nil {
			m.errMsg = "listing namespaces: " + err.Error()
			nss = m.namespaceOptions() // best-effort fallback
		}
		opts = nss
	}
	sort.Strings(opts)
	switch kind {
	case pickNamespace:
		// Offer an "all namespaces" choice at the top (cross-namespace view).
		opts = append([]string{allNamespacesLabel}, opts...)
	case pickType:
		// Helm releases are reachable from ':' too (type "helm"), not only 'H'.
		opts = append([]string{helmReleasesLabel}, opts...)
	}
	m.pickerOpts = opts
	m.pickerQuery = ""
	m.picker.SetColumns([]table.Column{{Title: "select (type to filter)", Width: max(20, m.width-4)}})
	m.applyPickerRows()
	m.screen = screenPicker
	m.layout()
	return m, nil
}

func (m Model) pickerLabel() string {
	switch m.pickerKind {
	case pickType:
		return "resource type"
	case pickNamespace:
		return "namespace"
	case pickContext:
		return "context"
	case pickEventKind:
		return "kind"
	default:
		return "select"
	}
}

// applyPickerRows rebuilds the picker rows from pickerOpts filtered by the
// current type-to-filter query (case-insensitive substring match).
func (m *Model) applyPickerRows() {
	q := strings.ToLower(strings.TrimSpace(m.pickerQuery))
	rows := make([]table.Row, 0, len(m.pickerOpts))
	for _, o := range m.pickerOpts {
		if q == "" || strings.Contains(strings.ToLower(o), q) {
			rows = append(rows, table.Row{o})
		}
	}
	m.pickerWin.SetRows(rows)
	m.pickerWin.Sync(&m.picker)
}

func (m Model) namespaceOptions() []string {
	// Best-effort: derive namespaces from currently listed objects plus common ones.
	set := map[string]bool{}
	for _, o := range m.objects {
		if o.Namespace != "" {
			set[o.Namespace] = true
		}
	}
	set["default"] = true
	set["kube-system"] = true
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	return out
}

func (m Model) pickerSelect() (tea.Model, tea.Cmd) {
	row, _ := m.pickerWin.Selected()
	if len(row) == 0 {
		return m.goBack()
	}
	choice := row[0]
	switch m.pickerKind {
	case pickType:
		if choice == helmReleasesLabel {
			m.screen = screenList // leave the picker even if helm is unavailable
			return m.openHelm()
		}
		choice = strings.TrimSpace(strings.SplitN(choice, "  (", 2)[0])
		for _, t := range m.types {
			if t.Key() == choice {
				m.curType = t
			}
		}
		// A type switch is a fresh view: leave any drill, clear the text
		// filter (an invisible leftover filter would empty the new list) and
		// the marked selection.
		m.drillSelector, m.drillFor, m.drillNamespace = "", "", ""
		m.filter.SetValue("")
		m.marked = map[string]model.ResourceObject{}
		m.statusMsg = ""
		m.screen = screenList
		m.layout()
		m.persist()
		return m, m.listObjects()
	case pickNamespace:
		if choice == allNamespacesLabel {
			m.client.Namespace = "" // empty → list across all namespaces
		} else {
			m.client.Namespace = choice
		}
		m.layout()
		m.persist()
		if m.pickerReturn == screenEvents {
			// Namespace changed from the timeline: stay there and reload it.
			// A drill scope no longer applies to the new namespace.
			m.eventsScope = nil
			m.eventsScopeFor = ""
			m.drillSelector, m.drillFor, m.drillNamespace = "", "", ""
			m.screen = screenEvents
			m.events.SetContent("loading events…")
			return m, m.fetchEvents()
		}
		// Changing namespace from the list leaves any drill (its workload
		// belonged to the previous scope) and the marked selection.
		m.drillSelector, m.drillFor, m.drillNamespace = "", "", ""
		m.marked = map[string]model.ResourceObject{}
		m.statusMsg = ""
		m.screen = screenList
		return m, m.listObjects()
	case pickEventKind:
		if choice == allKindsLabel {
			m.eventsKind = ""
		} else {
			m.eventsKind = choice
		}
		m.screen = screenEvents
		m.layout()
		m.renderEvents()
		return m, nil
	case pickContext:
		nc, err := kube.NewClient(kube.Options{KubeconfigPath: m.kubeconfigPath, Context: choice})
		if err != nil {
			m.errMsg = err.Error()
			return m.goBack()
		}
		m.client = nc
		m.helm = helm.New(m.kubeconfigPath, choice) // helm reads the new cluster too
		m.marked = map[string]model.ResourceObject{}
		m.screen = screenList
		m.layout()
		m.persist()
		cmds := []tea.Cmd{m.loadTypes()}
		// Re-link metrics to the new cluster's Prometheus (unless an explicit URL).
		if m.cfg.PrometheusURL == "" {
			m.metrics = &metrics.Client{} // disable until re-discovered
			cmds = append(cmds, m.discoverMetrics())
		}
		return m, tea.Batch(cmds...)
	}
	return m.goBack()
}

// ---- Rendering ----

func (m *Model) layout() {
	headerH := 3 // header + rule
	footerH := 4 // rule + status line + shortcuts line
	if m.help.ShowAll {
		footerH = 8
	}
	bodyH := m.height - headerH - footerH
	if bodyH < 3 {
		bodyH = 3
	}
	m.bodyH = bodyH
	m.table.SetHeight(bodyH)
	m.table.SetWidth(m.width)
	m.win.SetHeight(bodyH - 1) // -1: the table's own column header
	m.pickerWin.SetHeight(m.modalListRows())
	m.pickerWin.Sync(&m.picker)
	m.helmWin.SetHeight(bodyH - 1)
	m.helmWin.Sync(&m.helmTable)
	m.detail.Width, m.detail.Height = m.width, bodyH
	m.logsView.Width, m.logsView.Height = m.width, bodyH
	m.usage.Width, m.usage.Height = m.width, bodyH
	m.diag.Width, m.diag.Height = m.width, bodyH
	m.topo.Width, m.topo.Height = m.width, bodyH
	m.events.Width, m.events.Height = m.width, bodyH
	m.helmHist.Width, m.helmHist.Height = m.width, bodyH
	m.helmTable.SetHeight(bodyH)
	m.helmTable.SetWidth(m.width)
	m.picker.SetHeight(bodyH)
	m.picker.SetWidth(m.width)
}

// rule renders a full-width thin separator; with a title it becomes a section
// header: "── Title ───────" (title in the table-header color, dashes faint).
func (m Model) rule(title string) string {
	w := m.width
	if w < 10 {
		w = 80
	}
	if title == "" {
		return m.theme.Faint.Render(strings.Repeat("─", w))
	}
	used := 3 + len([]rune(title)) + 1 // "── " + title + " "
	rest := w - used
	if rest < 0 {
		rest = 0
	}
	return m.theme.Faint.Render("── ") + m.theme.TableHeader.Render(title) +
		m.theme.Faint.Render(" "+strings.Repeat("─", rest))
}

// bodyView renders the body of a given screen (used both for the active
// screen and as the background behind modal overlays).
func (m Model) bodyView(sc screen) string {
	switch sc {
	case screenDetail:
		return m.detail.View()
	case screenLogs:
		return m.logsView.View()
	case screenTop:
		return m.usage.View()
	case screenDiag:
		return m.diag.View()
	case screenTopology:
		return m.topo.View()
	case screenEvents:
		return m.events.View()
	case screenHelm:
		return m.helmTable.View()
	case screenHelmHist:
		return m.helmHist.View()
	default:
		return m.listView()
	}
}

func (m Model) View() string {
	// Pickers render as a centered modal OVER the screen they were opened
	// from; filters render as a centered input box over the live view.
	bodySc := m.screen
	if m.screen == screenPicker {
		bodySc = m.pickerReturn
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.rule(""))
	b.WriteString("\n")
	b.WriteString(m.bodyView(bodySc))
	b.WriteString("\n")
	b.WriteString(m.rule(""))
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	b.WriteString(m.footer())
	out := b.String()

	switch {
	case m.screen == screenPicker:
		box, _ := m.pickerModal()
		out = overlayCenter(out, box, m.width)
	case m.filtering:
		out = overlayCenter(out, m.inputModal("filter "+m.curType.Resource, m.filter.Value(), "Enter save · Esc close"), m.width)
	case m.screen == screenEvents && m.eventsFiltering:
		out = overlayCenter(out, m.inputModal("filter events", m.eventsQuery, "Enter save · Esc cancel"), m.width)
	}
	return out
}

// overlayCenter splices a box over the center of a rendered frame,
// ANSI-safely (the background stays visible around the modal).
func overlayCenter(bg, box string, totalW int) string {
	bgLines := strings.Split(bg, "\n")
	boxLines := strings.Split(box, "\n")
	boxW := 0
	for _, l := range boxLines {
		if w := lipgloss.Width(l); w > boxW {
			boxW = w
		}
	}
	x := (totalW - boxW) / 2
	if x < 0 {
		x = 0
	}
	y := (len(bgLines) - len(boxLines)) / 2
	if y < 1 {
		y = 1
	}
	for i, bl := range boxLines {
		r := y + i
		if r >= len(bgLines) {
			break
		}
		line := bgLines[r]
		left := xansi.Truncate(line, x, "")
		if pad := x - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := xansi.TruncateLeft(line, x+boxW, "")
		bgLines[r] = left + bl + right
	}
	return strings.Join(bgLines, "\n")
}

// modalW/modalListRows bound the picker modal size.
func (m Model) modalW() int {
	w := m.width - 10
	if w > 64 {
		w = 64
	}
	if w < 30 {
		w = 30
	}
	return w
}

func (m Model) modalListRows() int {
	r := m.bodyH - 6
	if r > 10 {
		r = 10
	}
	if r < 3 {
		r = 3
	}
	return r
}

var modalBorder = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("62")).
	Padding(0, 1)

// pickerModalGeom mirrors pickerModal's layout for mouse hit-testing:
// x/y of the box and the absolute row of the first option line.
type modalGeom struct {
	x, y, w, h int // outer box incl. border
	optTop     int // absolute y of the first option row
	optRows    int // option rows displayed
}

// pickerModal renders the centered picker box and returns its geometry.
func (m Model) pickerModal() (string, modalGeom) {
	w := m.modalW()
	inner := w - 4 // border + padding
	shown := m.pickerWin.height
	if l := m.pickerWin.Len() - m.pickerWin.win; l < shown {
		shown = l
	}
	if shown < 0 {
		shown = 0
	}
	var lines []string
	lines = append(lines, m.theme.TableHeader.Render(padTo(m.pickerLabel(), inner)))
	lines = append(lines, padTo("▸ "+m.pickerQuery+"▏", inner))
	for i := m.pickerWin.win; i < m.pickerWin.win+shown; i++ {
		txt := padTo(" "+truncate(m.pickerWin.rows[i][0], inner-1), inner)
		if i == m.pickerWin.cursor {
			txt = m.theme.TableSelected.Render(txt)
		}
		lines = append(lines, txt)
	}
	_, _, total := m.pickerWin.Range()
	hint := fmt.Sprintf("↑↓ · Enter ok · Esc close   %d option(s)", total)
	lines = append(lines, m.theme.Faint.Render(padTo(hint, inner)))
	box := modalBorder.Render(strings.Join(lines, "\n"))

	boxLines := len(lines) + 2
	bgLines := m.bodyH + 5 // header+rule+body+rule+footer
	y := (bgLines - boxLines) / 2
	if y < 1 {
		y = 1
	}
	geom := modalGeom{
		x: (m.width - w) / 2, y: y, w: w, h: boxLines,
		optTop:  y + 3, // border + title + query
		optRows: shown,
	}
	return box, geom
}

// inputModal renders a centered single-input box (filters).
func (m Model) inputModal(title, value, hint string) string {
	w := m.modalW()
	inner := w - 4
	lines := []string{
		m.theme.TableHeader.Render(padTo(title, inner)),
		padTo("/ "+value+"▏", inner),
		m.theme.Faint.Render(padTo(hint, inner)),
	}
	return modalBorder.Render(strings.Join(lines, "\n"))
}

// padTo pads/truncates a plain string to an exact display width.
func padTo(s string, w int) string {
	s = truncate(s, w)
	if pad := w - len([]rune(s)); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// headerZones recomputes the clickable chip ranges of the header line.
func (m Model) headerZones() []clickZone {
	_, zones := m.buildHeaderLine()
	return zones
}

func (m Model) header() string {
	line, _ := m.buildHeaderLine()
	return line
}

func (m Model) buildHeaderLine() (string, []clickZone) {
	ctx := "-"
	ns := "-"
	if m.client != nil {
		ns = m.client.Namespace
		if ns == "" {
			ns = "<all>"
		}
		if c := m.client.ActiveContext(); c != "" {
			ctx = c
		}
	}
	typeLabel := m.curType.Key()
	if m.drillSelector != "" {
		typeLabel = "pods ⊂ " + m.drillFor
	}
	// Identity chips: app badge + read-only + colored ctx/ns/type values —
	// friendlier and easier to parse at a glance than a flat line (FR-036).
	// Each chip is clickable (zones tracked by width): ctx→'c' ns→'n' type→':'.
	sep := m.theme.Faint.Render(" │ ")
	var line string
	var zones []clickZone
	add := func(seg string, key string) {
		x0 := lipgloss.Width(line)
		line += seg
		if key != "" {
			zones = append(zones, clickZone{x0: x0, x1: lipgloss.Width(line), key: key})
		}
	}
	add(m.theme.AppBadge.Render("⎈ idz-k8s"), "")
	add(" "+m.theme.Faint.Render("ctx ")+m.theme.CtxVal.Render(ctx), "c")
	add(sep, "")
	add(m.theme.Faint.Render("ns ")+m.theme.NsVal.Render(ns), "n")
	add(sep, "")
	add(m.theme.Faint.Render("type ")+m.theme.TypeVal.Render(typeLabel), ":")
	switch {
	case strings.TrimSpace(m.filter.Value()) != "" || m.filtering:
		// A committed filter stays visible so it can never silently empty a
		// list (press '/' to edit, then clear it).
		line += "  " + m.theme.Warning.Render("filter:"+m.filter.Value()) + m.theme.Faint.Render(" (/ edit)")
	}
	// NEVER let the header wrap: a wrapped header shifts every line below and
	// breaks the click→row mapping. Truncate, then center for emphasis.
	if m.width > 0 {
		line = xansi.Truncate(line, m.width, "…")
		if pad := (m.width - lipgloss.Width(line)) / 2; pad > 0 {
			line = strings.Repeat(" ", pad) + line
			for i := range zones {
				zones[i].x0 += pad
				zones[i].x1 += pad
			}
		}
	}
	return line, zones
}

// statusLine renders the info line (position / status messages / errors) —
// on its OWN line so it never hides the shortcuts (user feedback).
func (m Model) statusLine() string {
	var line string
	switch {
	case m.errMsg != "":
		line = m.theme.Error.Render("⚠ " + m.errMsg)
	case m.statusMsg != "":
		line = m.theme.StatusBar.Render(m.statusMsg)
	default:
		if from, to, total := m.win.Range(); total > 0 && m.screen == screenList {
			line = m.theme.Position.Render(fmt.Sprintf("%d-%d/%d", from, to, total))
		} else {
			line = m.theme.Position.Render(fmt.Sprintf("%d items", len(m.objects)))
		}
	}
	if m.width > 0 {
		line = xansi.Truncate(line, m.width, "…")
	}
	return line
}

func (m Model) footer() string {
	km := m.screenKeymap()
	if m.help.ShowAll {
		return m.help.View(km)
	}
	line, _ := m.footerShort("", km)
	if m.width > 0 {
		// Never wrap: a wrapped footer breaks nothing geometrically (it is the
		// last line) but looks broken; truncate cleanly.
		line = xansi.Truncate(line, m.width, "…")
	}
	return line
}

// footerZones recomputes the clickable label ranges of the shortcut bar.
func (m Model) footerZones() []clickZone {
	_, zones := m.footerShort("", m.screenKeymap())
	return zones
}

// clickZone maps an x range of a rendered line to the key it triggers when
// clicked (T048: on-screen controls are clickable, not just rows).
type clickZone struct {
	x0, x1 int
	key    string
}

// footerShort renders the shortcut bar with per-label click zones. Clicking a
// label is exactly like pressing its key.
func (m Model) footerShort(prefix string, km keymapView) (string, []clickZone) {
	line := prefix
	var zones []clickZone
	first := true
	for _, b := range km.ShortHelp() {
		h := b.Help()
		if h.Key == "" {
			continue
		}
		if !first {
			line += m.theme.Faint.Render(" • ")
		}
		first = false
		x0 := lipgloss.Width(line)
		line += m.theme.Position.Render(h.Key) + " " + m.theme.Help.Render(h.Desc)
		keys := b.Keys()
		if len(keys) > 0 {
			zones = append(zones, clickZone{x0: x0, x1: lipgloss.Width(line), key: keys[0]})
		}
	}
	return line, zones
}

// keyMsgFor synthesizes the KeyMsg equivalent to pressing a binding's key.
func keyMsgFor(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case " ", "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// keymapView adapts a per-screen set of bindings to the bubbles help.KeyMap
// interface so the footer and help overlay show only the shortcuts active in
// the current view (FR-010).
type keymapView struct {
	short []key.Binding
	full  [][]key.Binding
}

func (k keymapView) ShortHelp() []key.Binding  { return k.short }
func (k keymapView) FullHelp() [][]key.Binding { return k.full }

// screenKeymap returns the bindings relevant to the current screen. Sub-views
// (detail, logs, topology, failures, top, picker) only expose scroll/back/quit
// (plus their own action) — the list-only shortcuts (context, namespace,
// topology, failures…) are hidden there since they are not active.
func (m Model) screenKeymap() keymapView {
	k := m.keys
	// Mouse toggle rides along the nav group so it shows in every help overlay
	// (copy/paste is needed from any view).
	nav := []key.Binding{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End, k.Mouse}
	switch m.screen {
	case screenList:
		return keymapView{
			short: []key.Binding{k.Open, k.Mark, k.Yaml, k.Describe, k.Filter, k.Sort, k.Logs, k.Top, k.Diag, k.Topology, k.Events, k.Namespace, k.Context, k.Help, k.Quit},
			full: [][]key.Binding{
				nav,
				{k.Open, k.Mark, k.Yaml, k.Describe, k.Filter, k.Jump, k.Logs},
				{k.Sort, k.SortDir, k.Top, k.Diag, k.Topology, k.Events},
				{k.Namespace, k.Context, k.Help, k.Quit},
			},
		}
	case screenHelm:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Values, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Values, k.Mouse, k.Back, k.Help, k.Quit}},
		}
	case screenEvents:
		return keymapView{
			short: []key.Binding{k.Filter, k.Kind, k.WarnOnly, k.Namespace, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Filter, k.Kind, k.WarnOnly, k.Namespace}, {k.Back, k.Help, k.Quit}},
		}
	case screenLogs:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Pause, k.End, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Pause, k.Back, k.Help, k.Quit}},
		}
	case screenPicker:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Back, k.Quit}},
		}
	case screenTop:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Top, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Top, k.Back, k.Help, k.Quit}},
		}
	default: // detail, logs, diag, topology
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Back, k.Help, k.Quit},
			full:  [][]key.Binding{nav, {k.Back, k.Help, k.Quit}},
		}
	}
}

// ---- Helpers ----

// columnAt maps an x position to a visual column index (0 = mark column).
func (m Model) columnAt(x int) (int, bool) {
	widths := m.listWidths(m.columnsForType())
	pos := 0
	for i, w := range widths {
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w + 1 // column gap
	}
	return 0, false
}

func (m Model) now() time.Time { return time.Now() }

func readyFrac(ready, desired int) float64 {
	if desired <= 0 {
		return 2 // no ready notion sorts last in asc
	}
	return float64(ready) / float64(desired)
}

func (m Model) selectedObject() (model.ResourceObject, bool) {
	row, _ := m.win.Selected()
	if len(row) < 2 {
		return model.ResourceObject{}, false
	}
	var ns, name string
	if m.curType.Namespaced {
		if len(row) < 3 {
			return model.ResourceObject{}, false
		}
		ns, name = row[1], row[2]
	} else {
		name = row[1]
	}
	for _, o := range m.objects {
		if o.Namespace == ns && o.Name == name {
			return o, true
		}
	}
	return model.ResourceObject{}, false
}

// persist saves the current context/namespace/type as defaults for next launch.
// Best-effort: failures are ignored (never block the UI).
func (m *Model) persist() {
	if m.configPath == "" || m.client == nil {
		return
	}
	m.cfg.LastContext = m.client.ActiveContext()
	m.cfg.LastNamespace = m.client.Namespace
	m.cfg.LastType = m.curType.Key()
	_ = config.Save(m.configPath, m.cfg)
}

// colorizeYAML tints YAML for readability: keys in blue, comments faint.
// Purely visual (line-by-line, no parsing) — content is preserved verbatim.
func (m Model) colorizeYAML(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		indent := l[:len(l)-len(trimmed)]
		switch {
		case strings.HasPrefix(trimmed, "#"):
			lines[i] = indent + m.theme.Faint.Render(trimmed)
		default:
			if c := strings.Index(trimmed, ":"); c > 0 {
				key := trimmed[:c]
				if !strings.ContainsAny(key, " \"'{}[]") {
					lines[i] = indent + m.theme.YamlKey.Render(key) + trimmed[c:]
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// typeOptionLabel renders a type-picker option with its kubectl-style
// aliases, e.g. "v1/services  (svc)" — typing "svc" then matches it.
func typeOptionLabel(t model.ResourceType) string {
	if len(t.ShortNames) == 0 {
		return t.Key()
	}
	return t.Key() + "  (" + strings.Join(t.ShortNames, ",") + ")"
}

// statusCell renders the STATUS column: the reason when we have one (more
// informative: "Running", "3 eps", "no eps"), the level label otherwise, and a
// neutral "—" for kinds that simply have no status (ConfigMap, ServiceAccount…)
// instead of a misleading "? Unknown".
func statusCell(s model.StatusSummary) string {
	if s.Level == model.HealthUnknown && s.Reason == "" {
		return "—"
	}
	label := s.Reason
	if label == "" {
		label = s.Level.Label()
	}
	return s.Level.Symbol() + " " + label
}

func findTypeByKey(types []model.ResourceType, key string) (model.ResourceType, bool) {
	if key == "" {
		return model.ResourceType{}, false
	}
	for _, t := range types {
		if t.Key() == key {
			return t, true
		}
	}
	return model.ResourceType{}, false
}

func defaultType(types []model.ResourceType) model.ResourceType {
	for _, t := range types {
		if t.Group == "" && t.Resource == "pods" {
			return t
		}
	}
	return types[0]
}

// cleanForDisplay returns a shallow clone of the object with noisy internal
// bookkeeping removed from the detail view: metadata.managedFields (Server-Side
// Apply field ownership, the "f:" tree) and the verbose last-applied annotation.
// This mirrors what kubectl describe / k9s hide by default.
func cleanForDisplay(raw map[string]interface{}) map[string]interface{} {
	clone := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		clone[k] = v
	}
	if meta, ok := clone["metadata"].(map[string]interface{}); ok {
		m2 := make(map[string]interface{}, len(meta))
		for k, v := range meta {
			if k == "managedFields" {
				continue
			}
			m2[k] = v
		}
		if ann, ok := m2["annotations"].(map[string]interface{}); ok {
			a2 := make(map[string]interface{}, len(ann))
			for k, v := range ann {
				if k == "kubectl.kubernetes.io/last-applied-configuration" {
					continue
				}
				a2[k] = v
			}
			m2["annotations"] = a2
		}
		clone["metadata"] = m2
	}
	return clone
}

func maskSecret(raw map[string]interface{}) map[string]interface{} {
	clone := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		clone[k] = v
	}
	if data, ok := clone["data"].(map[string]interface{}); ok {
		masked := make(map[string]interface{}, len(data))
		for k := range data {
			masked[k] = "••••••"
		}
		clone["data"] = masked
	}
	return clone
}

func hit(msg tea.KeyMsg, b key.Binding) bool { return key.Matches(msg, b) }

// podLogPalette holds visually distinct colors for merged-log pod prefixes.
var podLogPalette = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("39")),  // blue
	lipgloss.NewStyle().Foreground(lipgloss.Color("208")), // orange
	lipgloss.NewStyle().Foreground(lipgloss.Color("135")), // purple
	lipgloss.NewStyle().Foreground(lipgloss.Color("42")),  // green
	lipgloss.NewStyle().Foreground(lipgloss.Color("214")), // yellow
	lipgloss.NewStyle().Foreground(lipgloss.Color("45")),  // cyan
	lipgloss.NewStyle().Foreground(lipgloss.Color("197")), // pink
	lipgloss.NewStyle().Foreground(lipgloss.Color("190")), // lime
}

// podPrefixStyle picks a stable color for a pod name (same pod → same color
// for the whole session, no state needed).
func podPrefixStyle(pod string) lipgloss.Style {
	h := 0
	for _, r := range pod {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return podLogPalette[h%len(podLogPalette)]
}
