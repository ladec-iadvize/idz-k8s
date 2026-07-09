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
	screenSizing
	screenSizingList
	screenPosture
	screenConnectivity
	screenAccess
	screenDrift
)

type pickerKind int

const (
	pickType pickerKind = iota
	pickNamespace
	pickContext
	pickEventKind
	pickColumns
	pickView
)

// Sentinel options of the views picker (US8).
const (
	saveViewLabel  = "◆ save current view as…"
	resetViewLabel = "◆ reset current view to defaults"
)

// addFieldLabel is the column-chooser action row that adds a user-defined
// column: a label key ("app") or a dot-path field (".status.podIP").
const addFieldLabel = "◆ add custom field (label or .path)…"

// allKindsLabel is the sentinel option that clears the event kind filter.
const allKindsLabel = "◆ all kinds"

// helmReleasesLabel is the virtual entry in the ':' type picker that opens the
// Helm release view — so ":helm" works like any resource type.
const helmReleasesLabel = "◆ helm releases"

// logBufMax bounds the in-memory log buffer (keeps the tail).
const logBufMax = 5000

// allNamespacesLabel is the sentinel option that lists across all namespaces.
const allNamespacesLabel = "◆ all namespaces"

// nsPatternPrefix marks the synthetic namespace-picker option offered when
// the typed query is a glob (e.g. "staging-*"): selecting it scopes every
// view to the namespaces the pattern matches.
const nsPatternPrefix = "◆ pattern: "

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
	// Usage view ('u', pods or per-deployment aggregate): CPU and memory
	// side by side — one consistent table, no metric toggle.
	usageRows    []model.UsageRow
	usageAllRows []model.UsageRow
	usageWin     winTable
	usageSortCol int
	usageSortAsc bool
	usageIsAgg   bool // true when rows aggregate a workload's pods
	usageFilterQ string
	usageTyping  bool

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

	// Drill-down: viewing the pods owned by a workload (US9 ownership) or
	// running on a node (Enter on the nodes list).
	drillSelector  string             // label selector; "" = not drilling
	drillNode      string             // node name; "" = not drilling by node
	drillFor       string             // e.g. "Deployment/back", "Node/ip-10-0-1-2"
	drillNamespace string             // workload namespace (query scope only — the user's namespace filter is untouched)
	drillPrevType  model.ResourceType // type to restore on Esc

	recentWin int // first visible row of the events detail window

	// Helm release overview (US12, read-only).
	helmFiltering  bool   // typing a helm filter (Enter commits, Esc cancels)
	helmQuery      string // committed helm filter (name/namespace/chart)
	helmTable      table.Model
	helmHist       viewport.Model
	helmRows       []model.HelmRelease
	helmValuesOnly bool // 'v': show only the values of a release

	// Posture overview (US13, advisory & read-only).
	posture viewport.Model

	// Per-pod connectivity / NetworkPolicy view (US14, read-only).
	connectivity viewport.Model

	// Access (RBAC) view (US15, read-only introspection).
	access viewport.Model

	// Live vs last-applied drift view (US16, read-only).
	drift viewport.Model

	// Sizing recommendations (US6, advisory & read-only): overview table of
	// every listed workload, Enter → per-workload detail panel.
	sizingVP   viewport.Model
	sizingFor  string // e.g. "Deployment/back"
	sizingRows []model.SizingAdvice
	sizingObjs []model.ResourceObject // same order as sizingRows
	sizingWin  winTable
	sizingFrom screen // where Esc returns from the detail panel
	// Overview sort: -1 = severity (worst first, the default).
	sizingSortCol int
	sizingSortAsc bool

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

	// Column sorting of the main list; persisted per type via ViewPrefs (US8).
	sortCol int // visual column index into columnsForType(); -1 = none
	sortAsc bool

	// Objects behind the visible rows, same order (selection is index-based,
	// so it survives any column arrangement).
	rowObjs []model.ResourceObject

	// Column chooser state ('C', US8): the current type's available columns
	// with their visibility, in display order.
	colItems []colItem

	// Save-view naming prompt ('V' → save as…, US8).
	viewNaming bool
	viewName   string

	// Custom-column prompt (chooser → add custom field…).
	fieldNaming bool
	fieldInput  string

	// Vim-like '/' search inside EVERY content viewport (describe/YAML,
	// helm detail, logs, failures, topology, posture, connectivity, access,
	// drift, sizing, top): matches highlighted, 'n'/'N' navigate, Esc
	// clears first, then goes back. One consistent behavior across views
	// (owner request 2026-07-07).
	searchTyping bool
	searchInput  string
	searchQuery  string
	searchScreen screen // the screen the committed query belongs to
	searchHits   []int  // matching line numbers in the raw content
	searchIdx    int
	vpRaw        map[screen]string // unhighlighted content per viewport screen

	// Live refresh (watch-driven): a change signal re-renders the list at
	// most every changeFlushDelay (bursts from a rolling update coalesce).
	changePending bool

	// Sizing overview row filter ('/' on the table, helm-list style).
	sizingFiltering bool
	sizingQuery     string
	sizingAllRows   []model.SizingAdvice
	sizingAllObjs   []model.ResourceObject
}

// colItem is one entry of the column chooser.
type colItem struct {
	title string
	on    bool
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
		diag:           viewport.New(0, 0),
		topo:           viewport.New(0, 0),
		events:         viewport.New(0, 0),
		filter:         fi,
		picker:         table.New(table.WithFocused(true)),
		helmTable:      table.New(table.WithFocused(true)),
		helmHist:       viewport.New(0, 0),
		sizingVP:       viewport.New(0, 0),
		posture:        viewport.New(0, 0),
		connectivity:   viewport.New(0, 0),
		access:         viewport.New(0, 0),
		drift:          viewport.New(0, 0),
		screen:         screenList,
		marked:         map[string]model.ResourceObject{},
		sortCol:        -1,
		sizingSortCol:  -1,
		usageSortCol:   -1,
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
	stale    error // set when data comes from a cache whose watch is failing
}
type logLineMsg kube.LogLine
type tickMsg struct{}

// changeMsg: the watch reported a change (ok=false → the client was
// replaced and the loop must re-arm on the new one).
type changeMsg struct{ ok bool }

// changeFlushMsg fires after the throttle window to apply coalesced changes.
type changeFlushMsg struct{}

// changeFlushDelay throttles watch-driven re-renders (a rolling update
// emits dozens of events per second; the eye needs ~4 fps, not 50).
const changeFlushDelay = 250 * time.Millisecond

