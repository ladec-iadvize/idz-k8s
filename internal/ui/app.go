// Package ui implements the Bubble Tea program. For the v1 MVP the US1
// screens (list, detail, logs, pickers) are consolidated here; they can be
// split into internal/ui/views/ later without touching the data layers.
package ui

import (
	"context"
	"fmt"
	"os"
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

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
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
	pickPalette
	pickAction
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

// activeContextSuffix marks the active context in the context picker; the
// "  (…)" annotation form matches the type picker's labels and is stripped
// on selection.
const activeContextSuffix = "  (active)"

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
	detail   viewport.Model
	logsView viewport.Model
	diag     viewport.Model
	topo     viewport.Model
	events   viewport.Model
	filter   textinput.Model

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
	eventsShown     []model.Event // after filters, most recent first (render order)
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

	// Helm release overview (US12).
	helmFiltering  bool   // typing a helm filter (Enter commits, Esc cancels)
	helmQuery      string // committed helm filter (name/namespace/chart)
	helmSortCol    int    // -1 = storage order
	helmSortAsc    bool
	helmTable      table.Model
	helmHist       viewport.Model
	helmRows       []model.HelmRelease
	helmValuesOnly bool // 'v': show only the values of a release

	// Posture overview (US13, advisory).
	posture viewport.Model

	// Per-pod connectivity / NetworkPolicy view (US14).
	connectivity viewport.Model

	// Access (RBAC) view (US15, introspection).
	access viewport.Model

	// Live vs last-applied drift view (US16).
	drift viewport.Model

	// Sizing recommendations (US6, advisory): overview table of
	// every listed workload, Enter → per-workload detail panel.
	sizingVP   viewport.Model
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

	// kikoo: celebratory banner mode (visual only).
	kikoo bool

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

	// Live per-pod usage for the pods list columns (CPU/MEM + % of request),
	// refreshed on the periodic tick — never on the watch bursts, so
	// Prometheus is queried at most once per refresh interval.
	podUsageCPU map[string]float64 // key: ns/name
	podUsageMem map[string]float64

	// Longest content of the list's flexible column (rune-counted), set by
	// applyRows — listWidths caps the flex width with it.
	flexContentW int

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

	// Topology: selectable node headers (Enter drills to the node's pods).
	topoNodes     []model.TopologyNode
	topoSel       int
	topoNodeLines []int

	// The type behind the usage rows (Enter jumps to it).
	usageTypeKey string

	// Failures/posture: selectable findings (Enter jumps to the object),
	// 'w' keeps error-level findings only.
	diagAll        []model.Diagnostic
	diagSel        int
	diagErrOnly    bool
	diagRefs       []objRef
	diagLines      []int // content line of each selectable finding
	postureAll     []model.PostureFinding
	postureSel     int
	postureErrOnly bool
	postureRefs    []objRef
	postureLines   []int

	// Where Esc returns from a describe opened out of an analysis view
	// (failures/posture/events) — screenList means "normal describe".
	describeReturn screen

	// Live refresh (watch-driven): a change signal re-renders the list at
	// most every changeFlushDelay (bursts from a rolling update coalesce).
	changePending bool

	// Sizing overview row filter ('/' on the table, helm-list style).
	sizingFiltering bool
	sizingQuery     string
	sizingAllRows   []model.SizingAdvice
	sizingAllObjs   []model.ResourceObject

	// v3 admin: confirmation modal (EVERY mutation goes through it or a
	// value prompt), the open actions palette, and active port-forwards.
	confirming   bool
	confirmTitle string
	confirmCmd   tea.Cmd
	promptKind   promptKind
	promptTitle  string
	promptInput  string
	promptHint   string
	promptAction func(m *Model, value string) (tea.Model, tea.Cmd)
	actionList   []actionEntry
	actionFor    string // palette title, e.g. "Deployment/back"
	forwards     map[string]*kube.PortForward
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

// WithHelm attaches the Helm release client (US12; v3 rollback/uninstall).
func WithHelm(hc *helm.Client) Option {
	return func(m *Model) { m.helm = hc }
}

// WithTheme applies a resolved theme (dark/light/auto — see theme.ForName).
func WithTheme(t theme.Theme) Option {
	return func(m *Model) { m.theme = t }
}

// WithKikoo enables the celebratory ASCII banner (--kikoo): pure visual
// flair, iAdvize green. The banner is composed OUTSIDE the click geometry
// (prepended after overlays; mouse Y is normalized in one place).
func WithKikoo(on bool) Option {
	return func(m *Model) { m.kikoo = on }
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
		detail:         viewport.New(0, 0),
		logsView:       viewport.New(0, 0),
		diag:           viewport.New(0, 0),
		topo:           viewport.New(0, 0),
		events:         viewport.New(0, 0),
		filter:         fi,
		helmTable:      table.New(table.WithFocused(true)),
		helmHist:       viewport.New(0, 0),
		sizingVP:       viewport.New(0, 0),
		posture:        viewport.New(0, 0),
		connectivity:   viewport.New(0, 0),
		access:         viewport.New(0, 0),
		drift:          viewport.New(0, 0),
		screen:         screenList,
		marked:         map[string]model.ResourceObject{},
		forwards:       map[string]*kube.PortForward{},
		sortCol:        -1,
		sizingSortCol:  -1,
		usageSortCol:   -1,
		helmSortCol:    -1,
		sortAsc:        true,
	}
	// Table look & feel: colored headers, background-highlighted selection.
	// Purely visual — row geometry is unchanged (mouse mapping intact).
	ts := table.DefaultStyles()
	ts.Header = m.theme.TableHeader.BorderStyle(lipgloss.NormalBorder()).BorderBottom(false)
	ts.Selected = m.theme.TableSelected
	ts.Cell = lipgloss.NewStyle()
	m.helmTable.SetStyles(ts)

	for _, opt := range opts {
		opt(&m)
	}
	// Help overlay: bubbles/help defaults are faint-on-dark and nearly
	// invisible (owner report 2026-07-12) — style it from the theme AFTER
	// the options so a WithTheme choice applies.
	hs := m.help.Styles
	hs.ShortKey, hs.FullKey = m.theme.HelpKey, m.theme.HelpKey
	hs.ShortDesc, hs.FullDesc = m.theme.HelpDesc, m.theme.HelpDesc
	hs.ShortSeparator, hs.FullSeparator = m.theme.Faint, m.theme.Faint
	m.help.Styles = hs
	return m
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

	case nsOptionsMsg:
		// Stale by the time it lands? (picker closed or repurposed) Drop it.
		if m.screen != screenPicker || m.pickerKind != pickNamespace {
			return m, nil
		}
		if msg.err != nil {
			m.errMsg = "listing namespaces: " + msg.err.Error()
			return m, nil // keep the best-effort options already shown
		}
		opts := append([]string{}, msg.opts...)
		sort.Strings(opts)
		m.pickerOpts = append([]string{allNamespacesLabel}, opts...)
		m.applyPickerRows()
		return m, nil

	case objectsMsg:
		if msg.err != nil {
			if kube.IsForbidden(msg.err) {
				// RBAC denial: an access problem, not a lost connection —
				// name the type and point to the access view (FR-032).
				m.errMsg = "forbidden: your credentials cannot list " + m.curType.Key() + " — the access view ('>') shows your rights"
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
			if strings.EqualFold(m.curType.Kind, "Pod") && m.metrics.Enabled() {
				cmds = append(cmds, m.fetchListUsage())
			}
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
				line = theme.PodPrefix(msg.Pod).Render("["+msg.Pod+"]") + " " + msg.Text
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

	case podUsageMsg:
		m.podUsageCPU, m.podUsageMem = msg.cpu, msg.mem
		if m.screen == screenList && strings.EqualFold(m.curType.Kind, "Pod") {
			m.applyRows()
		}
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

	case describeRefMsg:
		if m.screen == screenDetail {
			m.detailObj = msg.obj
			m.detailNS, m.detailName = msg.obj.Namespace, msg.obj.Name
			m.setDetailContent(describeContent(msg.obj, msg.events, m.theme, m.width))
		}
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

	case adminMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.statusMsg = "✓ " + msg.summary
		// Refresh whatever the mutation touched.
		switch m.screen {
		case screenHelm, screenHelmHist:
			return m, m.fetchHelmRows()
		default:
			return m, m.listObjects()
		}

	case editOpenMsg:
		if msg.err != nil {
			m.errMsg = "edit: " + msg.err.Error()
			return m, nil
		}
		// Hand the terminal to the editor; Bubble Tea restores the screen
		// when the process exits.
		closed := editorClosedMsg{path: msg.path, original: msg.original, t: msg.t, ns: msg.ns, name: msg.name}
		return m, tea.ExecProcess(editorCommand(msg.path), func(err error) tea.Msg {
			closed.err = err
			return closed
		})

	case editorClosedMsg:
		if msg.err != nil {
			_ = os.Remove(msg.path)
			m.errMsg = "editor: " + msg.err.Error()
			return m, nil
		}
		m.statusMsg = "⏳ applying " + msg.t.Kind + "/" + msg.name
		return m, m.applyEdit(msg)

	case forwardMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		// Replace any previous tunnel to the same pod+port.
		if old, ok := m.forwards[msg.fw.Key()]; ok {
			old.Stop()
		}
		m.forwards[msg.fw.Key()] = msg.fw
		m.errMsg = ""
		m.statusMsg = "⇄ " + msg.fw.Label() + " — stop it from the actions palette ('a')"
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)
	}

	// Delegate to the focused component.
	return m.delegate(msg)
}