type errMsg struct{ err error }
type usageTableMsg struct {
	rows  []model.UsageRow
	isAgg bool
	err   error
}
type usageMsg struct {
	ns, name string
	cpu, mem model.Usage
}
type metricsMsg struct {
	client *metrics.Client
	note   string
}
type sizingMsg struct {
	advice model.SizingAdvice
	err    error
}
type accessMsg struct {
	report model.AccessReport
	err    error
}
type connectivityMsg struct {
	report model.ConnectivityReport
	err    error
}
type postureMsg struct {
	rows []model.PostureFinding
	err  error
}
type sizingListMsg struct {
	rows []model.SizingAdvice
	objs []model.ResourceObject
	err  error
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
	cmds := []tea.Cmd{m.loadTypes(), m.waitForChange()}
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

// waitForChange blocks on the client's coalesced watch signal and turns it
// into a message (same pattern as the log stream). One waiter at a time.
func (m Model) waitForChange() tea.Cmd {
	if m.client == nil {
		return nil
	}
	ch := m.client.Changes()
	return func() tea.Msg {
		_, ok := <-ch
		return changeMsg{ok: ok}
	}
}

func (m Model) listObjects() tea.Cmd {
	c, t, ns, sel, node := m.client, m.curType, m.client.Namespace, m.drillSelector, m.drillNode
	if sel != "" && m.drillNamespace != "" {
		// Drilling: query in the workload's namespace so its selector cannot
		// match same-labelled pods elsewhere. The user's ns filter is untouched.
		ns = m.drillNamespace
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if node != "" {
			// Node drill: every pod scheduled on the node, all namespaces.
			objs, err := c.PodsOnNode(ctx, node)
			return objectsMsg{objects: objs, err: err}
		}
		objs, err := c.ListSelected(ctx, t, ns, sel)
		stale := c.CacheStale()
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
		return objectsMsg{objects: objs, nodePods: nodePods, err: err, stale: stale}
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
			m.applyViewPref()
		}
		return m, tea.Batch(m.listObjects(), m.tick())

	case objectsMsg:
		if msg.err != nil {
			if kube.IsForbidden(msg.err) {
				// RBAC denial: an access problem, not a lost connection —
				// name the type and point to the access view (FR-032).
				m.errMsg = "forbidden: your credentials cannot list " + m.curType.Key() + " — 'a' shows your access"
				return m, nil
			}
			// Keep the last data on screen; the tick keeps retrying (FR-016).
			m.disconnected = true
			m.errMsg = "cluster unreachable — retrying every " +
				fmt.Sprintf("%ds", m.cfg.RefreshIntervalSeconds) + " (" + truncate(msg.err.Error(), 60) + ")"
		} else {
			if msg.stale != nil {
				// Cached data is still shown, but freshness is never faked:
				// the watch is failing, announce it (FR-016/FR-021 spirit).
				m.disconnected = true
				m.errMsg = "cluster unreachable — showing cached data, watch retrying (" + truncate(msg.stale.Error(), 60) + ")"
			} else if m.disconnected {
				m.disconnected = false
				m.statusMsg = "✓ reconnected"
				m.errMsg = ""
			} else {
				m.errMsg = ""
			}
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

	case changeMsg:
		// Re-arm first (on the CURRENT client — after a context switch the
		// old channel closes and this rebinds to the new one), then schedule
		// one throttled flush for the whole burst.
		cmds := []tea.Cmd{m.waitForChange()}
		if msg.ok && !m.changePending {
			m.changePending = true
			cmds = append(cmds, tea.Tick(changeFlushDelay, func(time.Time) tea.Msg { return changeFlushMsg{} }))
		}
		return m, tea.Batch(cmds...)

	case changeFlushMsg:
		m.changePending = false
		if m.screen == screenList {
			// Reads the informer cache — no API round-trip.
			return m, m.listObjects()
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
			m.setContent(screenLogs, strings.Join(m.logBuf, "\n"))
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

	case usageTableMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.usageAllRows, m.usageIsAgg = msg.rows, msg.isAgg
		m.statusMsg = ""
		m.applyUsageFilter()
		return m, nil

	case diagMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.renderDiag(msg.rows)
		return m, nil

	case sizingMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.renderSizing(msg.advice)
		return m, nil

	case accessMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.renderAccess(msg.report)
		return m, nil

	case connectivityMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.renderConnectivity(msg.report)
		return m, nil

	case postureMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		}
		m.renderPosture(msg.rows)
		return m, nil

	case sizingListMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.sizingAllRows, m.sizingAllObjs = msg.rows, msg.objs
		m.applySizingFilter() // filter + user sort survive refreshes
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
			m.setDetailContent(describeContent(m.detailObj, msg.events, m.theme, m.width) + msg.extra)
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
	// View-name typing mode captures ALL keys (Enter saves, Esc cancels).
	if m.viewNaming {
		switch msg.Type {
		case tea.KeyEnter:
			m.viewNaming = false
			m.saveCurrentView(strings.TrimSpace(m.viewName))
			return m, nil
		case tea.KeyEscape:
			m.viewNaming = false
			return m, nil
		case tea.KeyBackspace:
			if m.viewName != "" {
				m.viewName = m.viewName[:len(m.viewName)-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.viewName += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}

	// Custom-column typing mode captures ALL keys (Enter adds, Esc cancels).
	if m.fieldNaming {
		switch msg.Type {
		case tea.KeyEnter:
			m.fieldNaming = false
			m.addCustomColumn(strings.TrimSpace(m.fieldInput))
			return m, nil
		case tea.KeyEscape:
			m.fieldNaming = false
			return m, nil
		case tea.KeyBackspace:
			if m.fieldInput != "" {
				m.fieldInput = m.fieldInput[:len(m.fieldInput)-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.fieldInput += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}

	// Viewport search typing mode captures ALL keys (Enter searches).
	if m.searchTyping {
		switch msg.Type {
		case tea.KeyEnter:
			m.searchTyping = false
			m.searchQuery = strings.TrimSpace(m.searchInput)
			m.searchScreen = m.screen
			m.applySearch(true)
			return m, nil
		case tea.KeyEscape:
			m.searchTyping = false
			m.searchInput = ""
			return m, nil
		case tea.KeyBackspace:
			if m.searchInput != "" {
				m.searchInput = m.searchInput[:len(m.searchInput)-1]
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.searchInput += string(msg.Runes)
			return m, nil
		}
		return m, nil
	}

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

	// Helm filter typing mode captures ALL keys, like the events filter.
	if m.screen == screenHelm && m.helmFiltering {
		switch msg.Type {
		case tea.KeyEnter:
			m.helmFiltering = false // keep the query (visible as a chip)
			m.renderHelm()
			return m, nil
		case tea.KeyEscape:
			m.helmFiltering = false
			m.helmQuery = ""
			m.renderHelm()
			return m, nil
		case tea.KeyBackspace:
			if m.helmQuery != "" {
				m.helmQuery = m.helmQuery[:len(m.helmQuery)-1]
				m.renderHelm()
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.helmQuery += string(msg.Runes)
			m.renderHelm()
			return m, nil
		}
		return m, nil
	}

	// Sizing-overview filter typing mode (same behavior as the helm list).
	if m.screen == screenSizingList && m.sizingFiltering {
		switch msg.Type {
		case tea.KeyEnter:
			m.sizingFiltering = false
			m.applySizingFilter()
			return m, nil
		case tea.KeyEscape:
			m.sizingFiltering = false
			m.sizingQuery = ""
			m.applySizingFilter()
			return m, nil
		case tea.KeyBackspace:
			if m.sizingQuery != "" {
				m.sizingQuery = m.sizingQuery[:len(m.sizingQuery)-1]
				m.applySizingFilter()
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.sizingQuery += string(msg.Runes)
			m.applySizingFilter()
			return m, nil
		}
		return m, nil
	}

	// Usage-view filter typing mode (same behavior as the other tables).
	if m.screen == screenTop && m.usageTyping {
		switch msg.Type {
		case tea.KeyEnter:
			m.usageTyping = false
			m.applyUsageFilter()
			return m, nil
		case tea.KeyEscape:
			m.usageTyping = false
			m.usageFilterQ = ""
			m.applyUsageFilter()
			return m, nil
		case tea.KeyBackspace:
			if m.usageFilterQ != "" {
				m.usageFilterQ = m.usageFilterQ[:len(m.usageFilterQ)-1]
				m.applyUsageFilter()
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			m.usageFilterQ += string(msg.Runes)
			m.applyUsageFilter()
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
			m.persistViewPref()
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

	// Consistent '/' + n/N on every content viewport (owner request).
	if m.searchableNow() {
		if mi, cmd, handled := m.handleSearchKey(msg); handled {
			return mi, cmd
		}
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
	case screenSizing:
		var cmd tea.Cmd
		m.sizingVP, cmd = m.sizingVP.Update(msg)
		return m, cmd
	case screenPosture:
		var cmd tea.Cmd
		m.posture, cmd = m.posture.Update(msg)
		return m, cmd
	case screenConnectivity:
		var cmd tea.Cmd
		m.connectivity, cmd = m.connectivity.Update(msg)
		return m, cmd
	case screenAccess:
		var cmd tea.Cmd
		m.access, cmd = m.access.Update(msg)
		return m, cmd
	case screenDrift:
		var cmd tea.Cmd
		m.drift, cmd = m.drift.Update(msg)
		return m, cmd
	case screenSizingList:
		switch {
		case hit(msg, m.keys.Open):
			return m.openSizingDetail(m.sizingWin.cursor)
		case hit(msg, m.keys.Filter):
			m.sizingFiltering = true
			return m, nil
		case hit(msg, m.keys.Sort):
			m.sizingSortCol++
			if m.sizingSortCol >= len(m.sizingColumns()) {
				m.sizingSortCol = -1 // back to severity (worst first)
			}
			m.sizingSortAsc = true
			m.applySizingSort()
			return m, nil
		case hit(msg, m.keys.SortDir):
			m.sizingSortAsc = !m.sizingSortAsc
			m.applySizingSort()
			return m, nil
		}
		m.navigate(&m.sizingWin, msg)
		return m, nil
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
	switch {
	case hit(msg, m.keys.Filter):
		m.usageTyping = true
		return m, nil
	case hit(msg, m.keys.Sort):
		m.usageSortCol++
		if m.usageSortCol >= len(m.usageColumns()) {
			m.usageSortCol = -1 // back to CPU-desc default
		}
		m.usageSortAsc = true
		m.applyUsageSort()
		return m, nil
	case hit(msg, m.keys.SortDir):
		m.usageSortAsc = !m.usageSortAsc
		m.applyUsageSort()
		return m, nil
	}
	m.navigate(&m.usageWin, msg)
	return m, nil
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
		// Owner decision 2026-07-09 (rev.): usage reads pods, so it lives
		// behind the pods and deployments lists (per-deployment aggregate).
		if !strings.EqualFold(m.curType.Kind, "Pod") && !strings.EqualFold(m.curType.Kind, "Deployment") {
			m.statusMsg = "usage: open it from the pods (:po) or deployments (:deploy) list"
			return m, nil
		}
		return m.openTop()
	case hit(msg, m.keys.Diag):
		return m.openDiag()
	case hit(msg, m.keys.Sizing):
		return m.openSizing()
	case hit(msg, m.keys.Posture):
		return m.openPosture()
	case hit(msg, m.keys.Connectivity):
		return m.openConnectivity()
	case hit(msg, m.keys.Access):
		return m.openAccess()
	case hit(msg, m.keys.Drift):
		return m.openDrift()
	case hit(msg, m.keys.Topology):
		if !strings.EqualFold(m.curType.Kind, "Node") && !strings.EqualFold(m.curType.Kind, "Deployment") {
			m.statusMsg = "topology: open it from the deployments (:deploy) or nodes (:no) list"
			return m, nil
		}
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
		m.persistViewPref()
		return m, nil
	case hit(msg, m.keys.SortDir):
		if m.sortCol >= 1 {
			m.sortAsc = !m.sortAsc
			m.applyRows()
			m.persistViewPref()
		}
		return m, nil
	case hit(msg, m.keys.Columns):
		return m.openColumnChooser()
	case hit(msg, m.keys.Views):
		return m.openViewPicker()
	case hit(msg, m.keys.ResetView):
		m.resetCurrentView()
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
		case screenSizingList:
			m.sizingWin.Move(delta)
			return m, nil
		case screenTop:
			m.usageWin.Move(delta)
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
				m.persistViewPref()
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
	case screenTop:
		if msg.Y == 2 {
			if col, ok := m.usageColumnAt(msg.X); ok {
				if m.usageSortCol == col {
					m.usageSortAsc = !m.usageSortAsc
				} else {
					m.usageSortCol, m.usageSortAsc = col, true
				}
				m.applyUsageSort()
			}
			return m, nil
		}
		m.usageWin.ClickVisible(msg.Y - 3)
		return m, nil
	case screenSizingList:
		if msg.Y == 2 {
			if col, ok := m.sizingColumnAt(msg.X); ok {
				if m.sizingSortCol == col {
					m.sizingSortAsc = !m.sizingSortAsc
				} else {
					m.sizingSortCol, m.sizingSortAsc = col, true
				}
				m.applySizingSort()
			}
			return m, nil
		}
		if m.sizingWin.ClickVisible(msg.Y - 3) {
			if doubleClick(m.sizingWin.cursor) {
				return m.openSizingDetail(m.sizingWin.cursor)
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
				if m.pickerKind == pickColumns {
					// Chooser: a single click toggles the column.
					m.toggleColItem(m.pickerWin.cursor)
					return m, nil
				}
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
	// Column chooser: Space toggles, ←/→ reorders; no type-to-filter (and no
	// key may leak to global shortcuts while the chooser is open).
	if m.pickerKind == pickColumns {
		switch msg.Type {
		case tea.KeySpace:
			m.toggleColItem(m.pickerWin.cursor)
			return m, nil
		case tea.KeyLeft, tea.KeyRight:
			i, j := m.pickerWin.cursor, m.pickerWin.cursor-1
			if msg.Type == tea.KeyRight {
				j = i + 1
			}
			if i >= 0 && i < len(m.colItems) && j >= 0 && j < len(m.colItems) &&
				m.colItems[i].title != addFieldLabel && m.colItems[j].title != addFieldLabel {
				m.colItems[i], m.colItems[j] = m.colItems[j], m.colItems[i]
				m.applyColumnRows()
				m.pickerWin.Move(j - i)
				m.pickerWin.Sync(&m.picker)
			}
			return m, nil
		case tea.KeyBackspace, tea.KeyDelete:
			m.removeColItem(m.pickerWin.cursor)
			return m, nil
		case tea.KeyRunes:
			return m, nil
		}
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
	case screenDiag:
		m.diag, cmd = m.diag.Update(msg)
	case screenSizing:
		m.sizingVP, cmd = m.sizingVP.Update(msg)
	case screenPosture:
		m.posture, cmd = m.posture.Update(msg)
	case screenConnectivity:
		m.connectivity, cmd = m.connectivity.Update(msg)
	case screenAccess:
		m.access, cmd = m.access.Update(msg)
	case screenDrift:
		m.drift, cmd = m.drift.Update(msg)
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
	// Vim-like: on a searched viewport, the first Esc clears the search.
	if m.searchQuery != "" && m.screen == m.searchScreen {
		m.clearSearch()
		return m, nil
	}
	m.searchQuery, m.searchInput, m.searchHits = "", "", nil
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
	if m.screen == screenList && (m.drillSelector != "" || m.drillNode != "") {
		cmd := m.exitDrill()
		m.layout()
		return m, cmd
	}
	if m.screen == screenSizing && m.sizingFrom == screenSizingList {
		m.sizingFrom = screenList
		m.screen = screenSizingList
		m.layout()
		return m, nil
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
func describeContent(obj model.ResourceObject, events []model.Event, th theme.Theme, width int) string {
	if width < 40 {
		width = 100
	}
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
			// Full message, hard-wrapped under the MESSAGE column (owner bug
			// 2026-07-07: truncated messages hid the actual diagnosis).
			const msgCol = 64 // 2+28+1+7+1+24+1
			avail := width - msgCol - 1
			if avail < 24 {
				fmt.Fprintf(&b, "  %-28s %-7s %-24s\n", ctype, cstatus, orDash(reason))
				for _, l := range wrapTo(orDash(message), width-8) {
					b.WriteString("      " + l + "\n")
				}
				continue
			}
			chunks := wrapTo(orDash(message), avail)
			fmt.Fprintf(&b, "  %-28s %-7s %-24s %s\n", ctype, cstatus, orDash(reason), chunks[0])
			for _, l := range chunks[1:] {
				b.WriteString(strings.Repeat(" ", msgCol) + l + "\n")
			}
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
			// Full message, wrapped — never truncated (owner bug 2026-07-07).
			full := fmt.Sprintf("  %-5s %s %s%s — %s", kube.Age(e.Time, now), eventBadge(e), e.Reason, cnt, e.Message)
			for j, l := range wrapTo(full, width-4) {
				if j > 0 {
					l = "    " + l
				}
				if e.Warning() {
					b.WriteString(th.Warning.Render(l))
				} else {
					b.WriteString(th.Faint.Render(l))
				}
				b.WriteString("\n")
			}
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
	if !found || strings.EqualFold(m.curType.Kind, "Pod") || m.drillSelector != "" || m.drillNode != "" {
		return nil, false
	}
	pods, okType := findTypeByKey(m.types, "v1/pods")
	if !okType {
		pods = model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	}
	// A node has no selector: drilling shows the pods scheduled on it —
	// the list twin of the topology view (complementary, not redundant:
	// topology is capacity-oriented, this is a full pod list with filter,
	// sort, marks, logs…).
	if strings.EqualFold(m.curType.Kind, "Node") {
		m.drillPrevType = m.curType
		m.drillNode = obj.Name
		m.drillFor = "Node/" + obj.Name
		m.curType = pods
		m.statusMsg = "pods on " + m.drillFor + " — Esc to go back"
		return m.listObjects(), true
	}
	sel, ok := kube.PodSelector(obj.Raw)
	if !ok {
		return nil, false
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
	m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
	m.curType = ownerType
	m.filter.SetValue(ref.Name)
	m.statusMsg = "owner of " + obj.Name + ": " + ref.Kind + "/" + ref.Name
	return m.listObjects()
}

// exitDrill restores the workload list the drill-down came from.
func (m *Model) exitDrill() tea.Cmd {
	m.curType = m.drillPrevType
	m.drillSelector = ""
	m.drillNode = ""
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
	m.setDetailContent(describeContent(obj, nil, m.theme, m.width) + "\nEvents: loading…")
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
	m.setDetailContent(b.String())
}

func (m Model) usagePanel() string {
	w := 20
	return "Usage (last 1h):\n" +
		"  " + components.UsageLine("CPU", m.detailCPU, w) + "\n" +
		"  " + components.UsageLine("MEM", m.detailMem, w) + "\n"
}

func (m *Model) openTop() (tea.Model, tea.Cmd) {
	m.screen = screenTop
	m.usageRows, m.usageAllRows = nil, nil
	m.usageWin.SetRows(nil)
	if !m.metrics.Enabled() {
		m.errMsg = "usage: metrics unavailable (no Prometheus reachable — see --prometheus-url)"
		m.screen = screenList
		return m, nil
	}
	m.statusMsg = "observing usage…"
	m.layout()
	// Deployments aggregate their pods' usage; the pods view lists them raw.
	var workloads []model.ResourceObject
	if !strings.EqualFold(m.curType.Kind, "Pod") {
		src := m.rowObjs
		if len(m.marked) > 0 {
			src = make([]model.ResourceObject, 0, len(m.marked))
			for _, o := range m.marked {
				src = append(src, o)
			}
		}
		for _, o := range src {
			if _, ok := kube.PodSelectorLabels(o.Raw); ok {
				workloads = append(workloads, o)
			}
		}
	}
	return m, m.fetchUsage(workloads)
}

// fetchUsage builds the usage rows: two instant per-pod queries feed both
// the pods view and the per-workload aggregation (cost independent of the
// row count).
func (m Model) fetchUsage(workloads []model.ResourceObject) tea.Cmd {
	cl, mc, ns := m.client, m.metrics, m.client.Namespace
	isAgg := len(workloads) > 0
	exactNS := ns
	if kube.IsNamespacePattern(ns) {
		exactNS = ""
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		toMap := func(rows []model.TopConsumer) map[string]float64 {
			out := make(map[string]float64, len(rows))
			for _, r := range rows {
				out[r.Namespace+"/"+r.Name] = r.Value
			}
			return out
		}
		cpu := toMap(mc.TopN(ctx, metrics.ScopeNowByPod(exactNS, model.MetricCPU), model.MetricCPU))
		mem := toMap(mc.TopN(ctx, metrics.ScopeNowByPod(exactNS, model.MetricMemory), model.MetricMemory))
		podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
		pods, err := cl.List(ctx, podType, ns)
		if err != nil {
			return usageTableMsg{err: err}
		}
		var rows []model.UsageRow
		if !isAgg {
			rows = make([]model.UsageRow, 0, len(pods))
			for _, p := range pods {
				key := p.Namespace + "/" + p.Name
				c, hc := cpu[key]
				mv, hm := mem[key]
				rows = append(rows, model.UsageRow{
					Namespace: p.Namespace, Name: p.Name, Pods: 1,
					CPU: c, Mem: mv, HasCPU: hc, HasMem: hm,
				})
			}
		} else {
			rows = make([]model.UsageRow, 0, len(workloads))
			for _, wl := range workloads {
				sel, ok := kube.PodSelectorLabels(wl.Raw)
				if !ok {
					continue
				}
				row := model.UsageRow{Namespace: wl.Namespace, Name: wl.Name}
				for _, p := range pods {
					if p.Namespace != wl.Namespace || !kube.LabelsMatch(p.Raw, sel) {
						continue
					}
					row.Pods++
					key := p.Namespace + "/" + p.Name
					if v, found := cpu[key]; found {
						row.CPU += v
						row.HasCPU = true
					}
					if v, found := mem[key]; found {
						row.Mem += v
						row.HasMem = true
					}
				}
				rows = append(rows, row)
			}
		}
		return usageTableMsg{rows: rows, isAgg: isAgg}
	}
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
	} else if (m.drillSelector != "" || m.drillNode != "") && len(m.objects) > 0 {
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
	// Full message of the selected event (owner bug 2026-07-07: the one-line
	// rows are width-truncated for click geometry, so the selection gets a
	// dedicated wrapped block — placed AFTER the rows, geometry untouched).
	if m.recentSel >= 0 && m.recentSel < len(evs) {
		e := evs[m.recentSel]
		b.WriteString("\n" + m.rule("selected event — full message") + "\n")
		cnt := ""
		if e.Count > 1 {
			cnt = fmt.Sprintf(" x%d", e.Count)
		}
		head := fmt.Sprintf("  %s %s %s/%s — %s%s", kube.Age(e.Time, now), eventBadge(e),
			e.ObjKind, e.ObjName, e.Reason, cnt)
		b.WriteString(m.theme.Section.Render(truncate(head, m.width-2)) + "\n")
		for _, l := range wrapTo(e.Message, m.width-6) {
			line := "  " + l
			if e.Warning() {
				b.WriteString(m.theme.Warning.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteString("\n")
		}
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
	q := strings.ToLower(strings.TrimSpace(m.helmQuery))
	rows := make([]table.Row, 0, len(m.helmRows))
	for _, r := range m.helmRows {
		if q != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Name+" "+r.Chart), q) {
			continue
		}
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
	case hit(msg, m.keys.Filter):
		m.helmFiltering = true
		return m, nil
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
	m.setHelmHistContent("loading release " + ns + "/" + name + "…")
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
		m.setHelmHistContent(b.String())
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
		m.setHelmHistContent(b.String())
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

	m.setHelmHistContent(b.String())
	m.helmHist.GotoTop()
}

func (m *Model) openTopology() (tea.Model, tea.Cmd) {
	m.screen = screenTopology
	m.setContent(screenTopology, "loading topology…")
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
	m.setContent(screenTopology, b.String())
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

// truncate shortens a plain string to n display cells. It counts RUNES, not
// bytes — multi-byte glyphs (—, ✓, →, ●) must not trigger a spurious cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
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
	m.setContent(screenDiag, "scanning for failing workloads…")
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
	b.WriteString(m.rule(fmt.Sprintf("Workload failure diagnostics — %s, %d finding(s)", scope, len(rows))) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.theme.Ok.Render("✓ no failing workloads detected"))
		m.setContent(screenDiag, b.String())
		return
	}
	// Grouped by failure type (posture-style, owner request 2026-07-07):
	// error-level groups first, then by size.
	byCat := map[string][]model.Diagnostic{}
	var order []string
	for _, d := range rows {
		c := diagCategory(d.Reason)
		if len(byCat[c]) == 0 {
			order = append(order, c)
		}
		byCat[c] = append(byCat[c], d)
	}
	sort.SliceStable(order, func(i, j int) bool {
		wi, wj := diagGroupWeight(byCat[order[i]]), diagGroupWeight(byCat[order[j]])
		if wi != wj {
			return wi > wj
		}
		return len(byCat[order[i]]) > len(byCat[order[j]])
	})
	for _, cat := range order {
		ds := byCat[cat]
		b.WriteString(m.rule(fmt.Sprintf("%s (%d)", cat, len(ds))) + "\n")
		for _, d := range ds {
			who := d.Namespace + "/" + d.Pod
			if d.Container != "" {
				who += " [" + d.Container + "]"
			}
			line := fmt.Sprintf("  %s %-45s %s", d.Level.Symbol(), truncate(who, 45), d.Reason)
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
		b.WriteString("\n")
	}
	m.setContent(screenDiag, b.String())
}

// diagCategory folds a diagnostic reason to its failure type: "OOMKilled
// (x3 restarts)" → "OOMKilled", "Evicted: node pressure" → "Evicted",
// "restarted x4" → "restarted".
func diagCategory(reason string) string {
	if i := strings.IndexAny(reason, ":("); i > 0 {
		reason = reason[:i]
	}
	fields := strings.Fields(reason)
	for len(fields) > 1 {
		last := fields[len(fields)-1]
		if strings.HasPrefix(last, "x") && len(last) > 1 {
			fields = fields[:len(fields)-1]
			continue
		}
		break
	}
	return strings.Join(fields, " ")
}

// diagGroupWeight ranks a group by its worst severity.
func diagGroupWeight(ds []model.Diagnostic) int {
	w := 0
	for _, d := range ds {
		if int(d.Level) > w {
			w = int(d.Level)
		}
	}
	return w
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
	m.setContent(screenLogs, "waiting for logs…")
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
		// Native Kubernetes types first, CRDs below (owner request
		// 2026-07-09) — each group alphabetical; type-to-filter unchanged.
		var natives, crds []string
		for _, t := range m.types {
			if t.IsCRD {
				crds = append(crds, typeOptionLabel(t))
			} else {
				natives = append(natives, typeOptionLabel(t))
			}
		}
		sort.Strings(natives)
		sort.Strings(crds)
		opts = append(append(opts, natives...), crds...)
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
	if kind != pickType {
		sort.Strings(opts)
	}
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
	case pickColumns:
		return "columns — " + m.curType.Key()
	case pickView:
		return "views"
	default:
		return "select"
	}
}

// applyPickerRows rebuilds the picker rows from pickerOpts filtered by the
// current type-to-filter query (case-insensitive substring match).
func (m *Model) applyPickerRows() {
	q := strings.ToLower(strings.TrimSpace(m.pickerQuery))
	rows := make([]table.Row, 0, len(m.pickerOpts))
	// Namespace picker: a glob query ("staging-*") becomes a selectable
	// pattern scope of its own, on top of the matching literal entries.
	if m.pickerKind == pickNamespace && kube.IsNamespacePattern(strings.TrimSpace(m.pickerQuery)) {
		rows = append(rows, table.Row{nsPatternPrefix + strings.TrimSpace(m.pickerQuery)})
	}
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
		// A type switch is a fresh view: leave any drill and the marked
		// selection, then restore the type's own saved customization —
		// filter, sort — instead of a leftover from the previous type (a
		// saved filter is always visible as a header chip, never invisible).
		m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
		m.applyViewPref()
		m.marked = map[string]model.ResourceObject{}
		m.statusMsg = ""
		m.screen = screenList
		m.layout()
		m.persist()
		return m, m.listObjects()
	case pickNamespace:
		switch {
		case choice == allNamespacesLabel:
			m.client.Namespace = "" // empty → list across all namespaces
		case strings.HasPrefix(choice, nsPatternPrefix):
			m.client.Namespace = strings.TrimPrefix(choice, nsPatternPrefix)
		default:
			m.client.Namespace = choice
		}
		m.layout()
		m.persist()
		if m.pickerReturn == screenEvents {
			// Namespace changed from the timeline: stay there and reload it.
			// A drill scope no longer applies to the new namespace.
			m.eventsScope = nil
			m.eventsScopeFor = ""
			m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
			m.screen = screenEvents
			m.events.SetContent("loading events…")
			return m, m.fetchEvents()
		}
		// Changing namespace from the list leaves any drill (its workload
		// belonged to the previous scope) and the marked selection.
		m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
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
	case pickColumns:
		if i := m.pickerWin.cursor; i >= 0 && i < len(m.colItems) && m.colItems[i].title == addFieldLabel {
			m.fieldNaming, m.fieldInput = true, ""
			return m, nil
		}
		return m.applyColumnChoice()
	case pickView:
		switch choice {
		case saveViewLabel:
			m.viewNaming, m.viewName = true, ""
			m.screen = m.pickerReturn
			m.layout()
			return m, nil
		case resetViewLabel:
			m.resetCurrentView()
			m.screen = screenList
			m.layout()
			return m, m.listObjects()
		}
		name := strings.TrimSpace(strings.SplitN(choice, "  (", 2)[0])
		for _, v := range m.cfg.SavedViews {
			if v.Name == name {
				return m.applySavedView(v)
			}
		}
		return m.goBack()
	case pickContext:
		nc, err := kube.NewClient(kube.Options{KubeconfigPath: m.kubeconfigPath, Context: choice})
		if err != nil {
			m.errMsg = err.Error()
			return m.goBack()
		}
		m.client.Close() // stop the old context's informer watches
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
	m.usageWin.SetHeight(bodyH - 1) // -1: the usage table's own header
	m.diag.Width, m.diag.Height = m.width, bodyH
	m.topo.Width, m.topo.Height = m.width, bodyH
	m.events.Width, m.events.Height = m.width, bodyH
	m.helmHist.Width, m.helmHist.Height = m.width, bodyH
	m.sizingVP.Width, m.sizingVP.Height = m.width, bodyH
	m.sizingWin.SetHeight(bodyH - 1) // -1: the overview's own column header
	m.posture.Width, m.posture.Height = m.width, bodyH
	m.connectivity.Width, m.connectivity.Height = m.width, bodyH
	m.access.Width, m.access.Height = m.width, bodyH
	m.drift.Width, m.drift.Height = m.width, bodyH
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
		return m.usageListView()
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
	case screenSizing:
		return m.sizingVP.View()
	case screenPosture:
		return m.posture.View()
	case screenConnectivity:
		return m.connectivity.View()
	case screenAccess:
		return m.access.View()
	case screenDrift:
		return m.drift.View()
	case screenSizingList:
		return m.sizingListView()
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
	case m.fieldNaming:
		out = overlayCenter(out, m.inputModal("add column — label key or .field.path",
			m.fieldInput, "e.g. app · .status.podIP — Enter add · Esc cancel"), m.width)
	case m.screen == screenPicker:
		box, _ := m.pickerModal()
		out = overlayCenter(out, box, m.width)
	case m.viewNaming:
		out = overlayCenter(out, m.inputModal("save view as", m.viewName, "Enter save · Esc cancel"), m.width)
	case m.filtering:
		out = overlayCenter(out, m.inputModal("filter "+m.curType.Resource, m.filter.Value(), "Enter save · Esc close"), m.width)
	case m.screen == screenEvents && m.eventsFiltering:
		out = overlayCenter(out, m.inputModal("filter events", m.eventsQuery, "Enter save · Esc cancel"), m.width)
	case m.screen == screenHelm && m.helmFiltering:
		out = overlayCenter(out, m.inputModal("filter helm releases", m.helmQuery, "Enter save · Esc cancel"), m.width)
	case m.searchTyping:
		out = overlayCenter(out, m.inputModal("search", m.searchInput, "Enter search · 'n'/'N' navigate · Esc clear"), m.width)
	case m.screen == screenSizingList && m.sizingFiltering:
		out = overlayCenter(out, m.inputModal("filter workloads", m.sizingQuery, "Enter save · Esc cancel"), m.width)
	case m.screen == screenTop && m.usageTyping:
		out = overlayCenter(out, m.inputModal("filter usage rows", m.usageFilterQ, "Enter save · Esc cancel"), m.width)
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
	if m.pickerKind == pickColumns {
		// No type-to-filter here; the line keeps the modal geometry stable.
		lines = append(lines, m.theme.Faint.Render(padTo("Space show/hide · ←/→ reorder · ⌫ remove custom", inner)))
	} else {
		lines = append(lines, padTo("▸ "+m.pickerQuery+"▏", inner))
	}
	for i := m.pickerWin.win; i < m.pickerWin.win+shown; i++ {
		txt := padTo(" "+truncate(m.pickerWin.rows[i][0], inner-1), inner)
		if i == m.pickerWin.cursor {
			txt = m.theme.TableSelected.Render(txt)
		}
		lines = append(lines, txt)
	}
	_, _, total := m.pickerWin.Range()
	hint := fmt.Sprintf("↑↓ · Enter ok · Esc close   %d option(s)", total)
	if m.pickerKind == pickColumns {
		hint = "Enter apply · Esc cancel"
	}
	if m.pickerKind == pickNamespace {
		hint = fmt.Sprintf("↑↓ · Enter ok · Esc close · '*' = pattern   %d option(s)", total)
	}
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
	if m.drillSelector != "" || m.drillNode != "" {
		typeLabel = "pods ⊂ " + m.drillFor
	}
	// The chip reflects what is on SCREEN, not the last browsed type — the
	// helm view is not a kube resource type (owner bug report 2026-07-07).
	sc := m.screen
	if sc == screenPicker {
		sc = m.pickerReturn
	}
	if sc == screenHelm || sc == screenHelmHist {
		typeLabel = "helm releases"
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
	case sc == screenTop && (strings.TrimSpace(m.usageFilterQ) != "" || m.usageTyping):
		line += "  " + m.theme.Warning.Render("filter:"+m.usageFilterQ) + m.theme.Faint.Render(" (/ edit)")
	case sc == screenSizingList && (strings.TrimSpace(m.sizingQuery) != "" || m.sizingFiltering):
		line += "  " + m.theme.Warning.Render("filter:"+m.sizingQuery) + m.theme.Faint.Render(" (/ edit)")
	case (sc == screenHelm || sc == screenHelmHist) && (strings.TrimSpace(m.helmQuery) != "" || m.helmFiltering):
		line += "  " + m.theme.Warning.Render("filter:"+m.helmQuery) + m.theme.Faint.Render(" (/ edit)")
	case sc != screenHelm && sc != screenHelmHist && (strings.TrimSpace(m.filter.Value()) != "" || m.filtering):
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
		// Node-oriented views only advertise themselves where they open
		// (deployments/nodes for topology, nodes for top usage).
		isNode := strings.EqualFold(m.curType.Kind, "Node")
		isDeploy := strings.EqualFold(m.curType.Kind, "Deployment")
		short := []key.Binding{k.Open, k.Mark, k.Yaml, k.Describe, k.Filter, k.Sort, k.Logs, k.Diag, k.Events}
		views := []key.Binding{k.Sort, k.SortDir, k.Diag, k.Events, k.Sizing, k.Posture, k.Connectivity, k.Access, k.Drift}
		isPod := strings.EqualFold(m.curType.Kind, "Pod")
		if isNode || isDeploy {
			short = append(short, k.Topology)
			views = append(views, k.Topology)
		}
		if isPod || isDeploy {
			short = append(short, k.Top)
			views = append(views, k.Top)
		}
		short = append(short, k.Namespace, k.Context, k.Help, k.Quit)
		return keymapView{
			short: short,
			full: [][]key.Binding{
				nav,
				{k.Open, k.Mark, k.Yaml, k.Describe, k.Filter, k.Jump, k.Logs},
				views,
				{k.Namespace, k.Context, k.Help, k.Quit},
			},
		}
	case screenHelm:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Values, k.Filter, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Values, k.Filter, k.Mouse, k.Back, k.Help, k.Quit}},
		}
	case screenEvents:
		return keymapView{
			short: []key.Binding{k.Filter, k.Kind, k.WarnOnly, k.Namespace, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Filter, k.Kind, k.WarnOnly, k.Namespace}, {k.Back, k.Help, k.Quit}},
		}
	case screenLogs:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Pause, k.End, k.Filter, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Pause, k.Filter, k.SearchNext, k.SearchPrev, k.Back, k.Help, k.Quit}},
		}
	case screenPicker:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Back, k.Quit}},
		}
	case screenTop:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Filter, k.Sort, k.SortDir, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Filter, k.Sort, k.SortDir, k.Back, k.Help, k.Quit}},
		}
	case screenSizingList:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Filter, k.Sort, k.SortDir, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Filter, k.Sort, k.SortDir, k.Back, k.Help, k.Quit}},
		}
	case screenDetail, screenHelmHist:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Filter, k.SearchNext, k.SearchPrev, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Filter, k.SearchNext, k.SearchPrev, k.Back, k.Help, k.Quit}},
		}
	default: // diag, topology, posture, connectivity, access, drift, sizing detail
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Filter, k.Back, k.Help, k.Quit},
			full:  [][]key.Binding{nav, {k.Filter, k.SearchNext, k.SearchPrev, k.Back, k.Help, k.Quit}},
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
	// Index-based: rowObjs mirrors the visible rows (applyRows), so the
	// selection is correct whatever columns the user chose to display (US8).
	if m.win.cursor < 0 || m.win.cursor >= len(m.rowObjs) {
		return model.ResourceObject{}, false
	}
	return m.rowObjs[m.win.cursor], true
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

// handleSearchKey handles '/', 'n' and 'N' on a searchable viewport.
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case hit(msg, m.keys.Filter):
		m.searchTyping, m.searchInput = true, m.searchQuery
		return m, nil, true
	case hit(msg, m.keys.SearchNext) && len(m.searchHits) > 0:
		m.searchStep(1)
		return m, nil, true
	case hit(msg, m.keys.SearchPrev) && len(m.searchHits) > 0:
		m.searchStep(-1)
		return m, nil, true
	}
	return m, nil, false
}

// ---- Viewport search ('/', vim-like — describe/YAML and helm detail) ----

// vpFor maps a screen to its content viewport ('/' searches all of them;
// the events timeline keeps its own dedicated filter instead).
func (m *Model) vpFor(sc screen) *viewport.Model {
	switch sc {
	case screenDetail:
		return &m.detail
	case screenLogs:
		return &m.logsView
	case screenDiag:
		return &m.diag
	case screenTopology:
		return &m.topo
	case screenHelmHist:
		return &m.helmHist
	case screenSizing:
		return &m.sizingVP
	case screenPosture:
		return &m.posture
	case screenConnectivity:
		return &m.connectivity
	case screenAccess:
		return &m.access
	case screenDrift:
		return &m.drift
	}
	return nil
}

// searchableNow reports whether '/' means viewport search on this screen.
func (m *Model) searchableNow() bool { return m.vpFor(m.screen) != nil }

// setContent stores a viewport's raw content and renders it, keeping an
// active search highlighted when the content refreshes (describe events
// landing, log lines streaming in…).
func (m *Model) setContent(sc screen, content string) {
	vp := m.vpFor(sc)
	if vp == nil {
		return
	}
	if m.vpRaw == nil {
		m.vpRaw = map[screen]string{}
	}
	m.vpRaw[sc] = content
	if m.searchQuery != "" && m.searchScreen == sc {
		m.applySearch(false)
		return
	}
	vp.SetContent(content)
}

// setDetailContent keeps existing call sites readable.
func (m *Model) setDetailContent(content string) { m.setContent(screenDetail, content) }

// setHelmHistContent keeps existing call sites readable.
func (m *Model) setHelmHistContent(content string) { m.setContent(screenHelmHist, content) }

// searchTarget maps the search's own screen to its viewport and raw content.
func (m *Model) searchTarget() (*viewport.Model, string) {
	if vp := m.vpFor(m.searchScreen); vp != nil {
		return vp, m.vpRaw[m.searchScreen]
	}
	return nil, ""
}

// clearSearch restores the unhighlighted content.
func (m *Model) clearSearch() {
	m.searchQuery, m.searchInput = "", ""
	m.searchHits, m.searchIdx = nil, 0
	if vp, raw := m.searchTarget(); vp != nil {
		vp.SetContent(raw)
	}
	m.statusMsg = ""
}

// applySearch highlights every match and (optionally) jumps to the first.
func (m *Model) applySearch(jumpFirst bool) {
	vp, raw := m.searchTarget()
	if vp == nil {
		return
	}
	if m.searchQuery == "" {
		m.clearSearch()
		return
	}
	highlighted, hits := highlightMatches(raw, m.searchQuery, m.theme.Highlight.Render)
	vp.SetContent(highlighted)
	m.searchHits = hits
	if len(hits) == 0 {
		m.statusMsg = "no match for “" + m.searchQuery + "” — Esc clears"
		return
	}
	if jumpFirst {
		m.searchIdx = 0
	}
	if m.searchIdx >= len(hits) {
		m.searchIdx = 0
	}
	m.gotoSearchHit()
}

// gotoSearchHit scrolls the current match into view (upper third).
func (m *Model) gotoSearchHit() {
	vp, _ := m.searchTarget()
	if vp == nil || len(m.searchHits) == 0 {
		return
	}
	off := m.searchHits[m.searchIdx] - vp.Height/3
	if off < 0 {
		off = 0
	}
	vp.SetYOffset(off)
	m.statusMsg = fmt.Sprintf("match %d/%d for “%s” — 'n' next · 'N' previous · Esc clears",
		m.searchIdx+1, len(m.searchHits), m.searchQuery)
}

// searchStep moves to the next/previous match, wrapping around.
func (m *Model) searchStep(dir int) {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx + dir + len(m.searchHits)) % len(m.searchHits)
	m.gotoSearchHit()
}

// highlightMatches rebuilds matching lines with the query highlighted
// (case-insensitive). Styled lines are flattened to plain text on match so
// the highlight offsets stay exact — the searched term wins over syntax
// color on those lines. Returns the content and the matching line numbers.
func highlightMatches(content, query string, mark func(...string) string) (string, []int) {
	q := strings.ToLower(query)
	lines := strings.Split(content, "\n")
	var hits []int
	for i, l := range lines {
		plain := xansi.Strip(l)
		low := strings.ToLower(plain)
		if !strings.Contains(low, q) {
			continue
		}
		hits = append(hits, i)
		var b strings.Builder
		for {
			j := strings.Index(low, q)
			if j < 0 {
				b.WriteString(plain)
				break
			}
			b.WriteString(plain[:j])
			end := j + len(q)
			if end > len(plain) {
				end = len(plain)
			}
			b.WriteString(mark(plain[j:end]))
			plain, low = plain[end:], low[end:]
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n"), hits
}

// ---- Drift view (US16, FR-033 — read-only, no apply anywhere) ----

// openDrift compares the selected object's live state against its
// last-applied configuration. Pure local derivation — nothing is fetched
// and nothing can be applied.
func (m *Model) openDrift() (tea.Model, tea.Cmd) {
	obj, found := m.selectedObject()
	if !found {
		m.statusMsg = "diff: select an object first"
		return m, nil
	}
	m.screen = screenDrift
	subject := m.curType.Kind + " " + obj.Namespace + "/" + obj.Name
	drifts, hasBaseline := kube.Drift(obj.Raw)
	var b strings.Builder
	b.WriteString(m.rule("Diff (read-only) — "+subject+" vs last-applied") + "\n\n")
	switch {
	case !hasBaseline:
		b.WriteString(m.theme.Faint.Render("No baseline: this object has no last-applied configuration annotation\n"+
			"(it was not created with 'kubectl apply' or declarative tooling), so there\n"+
			"is nothing to diff against. Nothing is wrong — there is just no reference.") + "\n")
	case len(drifts) == 0:
		b.WriteString(m.theme.Ok.Render("✓ no drift — the live object matches its last-applied configuration") + "\n")
	default:
		fmt.Fprintf(&b, "%d drifted field(s) — fields present in the baseline only; server defaults are not drift:\n\n", len(drifts))
		fmt.Fprintf(&b, "  %s\n", m.theme.Faint.Render(fmt.Sprintf("%-44s %-28s %s", "FIELD", "APPLIED", "LIVE")))
		for _, d := range drifts {
			line := fmt.Sprintf("  ~ %-42s %-28s %s", truncate(d.Path, 42), truncate(d.Applied, 28), truncate(d.Live, 40))
			if d.Live == "<absent>" {
				line = m.theme.Error.Render(line)
			} else {
				line = m.theme.Warning.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	m.setContent(screenDrift, b.String())
	m.layout()
	return m, nil
}

// ---- Access (RBAC) view (US15, FR-032 — read-only introspection) ----

// openAccess asks the API server what the current credentials can read.
func (m *Model) openAccess() (tea.Model, tea.Cmd) {
	m.screen = screenAccess
	m.setContent(screenAccess, "asking the API server what you can read…")
	m.layout()
	cl, ns, types := m.client, m.client.Namespace, m.types
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rep, err := cl.AccessSummary(ctx, ns, types)
		return accessMsg{report: rep, err: err}
	}
}

// renderAccess shows the server's own answer: allowed reads, then the
// browsable types these credentials cannot list (the view's blind spots).
func (m *Model) renderAccess(r model.AccessReport) {
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Access (RBAC) — what you can read in ns %s", r.Namespace)) + "\n\n")
	b.WriteString(m.theme.Faint.Render("The API server's own answer (SelfSubjectRulesReview) — nothing is guessed.") + "\n\n")
	if r.Incomplete || r.Evaluation != "" {
		note := "⚠ the server reported this rule set as INCOMPLETE"
		if r.Evaluation != "" {
			note += " — " + r.Evaluation
		}
		b.WriteString(m.theme.Warning.Render(note) + "\n\n")
	}
	b.WriteString(m.rule(fmt.Sprintf("allowed reads (%d rule(s))", len(r.Rules))) + "\n")
	if len(r.Rules) == 0 {
		b.WriteString("  " + m.theme.Error.Render("✗ no read access in this namespace") + "\n")
	}
	fmt.Fprintf(&b, "  %s\n", m.theme.Faint.Render(fmt.Sprintf("%-18s %-22s %s", "VERBS", "GROUPS", "RESOURCES")))
	for _, rule := range r.Rules {
		groups := strings.Join(rule.Groups, ",")
		if groups == "" {
			groups = `""`
		}
		res := strings.Join(rule.Resources, ", ")
		if len(rule.Names) > 0 {
			res += "  (only: " + strings.Join(rule.Names, ", ") + ")"
		}
		fmt.Fprintf(&b, "  %-18s %-22s %s\n", strings.Join(rule.Verbs, ","), truncate(groups, 22), res)
	}
	b.WriteString("\n")
	b.WriteString(m.rule(fmt.Sprintf("not listable with these credentials (%d type(s))", len(r.Unlistable))) + "\n")
	if len(r.Unlistable) == 0 {
		b.WriteString("  " + m.theme.Ok.Render("✓ every discovered type is listable") + "\n")
	}
	for _, t := range r.Unlistable {
		b.WriteString("  " + m.theme.Warning.Render("! "+t) + "\n")
	}
	m.setContent(screenAccess, b.String())
	m.layout()
}

// ---- Connectivity / NetworkPolicy view (US14, FR-031 — read-only) ----

// openConnectivity shows which NetworkPolicies select the selected pod (or a
// workload, evaluated on its pod template labels) and what they allow.
func (m *Model) openConnectivity() (tea.Model, tea.Cmd) {
	obj, found := m.selectedObject()
	if !found {
		m.statusMsg = "connectivity: select a pod or a workload first"
		return m, nil
	}
	var labels map[string]string
	var subject string
	if strings.EqualFold(m.curType.Kind, "Pod") {
		labels, _, _ = unstructured.NestedStringMap(obj.Raw, "metadata", "labels")
		subject = "pod " + obj.Namespace + "/" + obj.Name
	} else if tpl, found, _ := unstructured.NestedStringMap(obj.Raw, "spec", "template", "metadata", "labels"); found && len(tpl) > 0 {
		labels = tpl
		subject = "pods of " + m.curType.Kind + "/" + obj.Name
	} else {
		m.statusMsg = "connectivity: " + m.curType.Kind + " has no pod template — open it on a pod or workload"
		return m, nil
	}
	m.screen = screenConnectivity
	m.setContent(screenConnectivity, "evaluating NetworkPolicies for "+subject+"…")
	m.layout()
	cl, ns := m.client, obj.Namespace
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rep, err := cl.Connectivity(ctx, ns, subject, labels)
		return connectivityMsg{report: rep, err: err}
	}
}

// renderConnectivity renders the per-direction summary; the unrestricted
// state is explicit (FR-031), never an empty screen.
func (m *Model) renderConnectivity(r model.ConnectivityReport) {
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Connectivity — %s", r.Subject)) + "\n\n")
	if len(r.Policies) == 0 {
		b.WriteString(m.theme.Warning.Render("⚠ UNRESTRICTED — no NetworkPolicy selects it: all ingress and egress allowed") + "\n")
		m.setContent(screenConnectivity, b.String())
		m.layout()
		return
	}
	fmt.Fprintf(&b, "selected by %d NetworkPolicy(ies): %s\n\n", len(r.Policies), strings.Join(r.Policies, ", "))
	m.renderDirection(&b, "INGRESS", r.IngressRestricted, r.Ingress)
	b.WriteString("\n")
	m.renderDirection(&b, "EGRESS", r.EgressRestricted, r.Egress)
	m.setContent(screenConnectivity, b.String())
	m.layout()
}

func (m *Model) renderDirection(b *strings.Builder, label string, restricted bool, rules []model.PolicyRule) {
	b.WriteString(m.rule(label) + "\n")
	if !restricted {
		b.WriteString("  " + m.theme.Warning.Render("unrestricted — no "+strings.ToLower(label)+" policy selects it") + "\n")
		return
	}
	if len(rules) == 0 {
		b.WriteString("  " + m.theme.Error.Render("✗ default deny — a policy declares "+label+" but allows nothing") + "\n")
		return
	}
	b.WriteString("  " + m.theme.Ok.Render(fmt.Sprintf("restricted — only the following is allowed (%d rule(s)):", len(rules))) + "\n")
	for _, r := range rules {
		ports := "all ports"
		if len(r.Ports) > 0 {
			ports = strings.Join(r.Ports, ", ")
		}
		fmt.Fprintf(b, "    ✓ %-24s %s — %s\n", r.Policy, strings.Join(r.Peers, "; "), ports)
	}
}

// ---- Posture overview (US13, FR-030 — advisory, read-only) ----

// openPosture evaluates the compliance rules over the current scope.
func (m *Model) openPosture() (tea.Model, tea.Cmd) {
	m.screen = screenPosture
	m.setContent(screenPosture, "checking posture rules…")
	m.layout()
	return m, m.fetchPosture()
}

func (m Model) fetchPosture() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rows, err := cl.Posture(ctx, ns, time.Now())
		return postureMsg{rows: rows, err: err}
	}
}

// renderPosture groups the findings by rule, worst severities first inside
// each group; every line references the concrete object and field (FR-030).
func (m *Model) renderPosture(rows []model.PostureFinding) {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Posture (advisory) — %s, %d finding(s)", scope, len(rows))) + "\n\n")
	b.WriteString(m.theme.Faint.Render("Best-practice review of the observed configuration — read-only, nothing is applied.") + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.theme.Ok.Render("✓ no findings — every rule passes on this scope") + "\n")
		m.setContent(screenPosture, b.String())
		m.layout()
		return
	}
	byRule := map[string][]model.PostureFinding{}
	var order []string
	for _, f := range rows {
		if len(byRule[f.Rule]) == 0 {
			order = append(order, f.Rule)
		}
		byRule[f.Rule] = append(byRule[f.Rule], f)
	}
	for _, rule := range order {
		fs := byRule[rule]
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].Severity > fs[j].Severity })
		b.WriteString(m.rule(fmt.Sprintf("%s (%d)", rule, len(fs))) + "\n")
		for _, f := range fs {
			ref := f.Namespace + "/" + f.Name
			if f.Container != "" {
				ref += " [" + f.Container + "]"
			}
			line := fmt.Sprintf("  %s %-52s %s", f.Severity.Symbol(), truncate(ref, 52), f.Detail)
			switch f.Severity {
			case model.HealthError:
				line = m.theme.Error.Render(line)
			case model.HealthWarning:
				line = m.theme.Warning.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	m.setContent(screenPosture, b.String())
	m.layout()
}

// ---- Sizing recommendations (US6, FR-023 — advisory, read-only) ----

// openSizing ('z'): on a pods list, the selected pod's detail panel; on a
// workload list, the overview TABLE of every visible workload — Enter on a
// row then opens its detail panel.
func (m *Model) openSizing() (tea.Model, tea.Cmd) {
	if strings.EqualFold(m.curType.Kind, "Pod") {
		obj, found := m.selectedObject()
		if !found {
			m.statusMsg = "sizing: select a pod first"
			return m, nil
		}
		if !m.metrics.Enabled() {
			return m.openSizingUnavailable()
		}
		return m.openSizingFor(obj, "", screenList)
	}
	// Workloads with a pod selector, scoped like 'f'/'v': the marked set if
	// any, otherwise everything currently visible (filter applied).
	src := m.rowObjs
	if len(m.marked) > 0 {
		src = make([]model.ResourceObject, 0, len(m.marked))
		for _, o := range m.marked {
			src = append(src, o)
		}
	}
	var workloads []model.ResourceObject
	for _, o := range src {
		if _, ok := kube.PodSelectorLabels(o.Raw); ok {
			workloads = append(workloads, o)
		}
	}
	if len(workloads) == 0 {
		m.statusMsg = "sizing: no workload with pods here — open it on deployments, statefulsets…"
		return m, nil
	}
	if !m.metrics.Enabled() {
		return m.openSizingUnavailable()
	}
	m.screen = screenSizingList
	m.sizingRows, m.sizingObjs = nil, nil
	m.sizingWin.SetRows(nil)
	m.statusMsg = fmt.Sprintf("observing %d workload(s) over the last hour…", len(workloads))
	m.layout()
	return m, m.fetchSizingList(workloads)
}

// openSizingUnavailable states the explicit no-metrics case (FR-023/SC-013):
// without observed data there is NO recommendation — never an estimate.
func (m *Model) openSizingUnavailable() (tea.Model, tea.Cmd) {
	m.screen = screenSizing
	m.sizingFrom = screenList
	m.setContent(screenSizing, m.rule("Sizing (advisory)")+"\n\n"+
		"No recommendation: metrics are unavailable (no Prometheus reachable\n"+
		"for this context). Sizing is derived only from observed usage —\n"+
		"nothing is ever estimated. Use --prometheus-url to force a source.")
	m.layout()
	return m, nil
}

// openSizingFor opens the detail panel for one workload or pod.
func (m *Model) openSizingFor(obj model.ResourceObject, selector string, from screen) (tea.Model, tea.Cmd) {
	m.screen = screenSizing
	m.sizingFrom = from
	m.sizingFor = m.curType.Kind + "/" + obj.Name
	m.setContent(screenSizing, "observing "+m.sizingFor+" over the last hour…")
	m.layout()
	return m, m.fetchSizing(obj, selector)
}

// openSizingDetail opens the detail panel from an overview row.
func (m Model) openSizingDetail(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.sizingObjs) {
		return m, nil
	}
	obj := m.sizingObjs[i]
	sel, _ := kube.PodSelector(obj.Raw)
	return m.openSizingFor(obj, sel, screenSizingList)
}

// fetchSizingList feeds the overview: ONE pods list + FOUR Prometheus
// queries (per-pod avg/peak × cpu/mem over the window), then client-side
// selector matching and verdicts — the cost does not grow with the number
// of workloads.
func (m Model) fetchSizingList(workloads []model.ResourceObject) tea.Cmd {
	cl, mc, ns, kind := m.client, m.metrics, m.client.Namespace, m.curType.Kind
	exactNS := ns
	if kube.IsNamespacePattern(ns) {
		exactNS = "" // query the cluster; workloads are already scope-filtered
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
		pods, err := cl.List(ctx, podType, ns)
		if err != nil {
			return sizingListMsg{err: err}
		}
		toMap := func(rows []model.TopConsumer) map[string]float64 {
			out := make(map[string]float64, len(rows))
			for _, r := range rows {
				out[r.Namespace+"/"+r.Name] = r.Value
			}
			return out
		}
		w := metrics.TrendWindow
		avgCPU := toMap(mc.TopN(ctx, metrics.ScopeAvgByPod(exactNS, model.MetricCPU, w), model.MetricCPU))
		peakCPU := toMap(mc.TopN(ctx, metrics.ScopePeakByPod(exactNS, model.MetricCPU, w), model.MetricCPU))
		avgMem := toMap(mc.TopN(ctx, metrics.ScopeAvgByPod(exactNS, model.MetricMemory, w), model.MetricMemory))
		peakMem := toMap(mc.TopN(ctx, metrics.ScopePeakByPod(exactNS, model.MetricMemory, w), model.MetricMemory))

		rows := make([]model.SizingAdvice, 0, len(workloads))
		objs := make([]model.ResourceObject, 0, len(workloads))
		for _, wl := range workloads {
			sel, ok := kube.PodSelectorLabels(wl.Raw)
			if !ok {
				continue
			}
			cpu := model.ResourceSizing{Kind: model.MetricCPU}
			mem := model.ResourceSizing{Kind: model.MetricMemory}
			var nPods, nCPU, nMem float64
			for _, p := range pods {
				if p.Namespace != wl.Namespace || !kube.LabelsMatch(p.Raw, sel) {
					continue
				}
				nPods++
				cr, cli, mr, ml := kube.PodResources(p.Raw)
				cpu.Request += cr
				cpu.Limit += cli
				mem.Request += mr
				mem.Limit += ml
				key := p.Namespace + "/" + p.Name
				if a, ok1 := avgCPU[key]; ok1 {
					if pk, ok2 := peakCPU[key]; ok2 {
						cpu.Avg += a
						if pk > cpu.Peak {
							cpu.Peak = pk
						}
						nCPU++
					}
				}
				if a, ok1 := avgMem[key]; ok1 {
					if pk, ok2 := peakMem[key]; ok2 {
						mem.Avg += a
						if pk > mem.Peak {
							mem.Peak = pk
						}
						nMem++
					}
				}
			}
			if nPods > 0 {
				cpu.Request, cpu.Limit = cpu.Request/nPods, cpu.Limit/nPods
				mem.Request, mem.Limit = mem.Request/nPods, mem.Limit/nPods
			}
			if nCPU > 0 {
				cpu.Avg, cpu.HasData = cpu.Avg/nCPU, true
			}
			if nMem > 0 {
				mem.Avg, mem.HasData = mem.Avg/nMem, true
			}
			rows = append(rows, model.SizingAdvice{
				Workload:  kind + "/" + wl.Name,
				Namespace: wl.Namespace,
				Pods:      int(nPods),
				CPU:       model.EvaluateSizing(cpu),
				Memory:    model.EvaluateSizing(mem),
			})
			objs = append(objs, wl)
		}
		// Worst first: what needs attention is visible without scrolling.
		// One permutation applied to both parallel slices.
		idx := make([]int, len(rows))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool {
			return adviceSeverity(rows[idx[a]]) > adviceSeverity(rows[idx[b]])
		})
		sortedRows := make([]model.SizingAdvice, len(rows))
		sortedObjs := make([]model.ResourceObject, len(objs))
		for to, from := range idx {
			sortedRows[to], sortedObjs[to] = rows[from], objs[from]
		}
		return sizingListMsg{rows: sortedRows, objs: sortedObjs}
	}
}