// editAction is editText's verdict on one key of a text-typing mode.
type editAction int

const (
	editTyping editAction = iota // text possibly changed, stay in the mode
	editCommit                   // Enter: leave the mode, keep the text
	editCancel                   // Esc: leave the mode (callers clear the text)
)

// editText applies one key to a typing-mode buffer: Backspace deletes the
// last RUNE (never a byte — 'é' is 2 bytes, FR "widths in runes" applies to
// input too), runes/space append. Every typing mode shares this so the
// Enter-commits / Esc-cancels contract stays consistent (invariant 0).
func editText(s *string, msg tea.KeyMsg) (act editAction, changed bool) {
	switch msg.Type {
	case tea.KeyEnter:
		return editCommit, false
	case tea.KeyEscape:
		return editCancel, false
	case tea.KeyBackspace:
		if *s != "" {
			r := []rune(*s)
			*s = string(r[:len(r)-1])
			return editTyping, true
		}
	case tea.KeyRunes, tea.KeySpace:
		*s += string(msg.Runes)
		return editTyping, true
	}
	return editTyping, false
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Typing modes capture ALL keys BEFORE the global shortcuts (typing "q"
	// or "m" must never quit the app or toggle the mouse).

	// Confirmation modal (v3 admin): Enter runs, Esc cancels, nothing leaks.
	if m.confirming {
		return m.handleConfirmKey(msg)
	}

	// Value prompt (scale replicas, port-forward ports).
	if m.promptKind != promptNone {
		return m.handlePromptKey(msg)
	}

	// View-name typing mode (Enter saves, Esc cancels).
	if m.viewNaming {
		switch act, _ := editText(&m.viewName, msg); act {
		case editCommit:
			m.viewNaming = false
			m.saveCurrentView(strings.TrimSpace(m.viewName))
		case editCancel:
			m.viewNaming = false
		}
		return m, nil
	}

	// Custom-column typing mode (Enter adds, Esc cancels).
	if m.fieldNaming {
		switch act, _ := editText(&m.fieldInput, msg); act {
		case editCommit:
			m.fieldNaming = false
			m.addCustomColumn(strings.TrimSpace(m.fieldInput))
		case editCancel:
			m.fieldNaming = false
		}
		return m, nil
	}

	// Viewport search typing mode (Enter searches, Esc cancels).
	if m.searchTyping {
		switch act, _ := editText(&m.searchInput, msg); act {
		case editCommit:
			m.searchTyping = false
			m.searchQuery = strings.TrimSpace(m.searchInput)
			m.searchScreen = m.screen
			m.applySearch(true)
		case editCancel:
			m.searchTyping = false
			m.searchInput = ""
		}
		return m, nil
	}

	// Events filter typing mode: Enter commits (query saved), Esc cancels.
	if m.screen == screenEvents && m.eventsFiltering {
		act, changed := editText(&m.eventsQuery, msg)
		switch act {
		case editCommit:
			m.eventsFiltering = false
		case editCancel:
			m.eventsFiltering = false
			m.eventsQuery = ""
		}
		if changed || act != editTyping {
			m.renderEvents()
		}
		return m, nil
	}

	// Helm filter typing mode (query visible as a chip).
	if m.screen == screenHelm && m.helmFiltering {
		act, changed := editText(&m.helmQuery, msg)
		switch act {
		case editCommit:
			m.helmFiltering = false
		case editCancel:
			m.helmFiltering = false
			m.helmQuery = ""
		}
		if changed || act != editTyping {
			m.renderHelm()
		}
		return m, nil
	}

	// Sizing-overview filter typing mode.
	if m.screen == screenSizingList && m.sizingFiltering {
		act, changed := editText(&m.sizingQuery, msg)
		switch act {
		case editCommit:
			m.sizingFiltering = false
		case editCancel:
			m.sizingFiltering = false
			m.sizingQuery = ""
		}
		if changed || act != editTyping {
			m.applySizingFilter()
		}
		return m, nil
	}

	// Usage-view filter typing mode.
	if m.screen == screenTop && m.usageTyping {
		act, changed := editText(&m.usageFilterQ, msg)
		switch act {
		case editCommit:
			m.usageTyping = false
		case editCancel:
			m.usageTyping = false
			m.usageFilterQ = ""
		}
		if changed || act != editTyping {
			m.applyUsageFilter()
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

	// Filtering mode captures typing. Enter commits the filter (kept as a
	// header chip); Esc cancels and clears it — same contract as every other
	// filter mode (invariant 0: Esc means clear, never commit).
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			m.applyRows()
			m.persistViewPref()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
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
		m.stopAllForwards()
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
	case hit(msg, m.keys.Palette):
		// The views palette replaces every per-view shortcut (owner
		// decision 2026-07-12) and opens from anywhere.
		return m.openPalette()
	case hit(msg, m.keys.Jump):
		return m.openPicker(pickType)
	case hit(msg, m.keys.Namespace) && !m.searchNavActive():
		return m.openPicker(pickNamespace)
	case hit(msg, m.keys.Context):
		return m.openPicker(pickContext)
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
		return m.handleFindingsKey(msg, findingsNav{sel: &m.diagSel, refs: m.diagRefs,
			errOnly: &m.diagErrOnly, rerender: m.renderDiagView, vp: &m.diag})
	case screenSizing:
		var cmd tea.Cmd
		m.sizingVP, cmd = m.sizingVP.Update(msg)
		return m, cmd
	case screenPosture:
		return m.handleFindingsKey(msg, findingsNav{sel: &m.postureSel, refs: m.postureRefs,
			errOnly: &m.postureErrOnly, rerender: m.renderPostureView, vp: &m.posture})
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
		switch {
		case hit(msg, m.keys.Open):
			if m.topoSel >= 0 && m.topoSel < len(m.topoNodes) {
				return m.drillNodePods(m.topoNodes[m.topoSel].Name)
			}
			return m, nil
		}
		switch msg.Type {
		case tea.KeyUp:
			if m.topoSel > 0 {
				m.topoSel--
				m.renderTopologyView()
				m.keepFindingVisible(&m.topo, m.topoNodeLines, m.topoSel)
			}
			return m, nil
		case tea.KeyDown:
			if m.topoSel < len(m.topoNodes)-1 {
				m.topoSel++
				m.renderTopologyView()
				m.keepFindingVisible(&m.topo, m.topoNodeLines, m.topoSel)
			}
			return m, nil
		}
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

// Mouse-mapping geometry (CLAUDE.md: "geometry is sacred").
// header(1) + rule(1) + column header(1) → list rows start at y=3;
// sortable table header row is y=2; viewport content starts at y=2.
const (
	headerLines  = 3 // header + rule + column header, above list rows
	tableHeaderY = 2 // y of the clickable column-header row
	viewportTopY = 2 // first content line of full-screen viewports
)

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
			return m, nil
		default:
			return m.delegate(msg)
		}
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m.delegate(msg)
	}
	// kikoo banner: all click geometry below is banner-less — normalize once.
	msg.Y -= m.bannerH()
	if msg.Y < 0 {
		return m, nil // click inside the banner: pure decoration
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
		if msg.Y == tableHeaderY {
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
		if m.win.ClickVisible(msg.Y - headerLines) {
			if doubleClick(m.win.cursor) {
				return m.openListSelection()
			}
		}
		return m, nil
	case screenTop:
		if msg.Y == tableHeaderY {
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
		if m.usageWin.ClickVisible(msg.Y-headerLines) && doubleClick(m.usageWin.cursor) {
			if c := m.usageWin.cursor; c >= 0 && c < len(m.usageRows) {
				r := m.usageRows[c]
				return m.openDescribeRef(objRef{typeKey: m.usageTypeKey, ns: r.Namespace, name: r.Name})
			}
		}
		return m, nil
	case screenDiag:
		if i, ok := findingAt(m.diag.YOffset+msg.Y-viewportTopY, m.diagLines); ok {
			m.diagSel = i
			m.renderDiagView()
			if doubleClick(1000 + i) {
				return m.openDescribeRef(m.diagRefs[i])
			}
		}
		return m, nil
	case screenPosture:
		if i, ok := findingAt(m.posture.YOffset+msg.Y-viewportTopY, m.postureLines); ok {
			m.postureSel = i
			m.renderPostureView()
			if doubleClick(2000 + i) {
				return m.openDescribeRef(m.postureRefs[i])
			}
		}
		return m, nil
	case screenTopology:
		if i, ok := findingAt(m.topo.YOffset+msg.Y-viewportTopY, m.topoNodeLines); ok {
			m.topoSel = i
			m.renderTopologyView()
			if doubleClick(3000 + i) {
				return m.drillNodePods(m.topoNodes[i].Name)
			}
		}
		return m, nil
	case screenSizingList:
		if msg.Y == tableHeaderY {
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
		if m.sizingWin.ClickVisible(msg.Y - headerLines) {
			if doubleClick(m.sizingWin.cursor) {
				return m.openSizingDetail(m.sizingWin.cursor)
			}
		}
		return m, nil
	case screenHelm:
		if msg.Y == tableHeaderY {
			widths := m.helmColWidths()
			pos := 0
			for i, w := range widths {
				if msg.X >= pos && msg.X < pos+w {
					if m.helmSortCol == i {
						m.helmSortAsc = !m.helmSortAsc
					} else {
						m.helmSortCol, m.helmSortAsc = i, true
					}
					m.renderHelm()
					break
				}
				pos += w
			}
			return m, nil
		}
		if m.helmWin.ClickVisible(msg.Y - headerLines) {
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
		contentLine := m.events.YOffset + msg.Y - viewportTopY // y0 header, y1 rule → viewport at y=2
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
	if m.screen == screenDetail && m.describeReturn != screenList && m.describeReturn != screenDetail {
		back := m.describeReturn
		m.describeReturn = screenList
		m.screen = back
		m.layout()
		return m, nil
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

// findingAt maps a viewport content line to the finding rendered there.
func findingAt(contentLine int, lines []int) (int, bool) {
	for i, l := range lines {
		if l == contentLine {
			return i, true
		}
	}
	return 0, false
}

// keepFindingVisible scrolls a findings viewport so the selection shows.
func (m *Model) keepFindingVisible(vp *viewport.Model, lines []int, sel int) {
	if sel < 0 || sel >= len(lines) {
		return
	}
	line := lines[sel]
	if line < vp.YOffset {
		off := line - 2
		if off < 0 {
			off = 0
		}
		vp.SetYOffset(off)
	} else if line >= vp.YOffset+vp.Height {
		vp.SetYOffset(line - vp.Height + 3)
	}
}

// ---- Rendering ----

func (m *Model) layout() {
	headerH := 3 // header + rule
	footerH := 4 // rule + status line + shortcuts line
	if m.help.ShowAll {
		footerH = 8
	}
	bodyH := m.height - headerH - footerH - m.bannerH()
	if bodyH < 3 {
		bodyH = 3
	}
	m.bodyH = bodyH
	m.win.SetHeight(bodyH - 1) // -1: the table's own column header
	m.pickerWin.SetHeight(m.modalListRows())
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
	case m.confirming:
		out = overlayCenter(out, m.confirmModal(), m.width)
	case m.promptKind != promptNone:
		out = overlayCenter(out, m.inputModal(m.promptTitle, m.promptInput, m.promptHint), m.width)
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
	// The banner is prepended AFTER the overlays: modals center within the
	// app area and every click keeps its banner-less coordinates (mouse Y is
	// normalized once in handleMouse).
	if h := m.bannerH(); h > 0 {
		out = m.banner() + out
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
	box := m.theme.ModalBorder.Render(strings.Join(lines, "\n"))

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
	return m.theme.ModalBorder.Render(strings.Join(lines, "\n"))
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
	// Identity chips: app badge + colored ctx/ns/type values —
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
	if n := len(m.forwards); n > 0 {
		// Active port-forwards stay visible from every view (stop them via
		// the actions palette on the forwarded resource).
		line += "  " + m.theme.Warning.Render(fmt.Sprintf("⇄ %d fwd", n))
	}
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
	line, _ := m.footerShort(km)
	if m.width > 0 {
		// Never wrap: a wrapped footer breaks nothing geometrically (it is the
		// last line) but looks broken; truncate cleanly.
		line = xansi.Truncate(line, m.width, "…")
	}
	return line
}

// footerZones recomputes the clickable label ranges of the shortcut bar.
func (m Model) footerZones() []clickZone {
	_, zones := m.footerShort(m.screenKeymap())
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
func (m Model) footerShort(km keymapView) (string, []clickZone) {
	line := ""
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
		line += m.theme.HelpKey.Render(h.Key) + " " + m.theme.HelpDesc.Render(h.Desc)
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

func (k keymapView) ShortHelp() []key.Binding { return k.short }

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
			short: []key.Binding{k.Open, k.Mark, k.Yaml, k.Describe, k.Actions, k.Filter, k.Sort, k.Logs, k.Palette, k.Namespace, k.Context, k.Help, k.Quit},
			full: [][]key.Binding{
				nav,
				{k.Open, k.Mark, k.Yaml, k.Describe, k.Filter, k.Jump, k.Logs},
				{k.Actions, k.Edit, k.Sort, k.SortDir, k.Palette, k.Columns, k.Views, k.ResetView},
				{k.Namespace, k.Context, k.Help, k.Quit},
			},
		}
	case screenHelm:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Values, k.Actions, k.Filter, k.Sort, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Values, k.Actions, k.Filter, k.Sort, k.SortDir, k.Mouse, k.Back, k.Help, k.Quit}},
		}
	case screenEvents:
		return keymapView{
			short: []key.Binding{k.Open, k.Filter, k.Kind, k.WarnOnly, k.Namespace, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Filter, k.Kind, k.WarnOnly, k.Namespace}, {k.Back, k.Help, k.Quit}},
		}
	case screenDiag, screenPosture:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.WarnOnly, k.Filter, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.WarnOnly, k.Filter, k.SearchNext, k.SearchPrev, k.Back, k.Help, k.Quit}},
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
			short: []key.Binding{k.Up, k.Down, k.Open, k.Filter, k.Sort, k.SortDir, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Filter, k.Sort, k.SortDir, k.Back, k.Help, k.Quit}},
		}
	case screenTopology:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Filter, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Filter, k.SearchNext, k.SearchPrev, k.Back, k.Help, k.Quit}},
		}
	case screenSizingList:
		return keymapView{
			short: []key.Binding{k.Up, k.Down, k.Open, k.Filter, k.Sort, k.SortDir, k.Back, k.Quit},
			full:  [][]key.Binding{nav, {k.Open, k.Filter, k.Sort, k.SortDir, k.Back, k.Help, k.Quit}},
		}
	case screenDetail, screenHelmHist:
		short := []key.Binding{k.Up, k.Down, k.Filter, k.SearchNext, k.SearchPrev}
		actions := []key.Binding{k.Filter, k.SearchNext, k.SearchPrev}
		if m.screen == screenDetail && strings.EqualFold(m.curType.Kind, "Secret") {
			// Secret detail: 'x' toggles value reveal (masked by default, FR-015).
			short = append(short, k.Reveal)
			actions = append(actions, k.Reveal)
		}
		short = append(short, k.Back, k.Quit)
		actions = append(actions, k.Back, k.Help, k.Quit)
		return keymapView{
			short: short,
			full:  [][]key.Binding{nav, actions},
		}
	default: // connectivity, access, drift, sizing detail
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

func hit(msg tea.KeyMsg, b key.Binding) bool { return key.Matches(msg, b) }