// adviceSeverity ranks a row by its worst verdict (under > over > no-request
// > ok > no-data).
func adviceSeverity(a model.SizingAdvice) int {
	rank := func(v model.SizingVerdict) int {
		switch v {
		case model.SizingUnder:
			return 4
		case model.SizingOver:
			return 3
		case model.SizingNoRequest:
			return 2
		case model.SizingOK:
			return 1
		default:
			return 0
		}
	}
	r1, r2 := rank(a.CPU.Verdict), rank(a.Memory.Verdict)
	if r2 > r1 {
		return r2
	}
	return r1
}

// fetchSizing resolves the pods, their configured requests/limits (cluster),
// and their observed usage (Prometheus), then evaluates the verdicts.
func (m Model) fetchSizing(obj model.ResourceObject, selector string) tea.Cmd {
	cl, mc, kind := m.client, m.metrics, m.curType.Kind
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
		pods := []model.ResourceObject{obj}
		if !strings.EqualFold(kind, "Pod") {
			var err error
			pods, err = cl.ListSelected(ctx, podType, obj.Namespace, selector)
			if err != nil {
				return sizingMsg{err: err}
			}
		}
		adv := model.SizingAdvice{Workload: kind + "/" + obj.Name, Namespace: obj.Namespace, Pods: len(pods)}
		names := make([]string, 0, len(pods))
		var cpuReq, cpuLim, memReq, memLim float64
		for _, p := range pods {
			names = append(names, p.Name)
			cr, cli, mr, ml := kube.PodResources(p.Raw)
			cpuReq += cr
			cpuLim += cli
			memReq += mr
			memLim += ml
		}
		if n := float64(len(pods)); n > 0 {
			// Per-pod configuration (pods of one workload share a template).
			cpuReq, cpuLim, memReq, memLim = cpuReq/n, cpuLim/n, memReq/n, memLim/n
		}
		cpu, mem := mc.WorkloadSizing(ctx, obj.Namespace, names, metrics.TrendWindow)
		cpu.Request, cpu.Limit = cpuReq, cpuLim
		mem.Request, mem.Limit = memReq, memLim
		adv.CPU, adv.Memory = model.EvaluateSizing(cpu), model.EvaluateSizing(mem)
		return sizingMsg{advice: adv}
	}
}

// verdictBadge renders a compact colored verdict cell for the overview.
func (m Model) verdictBadge(v model.SizingVerdict) string {
	switch v {
	case model.SizingUnder:
		return m.theme.Error.Render("✗ under")
	case model.SizingOver:
		return m.theme.Warning.Render("! over")
	case model.SizingNoRequest:
		return m.theme.Warning.Render("! no req")
	case model.SizingOK:
		return m.theme.Ok.Render("✓ ok")
	default:
		return m.theme.Faint.Render("· no data")
	}
}

// sizingGauge shows the observed peak against the request (or the limit when
// no request is set) — the one-glance "is it sized right" visual.
func (m Model) sizingGauge(rs model.ResourceSizing, width int) string {
	den := rs.Request
	if den <= 0 {
		den = rs.Limit
	}
	if !rs.HasData || den <= 0 {
		return m.theme.Faint.Render(strings.Repeat("·", width))
	}
	return m.coloredGauge(rs.Peak/den, width)
}

// usageColumn is one column of the usage table ('u').
type usageColumn struct {
	title string
	width int // 0 = flexible
	right bool
	cell  func(m *Model, r model.UsageRow) string
	less  func(a, b model.UsageRow) bool
}

// usageColumns defines the table: CPU and memory side by side — no metric
// toggle (consistency, owner 2026-07-09). Gauges are relative to the top
// consumer of each column.
func (m *Model) usageColumns() []usageColumn {
	var maxCPU, maxMem float64
	for _, r := range m.usageAllRows {
		if r.CPU > maxCPU {
			maxCPU = r.CPU
		}
		if r.Mem > maxMem {
			maxMem = r.Mem
		}
	}
	relGauge := func(v float64, has bool, max float64) string {
		if !has || max <= 0 {
			return m.theme.Faint.Render(strings.Repeat("·", 12))
		}
		return m.coloredGauge(v/max, 12)
	}
	valOrDash := func(v float64, has bool, format func(float64) string) string {
		if !has {
			return "—"
		}
		return format(v)
	}
	cols := []usageColumn{
		{title: "NAME", width: 0,
			cell: func(m *Model, r model.UsageRow) string {
				if m.client != nil && (m.client.Namespace == "" || kube.IsNamespacePattern(m.client.Namespace)) {
					return r.Namespace + "/" + r.Name
				}
				return r.Name
			},
			less: func(a, b model.UsageRow) bool { return a.Namespace+a.Name < b.Namespace+b.Name }},
	}
	if m.usageIsAgg {
		cols = append(cols, usageColumn{title: "PODS", width: 4, right: true,
			cell: func(_ *Model, r model.UsageRow) string { return fmt.Sprintf("%d", r.Pods) },
			less: func(a, b model.UsageRow) bool { return a.Pods < b.Pods }})
	}
	cols = append(cols,
		usageColumn{title: "CPU", width: 9, right: true,
			cell: func(_ *Model, r model.UsageRow) string { return valOrDash(r.CPU, r.HasCPU, components.FormatCPU) },
			less: func(a, b model.UsageRow) bool { return a.CPU < b.CPU }},
		usageColumn{title: "", width: 14,
			cell: func(m *Model, r model.UsageRow) string { return relGauge(r.CPU, r.HasCPU, maxCPU) },
			less: func(a, b model.UsageRow) bool { return a.CPU < b.CPU }},
		usageColumn{title: "MEMORY", width: 9, right: true,
			cell: func(_ *Model, r model.UsageRow) string { return valOrDash(r.Mem, r.HasMem, components.FormatMemory) },
			less: func(a, b model.UsageRow) bool { return a.Mem < b.Mem }},
		usageColumn{title: "", width: 14,
			cell: func(m *Model, r model.UsageRow) string { return relGauge(r.Mem, r.HasMem, maxMem) },
			less: func(a, b model.UsageRow) bool { return a.Mem < b.Mem }},
	)
	return cols
}

// applyUsageFilter rebuilds the visible rows (name/namespace substring),
// then re-applies the sort.
func (m *Model) applyUsageFilter() {
	q := strings.ToLower(strings.TrimSpace(m.usageFilterQ))
	rows := make([]model.UsageRow, 0, len(m.usageAllRows))
	for _, r := range m.usageAllRows {
		if q != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Name), q) {
			continue
		}
		rows = append(rows, r)
	}
	m.usageRows = rows
	m.usageWin.SetRows(make([]table.Row, len(rows)))
	m.applyUsageSort()
}

// applyUsageSort: selected column order, or CPU-descending by default (the
// hottest consumers first).
func (m *Model) applyUsageSort() {
	cols := m.usageColumns()
	less := func(a, b model.UsageRow) bool { return a.CPU > b.CPU }
	if m.usageSortCol >= 0 && m.usageSortCol < len(cols) {
		l := cols[m.usageSortCol].less
		if m.usageSortAsc {
			less = l
		} else {
			less = func(a, b model.UsageRow) bool { return l(b, a) }
		}
	}
	sort.SliceStable(m.usageRows, func(i, j int) bool { return less(m.usageRows[i], m.usageRows[j]) })
	m.usageWin.Home()
}

// usageWidths / usageColumnAt mirror the sizing table geometry helpers.
func (m *Model) usageWidths(cols []usageColumn) []int {
	widths := make([]int, len(cols))
	fixed := 0
	flexIdx := -1
	for i, c := range cols {
		w := c.width
		if w == 0 {
			flexIdx, w = i, 20
		}
		widths[i] = w
		fixed += w
	}
	if flexIdx >= 0 {
		if extra := m.width - fixed - len(cols); extra > 0 {
			widths[flexIdx] += extra
		}
	}
	return widths
}

func (m *Model) usageColumnAt(x int) (int, bool) {
	widths := m.usageWidths(m.usageColumns())
	pos := 0
	for i, w := range widths {
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w + 1
	}
	return 0, false
}

// usageListView renders the table in the house style.
func (m Model) usageListView() string {
	cols := m.usageColumns()
	widths := m.usageWidths(cols)
	var b strings.Builder
	head := ""
	for i, c := range cols {
		title := c.title
		if m.usageSortCol == i {
			if m.usageSortAsc {
				title += " ↑"
			} else {
				title += " ↓"
			}
		}
		cell := padTo(title, widths[i])
		if c.right {
			cell = padLeft(title, widths[i])
		}
		head += cell + " "
	}
	b.WriteString(m.theme.TableHeader.Render(padTo(head, m.width)))
	b.WriteString("\n")
	if len(m.usageRows) == 0 {
		b.WriteString(m.theme.Faint.Render(" no data — Prometheus unreachable or no samples (nothing is estimated)"))
		b.WriteString("\n")
	}
	from := m.usageWin.win
	to := from + m.usageWin.height
	if to > len(m.usageRows) {
		to = len(m.usageRows)
	}
	for i := from; i < to; i++ {
		r := m.usageRows[i]
		line := ""
		for j, c := range cols {
			raw := c.cell(&m, r)
			var cell string
			switch {
			case c.right:
				cell = padLeft(raw, widths[j])
			case strings.Contains(raw, "\x1b"):
				cell = padTo2(raw, widths[j])
			default:
				cell = padTo(raw, widths[j])
			}
			line += cell + " "
		}
		if i == m.usageWin.cursor {
			line = m.theme.TableSelected.Render(padTo2(line, m.width))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := to - from; i < m.usageWin.height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// sizingColumn is one column of the sizing overview table.
type sizingColumn struct {
	title string
	width int // 0 = flexible (WORKLOAD)
	right bool
	cell  func(m *Model, a model.SizingAdvice) string // may carry ANSI styling
	less  func(a, b model.SizingAdvice) bool
}

// sizingColumns defines the overview: separate, aligned AVG/REQ columns and a
// titled STATUS per resource — every column is sortable ('s'/'S' or a header
// click; default order is severity, worst first).
func (m *Model) sizingColumns() []sizingColumn {
	sevRank := func(v model.SizingVerdict) int {
		return adviceSeverity(model.SizingAdvice{CPU: model.ResourceSizing{Verdict: v}})
	}
	util := func(rs model.ResourceSizing) float64 {
		den := rs.Request
		if den <= 0 {
			den = rs.Limit
		}
		if !rs.HasData || den <= 0 {
			return -1 // unknown sorts last in asc
		}
		return rs.Peak / den
	}
	avgOrDash := func(rs model.ResourceSizing, format func(float64) string) string {
		if !rs.HasData {
			return "—"
		}
		return format(rs.Avg)
	}
	reqOrDash := func(rs model.ResourceSizing, format func(float64) string) string {
		if rs.Request <= 0 {
			return "—"
		}
		return format(rs.Request)
	}
	return []sizingColumn{
		{title: "WORKLOAD", width: 0,
			cell: func(m *Model, a model.SizingAdvice) string {
				if m.client != nil && (m.client.Namespace == "" || kube.IsNamespacePattern(m.client.Namespace)) {
					return a.Namespace + "/" + strings.TrimPrefix(a.Workload, m.curType.Kind+"/")
				}
				return a.Workload
			},
			less: func(a, b model.SizingAdvice) bool {
				return a.Namespace+a.Workload < b.Namespace+b.Workload
			}},
		{title: "PODS", width: 4, right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return fmt.Sprintf("%d", a.Pods) },
			less: func(a, b model.SizingAdvice) bool { return a.Pods < b.Pods }},
		{title: "CPU", width: 12,
			cell: func(m *Model, a model.SizingAdvice) string { return m.sizingGauge(a.CPU, 10) },
			less: func(a, b model.SizingAdvice) bool { return util(a.CPU) < util(b.CPU) }},
		{title: "AVG", width: 8, right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return avgOrDash(a.CPU, components.FormatCPU) },
			less: func(a, b model.SizingAdvice) bool { return a.CPU.Avg < b.CPU.Avg }},
		{title: "REQ", width: 8, right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return reqOrDash(a.CPU, components.FormatCPU) },
			less: func(a, b model.SizingAdvice) bool { return a.CPU.Request < b.CPU.Request }},
		{title: "STATUS", width: 10,
			cell: func(m *Model, a model.SizingAdvice) string { return m.verdictBadge(a.CPU.Verdict) },
			less: func(a, b model.SizingAdvice) bool { return sevRank(a.CPU.Verdict) < sevRank(b.CPU.Verdict) }},
		{title: "MEMORY", width: 12,
			cell: func(m *Model, a model.SizingAdvice) string { return m.sizingGauge(a.Memory, 10) },
			less: func(a, b model.SizingAdvice) bool { return util(a.Memory) < util(b.Memory) }},
		{title: "AVG", width: 8, right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return avgOrDash(a.Memory, components.FormatMemory) },
			less: func(a, b model.SizingAdvice) bool { return a.Memory.Avg < b.Memory.Avg }},
		{title: "REQ", width: 8, right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return reqOrDash(a.Memory, components.FormatMemory) },
			less: func(a, b model.SizingAdvice) bool { return a.Memory.Request < b.Memory.Request }},
		{title: "STATUS", width: 10,
			cell: func(m *Model, a model.SizingAdvice) string { return m.verdictBadge(a.Memory.Verdict) },
			less: func(a, b model.SizingAdvice) bool { return sevRank(a.Memory.Verdict) < sevRank(b.Memory.Verdict) }},
	}
}

// sizingWidths resolves the overview widths (WORKLOAD absorbs the rest).
func (m *Model) sizingWidths(cols []sizingColumn) []int {
	widths := make([]int, len(cols))
	fixed := 0
	flexIdx := -1
	for i, c := range cols {
		w := c.width
		if w == 0 {
			flexIdx, w = i, 14
		}
		widths[i] = w
		fixed += w
	}
	if flexIdx >= 0 {
		if extra := m.width - fixed - len(cols); extra > 0 {
			widths[flexIdx] += extra
		}
	}
	return widths
}

// sizingColumnAt maps a header click to a column index.
func (m *Model) sizingColumnAt(x int) (int, bool) {
	widths := m.sizingWidths(m.sizingColumns())
	pos := 0
	for i, w := range widths {
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w + 1
	}
	return 0, false
}

// applySizingFilter rebuilds the visible overview rows from the master set
// (workload name/namespace substring), then re-applies the sort.
func (m *Model) applySizingFilter() {
	q := strings.ToLower(strings.TrimSpace(m.sizingQuery))
	rows := make([]model.SizingAdvice, 0, len(m.sizingAllRows))
	objs := make([]model.ResourceObject, 0, len(m.sizingAllObjs))
	for i, r := range m.sizingAllRows {
		if q != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Workload), q) {
			continue
		}
		rows = append(rows, r)
		objs = append(objs, m.sizingAllObjs[i])
	}
	m.sizingRows, m.sizingObjs = rows, objs
	m.sizingWin.SetRows(make([]table.Row, len(rows)))
	m.applySizingSort()
}

// applySizingSort reorders rows and their objects together: the selected
// column's order, or severity worst-first when no column is selected.
func (m *Model) applySizingSort() {
	cols := m.sizingColumns()
	idx := make([]int, len(m.sizingRows))
	for i := range idx {
		idx[i] = i
	}
	less := func(a, b int) bool {
		return adviceSeverity(m.sizingRows[a]) > adviceSeverity(m.sizingRows[b])
	}
	if m.sizingSortCol >= 0 && m.sizingSortCol < len(cols) {
		l := cols[m.sizingSortCol].less
		less = func(a, b int) bool {
			if m.sizingSortAsc {
				return l(m.sizingRows[a], m.sizingRows[b])
			}
			return l(m.sizingRows[b], m.sizingRows[a])
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return less(idx[a], idx[b]) })
	rows := make([]model.SizingAdvice, len(idx))
	objs := make([]model.ResourceObject, len(idx))
	for to, from := range idx {
		rows[to], objs[to] = m.sizingRows[from], m.sizingObjs[from]
	}
	m.sizingRows, m.sizingObjs = rows, objs
	m.sizingWin.Home()
}

// sizingListView renders the overview with real, aligned columns.
func (m Model) sizingListView() string {
	cols := m.sizingColumns()
	widths := m.sizingWidths(cols)

	var b strings.Builder
	head := ""
	for i, c := range cols {
		title := c.title
		if m.sizingSortCol == i {
			if m.sizingSortAsc {
				title += " ↑"
			} else {
				title += " ↓"
			}
		}
		cell := padTo(title, widths[i])
		if c.right {
			cell = padLeft(title, widths[i])
		}
		head += cell + " "
	}
	b.WriteString(m.theme.TableHeader.Render(padTo(head, m.width)))
	b.WriteString("\n")
	if len(m.sizingRows) == 0 {
		b.WriteString(m.theme.Faint.Render(" observing… (advisory, read-only — nothing is applied)"))
		b.WriteString("\n")
	}
	from := m.sizingWin.win
	to := from + m.sizingWin.height
	if to > len(m.sizingRows) {
		to = len(m.sizingRows)
	}
	for i := from; i < to; i++ {
		r := m.sizingRows[i]
		line := ""
		for j, c := range cols {
			raw := c.cell(&m, r)
			var cell string
			switch {
			case c.right:
				cell = padLeft(raw, widths[j])
			case strings.Contains(raw, "\x1b"):
				cell = padTo2(raw, widths[j]) // styled: pad without truncating
			default:
				cell = padTo(raw, widths[j])
			}
			line += cell + " "
		}
		if i == m.sizingWin.cursor {
			line = m.theme.TableSelected.Render(padTo2(line, m.width))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := to - from; i < m.sizingWin.height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// wrapTo hard-wraps text to a display width (rune-counted, word-aware) so a
// viewport line can never auto-wrap in the terminal — auto-wrap shifts every
// line below and breaks the click geometry. Unbreakable tokens are cut.
func wrapTo(s string, w int) []string {
	if w < 10 {
		w = 10
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			for len([]rune(word)) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				r := []rune(word)
				out = append(out, string(r[:w]))
				word = string(r[w:])
			}
			switch {
			case line == "":
				line = word
			case len([]rune(line))+1+len([]rune(word)) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// padLeft right-aligns a plain string in a fixed width (rune-counted).
func padLeft(s string, w int) string {
	s = truncate(s, w)
	if pad := w - len([]rune(s)); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// padTo2 pads a styled (ANSI) string to a display width without truncating.
func padTo2(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// renderSizing renders the advisory view: observed data first, verdict after —
// a recommendation is never shown without the numbers behind it (FR-023).
func (m *Model) renderSizing(a model.SizingAdvice) {
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Sizing (advisory) — %s in %s, %d pod(s), last 1h", a.Workload, a.Namespace, a.Pods)) + "\n\n")
	b.WriteString(m.theme.Faint.Render("Advisory only, based on observed usage per pod — this tool never applies changes.") + "\n\n")
	m.renderResourceSizing(&b, "CPU", a.CPU, components.FormatCPU)
	b.WriteString("\n")
	m.renderResourceSizing(&b, "MEMORY", a.Memory, components.FormatMemory)
	m.setContent(screenSizing, b.String())
	m.layout()
}

func (m *Model) renderResourceSizing(b *strings.Builder, label string, rs model.ResourceSizing, format func(float64) string) {
	b.WriteString(m.rule(label) + "\n")
	orUnset := func(v float64) string {
		if v <= 0 {
			return "—"
		}
		return format(v)
	}
	fmt.Fprintf(b, "  configured  request %s   limit %s   (per pod)\n", orUnset(rs.Request), orUnset(rs.Limit))
	// Reference for the gauges: the request (what sizing is about), else the
	// limit. Colored bars make over/under readable at a glance (FR-036).
	den, ref := rs.Request, "request"
	if den <= 0 {
		den, ref = rs.Limit, "limit"
	}
	switch {
	case !rs.HasData:
		b.WriteString("  observed    no data for this window\n")
	case den > 0:
		fmt.Fprintf(b, "  avg   %s %s   %.0f%% of %s\n", m.coloredGauge(rs.Avg/den, 16), padTo(format(rs.Avg), 9), rs.Avg/den*100, ref)
		fmt.Fprintf(b, "  peak  %s %s   %.0f%% of %s\n", m.coloredGauge(rs.Peak/den, 16), padTo(format(rs.Peak), 9), rs.Peak/den*100, ref)
	default:
		fmt.Fprintf(b, "  observed    avg %s   peak %s   (nothing configured to compare against)\n", format(rs.Avg), format(rs.Peak))
	}
	pct := func(v, of float64) string { return fmt.Sprintf("%.0f%%", v/of*100) }
	var verdict string
	switch rs.Verdict {
	case model.SizingNoData:
		verdict = m.theme.Faint.Render("— no recommendation: not enough observed data (nothing is estimated)")
	case model.SizingNoRequest:
		verdict = m.theme.Warning.Render("! no request configured — consider setting one near the observed peak " + format(rs.Peak))
	case model.SizingUnder:
		reason := "average " + format(rs.Avg) + " ≥ request " + format(rs.Request)
		if rs.Limit > 0 && rs.Peak >= model.SizingLimitFrac*rs.Limit {
			reason = "peak " + format(rs.Peak) + " = " + pct(rs.Peak, rs.Limit) + " of the limit (OOM/throttling risk)"
		}
		verdict = m.theme.Error.Render("✗ under-provisioned / at risk: " + reason)
	case model.SizingOver:
		verdict = m.theme.Warning.Render("! requests appear oversized: peak " + format(rs.Peak) + " = " + pct(rs.Peak, rs.Request) + " of the request")
	default:
		verdict = m.theme.Ok.Render("✓ sized correctly: peak " + format(rs.Peak) + " = " + pct(rs.Peak, rs.Request) + " of the request")
	}
	b.WriteString("  " + verdict + "\n")
}

// ---- Customizable views (US8, FR-024/FR-025) ----

// applyViewPref restores the current type's saved customization: committed
// filter and sort (resolved by column title; a title that no longer matches
// any column is silently ignored — FR-025 tolerance).
func (m *Model) applyViewPref() {
	pref := m.cfg.ViewPrefs[m.curType.Key()]
	m.filter.SetValue(pref.Filter)
	m.sortCol, m.sortAsc = -1, true
	if pref.SortCol != "" {
		for i, c := range m.columnsForType() {
			if c.title == pref.SortCol {
				m.sortCol, m.sortAsc = i+1, pref.SortAsc
				break
			}
		}
	}
}

// persistViewPref snapshots the current sort and committed filter into the
// type's pref — what you see (header chip, sort arrow) is what comes back
// next time. An all-default pref is removed rather than stored.
func (m *Model) persistViewPref() {
	key := m.curType.Key()
	if key == "" || key == "/" {
		return
	}
	pref := m.cfg.ViewPrefs[key]
	cols := m.columnsForType()
	pref.SortCol, pref.SortAsc = "", false
	if m.sortCol >= 1 && m.sortCol <= len(cols) {
		pref.SortCol, pref.SortAsc = cols[m.sortCol-1].title, m.sortAsc
	}
	pref.Filter = strings.TrimSpace(m.filter.Value())
	if len(pref.Columns) == 0 && pref.SortCol == "" && pref.Filter == "" {
		delete(m.cfg.ViewPrefs, key)
	} else {
		if m.cfg.ViewPrefs == nil {
			m.cfg.ViewPrefs = map[string]config.ViewPref{}
		}
		m.cfg.ViewPrefs[key] = pref
	}
	m.persist()
}

// openColumnChooser opens the 'C' modal: every column the current type offers,
// visible ones first in display order, then the hidden ones.
func (m Model) openColumnChooser() (tea.Model, tea.Cmd) {
	base := m.columnsBase()
	baseTitles := map[string]bool{}
	for _, c := range base {
		baseTitles[c.title] = true
	}
	// Shown columns, in order — custom ones keep their stored "label:"/
	// "field:" spec so they round-trip through the prefs unchanged.
	var items []colItem
	on := map[string]bool{}
	if pref := m.cfg.ViewPrefs[m.curType.Key()]; len(pref.Columns) > 0 {
		for _, spec := range pref.Columns {
			if baseTitles[spec] || strings.HasPrefix(spec, "label:") || strings.HasPrefix(spec, "field:") {
				items = append(items, colItem{title: spec, on: true})
				on[spec] = true
			}
		}
	}
	if len(items) == 0 {
		for _, c := range base {
			items = append(items, colItem{title: c.title, on: true})
			on[c.title] = true
		}
	}
	for _, c := range base {
		if !on[c.title] {
			items = append(items, colItem{title: c.title})
		}
	}
	items = append(items, colItem{title: addFieldLabel})
	m.colItems = items
	m.pickerKind = pickColumns
	m.pickerReturn = screenList
	m.pickerQuery = ""
	// The bubbles table panics on SetRows without columns — always set them.
	m.picker.SetColumns([]table.Column{{Title: "columns", Width: max(20, m.width-4)}})
	m.applyColumnRows()
	m.pickerWin.Home()
	m.screen = screenPicker
	m.layout()
	return m, nil
}

// applyColumnRows re-renders the chooser rows from colItems.
func (m *Model) applyColumnRows() {
	rows := make([]table.Row, 0, len(m.colItems))
	for _, it := range m.colItems {
		chk := "· "
		if it.on {
			chk = "✓ "
		}
		label := it.title
		if isCustomSpec(it.title) {
			// Shown like a built-in column, the spec kept as a reminder.
			label = customTitle(it.title) + "  (" + it.title + ")"
		}
		rows = append(rows, table.Row{chk + label})
	}
	m.pickerWin.SetRows(rows)
	m.pickerWin.Sync(&m.picker)
}

// toggleColItem flips a column's visibility (NAME stays — every other action
// in the list needs a way to identify the row).
func (m *Model) toggleColItem(i int) {
	if i < 0 || i >= len(m.colItems) {
		return
	}
	if m.colItems[i].title == addFieldLabel {
		m.fieldNaming, m.fieldInput = true, ""
		return
	}
	if m.colItems[i].title == "NAME" && m.colItems[i].on {
		m.statusMsg = "the NAME column cannot be hidden"
		return
	}
	m.colItems[i].on = !m.colItems[i].on
	m.applyColumnRows()
}

// addCustomColumn inserts a user-defined column into the chooser: a leading
// '.' means an object field path, anything else a label key.
func (m *Model) addCustomColumn(spec string) {
	if spec == "" {
		return
	}
	// Normalize: "metadata.labels.<key>" (with or without a leading dot) is
	// what people naturally type for a label — turn it into a label column,
	// where dotted keys like app.kubernetes.io/version just work.
	core := strings.TrimPrefix(spec, ".")
	if k, ok := strings.CutPrefix(core, "metadata.labels."); ok && k != "" {
		spec = "label:" + k
	} else if strings.HasPrefix(spec, ".") {
		spec = "field:" + spec
	} else {
		spec = "label:" + strings.TrimPrefix(spec, "label:")
	}
	for i := range m.colItems {
		if m.colItems[i].title == spec {
			m.colItems[i].on = true
			m.applyColumnRows()
			return
		}
	}
	// Insert before the trailing "add custom field…" action row.
	at := len(m.colItems)
	if at > 0 && m.colItems[at-1].title == addFieldLabel {
		at--
	}
	m.colItems = append(m.colItems[:at], append([]colItem{{title: spec, on: true}}, m.colItems[at:]...)...)
	m.applyColumnRows()
	m.statusMsg = "column added — Enter applies the arrangement"
}

// removeColItem deletes a user-defined column from the chooser (⌫ — the
// typo eraser). Built-in columns can only be hidden, never removed.
func (m *Model) removeColItem(i int) {
	if i < 0 || i >= len(m.colItems) {
		return
	}
	if !isCustomSpec(m.colItems[i].title) {
		if m.colItems[i].title != addFieldLabel {
			m.statusMsg = "built-in columns can only be hidden (Space) — ⌫ removes custom fields"
		}
		return
	}
	m.colItems = append(m.colItems[:i], m.colItems[i+1:]...)
	m.applyColumnRows()
	m.statusMsg = "custom field removed — Enter applies"
}

// applyColumnChoice commits the chooser (Enter): store the arrangement — or
// clear it when it matches the type's default — re-resolve the sort against
// the new columns, and persist.
func (m Model) applyColumnChoice() (tea.Model, tea.Cmd) {
	var sortTitle string
	if cols := m.columnsForType(); m.sortCol >= 1 && m.sortCol <= len(cols) {
		sortTitle = cols[m.sortCol-1].title
	}
	titles := make([]string, 0, len(m.colItems))
	for _, it := range m.colItems {
		if it.on {
			titles = append(titles, it.title)
		}
	}
	base := m.columnsBase()
	def := len(titles) == len(base)
	if def {
		for i, c := range base {
			if titles[i] != c.title {
				def = false
				break
			}
		}
	}
	key := m.curType.Key()
	if m.cfg.ViewPrefs == nil {
		m.cfg.ViewPrefs = map[string]config.ViewPref{}
	}
	pref := m.cfg.ViewPrefs[key]
	pref.Columns = titles
	if def {
		pref.Columns = nil
	}
	m.cfg.ViewPrefs[key] = pref
	m.screen = screenList
	m.layout()
	// The sorted column may have been hidden or moved: follow it by title.
	m.sortCol = -1
	if sortTitle != "" {
		for i, c := range m.columnsForType() {
			if c.title == sortTitle {
				m.sortCol = i + 1
				break
			}
		}
	}
	m.applyRows()
	m.persistViewPref()
	return m, nil
}

// openViewPicker opens the 'V' modal: saved views plus save/reset actions.
func (m Model) openViewPicker() (tea.Model, tea.Cmd) {
	opts := []string{saveViewLabel, resetViewLabel}
	for _, v := range m.cfg.SavedViews {
		opts = append(opts, viewOptionLabel(v))
	}
	m.pickerKind = pickView
	m.pickerReturn = m.screen
	m.pickerOpts = opts
	m.pickerQuery = ""
	// The bubbles table panics on SetRows without columns — always set them.
	m.picker.SetColumns([]table.Column{{Title: "views", Width: max(20, m.width-4)}})
	m.applyPickerRows()
	m.pickerWin.Home()
	m.pickerWin.Sync(&m.picker)
	m.screen = screenPicker
	m.layout()
	return m, nil
}

// viewOptionLabel renders a saved view as a picker option.
func viewOptionLabel(v config.SavedView) string {
	ns := v.Namespace
	if ns == "" {
		ns = "all ns"
	}
	return v.Name + "  (" + v.Type + ", " + ns + ")"
}

// saveCurrentView stores the whole current arrangement — type, namespace,
// columns, sort, filter — under a name (same name = update).
func (m *Model) saveCurrentView(name string) {
	if name == "" {
		m.statusMsg = "view not saved (empty name)"
		return
	}
	v := config.SavedView{
		Name:      name,
		Type:      m.curType.Key(),
		Namespace: m.client.Namespace,
		Columns:   m.cfg.ViewPrefs[m.curType.Key()].Columns,
		Filter:    strings.TrimSpace(m.filter.Value()),
	}
	if cols := m.columnsForType(); m.sortCol >= 1 && m.sortCol <= len(cols) {
		v.SortCol, v.SortAsc = cols[m.sortCol-1].title, m.sortAsc
	}
	updated := false
	for i := range m.cfg.SavedViews {
		if m.cfg.SavedViews[i].Name == name {
			m.cfg.SavedViews[i] = v
			updated = true
			break
		}
	}
	if !updated {
		m.cfg.SavedViews = append(m.cfg.SavedViews, v)
	}
	m.persist()
	if updated {
		m.statusMsg = "view “" + name + "” updated"
	} else {
		m.statusMsg = "view “" + name + "” saved — 'V' opens it anytime"
	}
}

// applySavedView switches to a saved view: type, namespace, columns, sort,
// filter. The view's arrangement becomes the type's current pref.
func (m Model) applySavedView(v config.SavedView) (tea.Model, tea.Cmd) {
	t, ok := findTypeByKey(m.types, v.Type)
	if !ok {
		m.errMsg = "view " + v.Name + ": type " + v.Type + " not available on this cluster"
		m.screen = m.pickerReturn
		m.layout()
		return m, nil
	}
	m.curType = t
	m.client.Namespace = v.Namespace
	if m.cfg.ViewPrefs == nil {
		m.cfg.ViewPrefs = map[string]config.ViewPref{}
	}
	m.cfg.ViewPrefs[v.Type] = config.ViewPref{Columns: v.Columns, SortCol: v.SortCol, SortAsc: v.SortAsc, Filter: v.Filter}
	m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
	m.marked = map[string]model.ResourceObject{}
	m.applyViewPref()
	m.statusMsg = "view “" + v.Name + "”"
	m.screen = screenList
	m.layout()
	m.persist()
	return m, m.listObjects()
}

// resetCurrentView drops every customization of the current type ('R').
func (m *Model) resetCurrentView() {
	delete(m.cfg.ViewPrefs, m.curType.Key())
	m.filter.SetValue("")
	m.sortCol, m.sortAsc = -1, true
	m.applyRows()
	m.persist()
	m.statusMsg = "view reset to defaults"
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
