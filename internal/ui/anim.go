package ui

// Animations (FR-040, owner request 2026-11-09): a loader in the body of a
// view still fetching, a spinner on the status line while a confirmed
// mutation is in flight, and a live progress bar for the rollouts it
// started. Everything shown is real state — the loader only says "still
// waiting", the bar only draws the counts the API returned.
//
// One frame tick drives them all, and only while something animates: an
// idle app never re-renders on a timer. `animations: off` in the config
// freezes every one of them on a static frame.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

const (
	animInterval = 80 * time.Millisecond
	// busyMax: past this, the in-flight mutation stops animating even if
	// its result never came back (a bulk call is bounded by 2×adminTimeout).
	busyMax = 2*adminTimeout + 5*time.Second
	// rolloutPoll is the GET cadence of a tracked rollout; rolloutMax the
	// point where tracking gives up and says so.
	rolloutPoll = time.Second
	rolloutMax  = 10 * time.Minute
)

type animTickMsg struct{}

type loadingState struct {
	label string
	since time.Time
}

// rolloutTarget names one workload whose rollout a mutation started.
type rolloutTarget struct {
	t        model.ResourceType
	ns, name string
}

func (r rolloutTarget) key() string   { return r.t.Kind + "/" + r.ns + "/" + r.name }
func (r rolloutTarget) label() string { return r.t.Kind + "/" + r.name }

type rolloutTrack struct {
	target  rolloutTarget
	started time.Time
	prog    kube.RolloutProgress
}

// rolloutMsg carries one fresh GET of a tracked workload.
type rolloutMsg struct {
	key  string
	prog kube.RolloutProgress
	ok   bool
	err  error
}

func (m Model) animEnabled() bool { return !strings.EqualFold(m.cfg.Animations, "off") }

func (m Model) busy() bool {
	return m.busyMsg != "" && time.Since(m.busySince) < busyMax
}

// animating reports whether anything on screen moves right now.
func (m Model) animating() bool {
	if !m.animEnabled() {
		return false
	}
	_, loading := m.loading[m.bodyScreen()]
	return loading || m.busy() || len(m.rollouts) > 0
}

// bodyScreen is the screen drawn in the body (a picker overlays the screen
// it was opened from).
func (m Model) bodyScreen() screen {
	if m.screen == screenPicker {
		return m.pickerReturn
	}
	return m.screen
}

func animTick() tea.Cmd {
	return tea.Tick(animInterval, func(time.Time) tea.Msg { return animTickMsg{} })
}

// withAnim starts the frame tick when an update left something animating
// and the tick is not already running. The tick stops itself (animTickMsg)
// once nothing moves.
func withAnim(mi tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	start := func(m *Model) tea.Cmd {
		if m.animOn || !m.animating() {
			return cmd
		}
		m.animOn = true
		return tea.Batch(cmd, animTick())
	}
	switch v := mi.(type) {
	case Model:
		c := start(&v)
		return v, c
	case *Model:
		return v, start(v)
	}
	return mi, cmd
}

func (m Model) handleAnimTick() (tea.Model, tea.Cmd) {
	m.animFrame++
	if !m.animating() {
		m.animOn = false
		return m, nil
	}
	return m, animTick()
}

// ---- view loaders ----

// startLoading marks a view as fetching: its body shows the animated loader
// until the view's result message arrives (loadDone). The caller still sets
// the view's plain placeholder text — it is what shows with animations off.
func (m *Model) startLoading(sc screen, label string) {
	if m.loading == nil {
		m.loading = map[screen]loadingState{}
	}
	m.loading[sc] = loadingState{label: label, since: time.Now()}
}

// loadDone clears the loader of the view a result message belongs to —
// success or error alike, so a failure never leaves a loader spinning.
func (m Model) loadDone(msg tea.Msg) {
	var sc screen
	switch msg.(type) {
	case topologyMsg:
		sc = screenTopology
	case eventsMsg:
		sc = screenEvents
	case helmMsg:
		sc = screenHelm
	case helmDetailMsg:
		sc = screenHelmHist
	case helmResourceMsg:
		sc = screenHelmRes
	case diagMsg:
		sc = screenDiag
	case sizingMsg:
		sc = screenSizing
	case postureMsg:
		sc = screenPosture
	case accessMsg:
		sc = screenAccess
	case connectivityMsg:
		sc = screenConnectivity
	default:
		return
	}
	delete(m.loading, sc)
}

const loaderTrack = 36

// loaderView fills the body with a centered loader: spinner + label, a
// bouncing comet, and the real elapsed time. Exactly m.bodyH lines, so the
// geometry below the body never moves.
func (m Model) loaderView(ls loadingState) string {
	h := max(m.bodyH, 3)
	w := m.width
	track := min(loaderTrack, max(w-4, 1))
	label := strings.TrimSuffix(strings.TrimSpace(ls.label), "…")
	block := []string{
		m.theme.Spinner(m.animFrame) + " " + m.theme.Title.Render(label),
		m.theme.Comet(track, m.animFrame),
		m.theme.Faint.Render(elapsed(time.Since(ls.since))),
	}
	top := max((h-len(block))/2, 0)
	lines := make([]string, h)
	for i := range lines {
		j := i - top
		if j < 0 || j >= len(block) {
			continue
		}
		line := block[j]
		if w > 0 {
			pad := max((w-xansi.StringWidth(line))/2, 0)
			line = xansi.Truncate(strings.Repeat(" ", pad)+line, w, "…")
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func elapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// ---- in-flight mutation ----

// startBusy shows the in-flight mutation on the status line until its
// adminMsg/forwardMsg lands.
func (m *Model) startBusy(label string) {
	m.busyMsg, m.busySince = label, time.Now()
	m.statusMsg = ""
}

func (m Model) busyLine() string {
	if !m.animEnabled() {
		return m.theme.StatusBar.Render("⏳ " + m.busyMsg)
	}
	return m.theme.Spinner(m.animFrame) + " " + m.theme.StatusBar.Render(m.busyMsg) +
		m.theme.Faint.Render(" · "+elapsed(time.Since(m.busySince)))
}

// ---- rollout tracking ----

// trackRollouts wraps a mutation so that, once it succeeds, the rollouts it
// started are tracked live. Only kinds with a rollout notion are tracked.
func trackRollouts(cmd tea.Cmd, t model.ResourceType, objs ...model.ResourceObject) tea.Cmd {
	if !kindIs(t.Kind, "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet") {
		return cmd
	}
	targets := make([]rolloutTarget, 0, len(objs))
	for _, o := range objs {
		targets = append(targets, rolloutTarget{t: t, ns: o.Namespace, name: o.Name})
	}
	return func() tea.Msg {
		msg := cmd()
		if am, ok := msg.(adminMsg); ok && am.err == nil {
			am.track = targets
			return am
		}
		return msg
	}
}

// beginRollouts registers the targets and fires their first GET.
func (m *Model) beginRollouts(targets []rolloutTarget) tea.Cmd {
	if m.rollouts == nil {
		m.rollouts = map[string]rolloutTrack{}
	}
	cmds := make([]tea.Cmd, 0, len(targets))
	for _, tg := range targets {
		m.rollouts[tg.key()] = rolloutTrack{target: tg, started: time.Now()}
		cmds = append(cmds, m.pollRollout(tg, 0))
	}
	return tea.Batch(cmds...)
}

func (m Model) pollRollout(tg rolloutTarget, delay time.Duration) tea.Cmd {
	cl := m.client
	fetch := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		obj, err := cl.GetObject(ctx, tg.t, tg.ns, tg.name)
		if err != nil {
			return rolloutMsg{key: tg.key(), err: err}
		}
		p, ok := kube.RolloutStatus(tg.t.Kind, obj.Raw)
		return rolloutMsg{key: tg.key(), prog: p, ok: ok}
	}
	if delay <= 0 {
		return fetch
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return fetch() })
}

func (m Model) handleRollout(msg rolloutMsg) (tea.Model, tea.Cmd) {
	tr, ok := m.rollouts[msg.key]
	if !ok {
		return m, nil // dropped (context switch) — a late answer is ignored
	}
	label := tr.target.label()
	switch {
	case msg.err != nil:
		delete(m.rollouts, msg.key)
		m.errMsg = "rollout of " + label + ": status unavailable — " + msg.err.Error()
		return m, nil
	case !msg.ok:
		delete(m.rollouts, msg.key) // OnDelete strategy: nothing rolls by itself
		return m, nil
	}
	tr.prog = msg.prog
	took := elapsed(time.Since(tr.started))
	switch {
	case msg.prog.Failed != "":
		delete(m.rollouts, msg.key)
		m.errMsg = fmt.Sprintf("rollout of %s stalled: %s (%d/%d updated, %d ready)",
			label, msg.prog.Failed, msg.prog.Updated, msg.prog.Desired, msg.prog.Ready)
		return m, nil
	case msg.prog.Done:
		delete(m.rollouts, msg.key)
		if len(m.rollouts) == 0 {
			m.statusMsg = fmt.Sprintf("✓ %s rolled out — %d/%d ready in %s", label, msg.prog.Ready, msg.prog.Desired, took)
		}
		return m, m.listObjects()
	case time.Since(tr.started) > rolloutMax:
		delete(m.rollouts, msg.key)
		m.statusMsg = fmt.Sprintf("! %s still rolling after %s (%d/%d updated) — tracking stopped, check its events",
			label, took, msg.prog.Updated, msg.prog.Desired)
		return m, nil
	}
	m.rollouts[msg.key] = tr
	return m, m.pollRollout(tr.target, rolloutPoll)
}

const rolloutBarW = 20

// rolloutLine renders the tracked rollouts on the status line: one bar for
// a single workload, an aggregate for a bulk restart.
func (m Model) rolloutLine() string {
	keys := make([]string, 0, len(m.rollouts))
	for k := range m.rollouts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var frac float64
	var started time.Time
	upd, ready, desired, observed := 0, 0, 0, 0
	for _, k := range keys {
		tr := m.rollouts[k]
		frac += tr.prog.Fraction()
		upd, ready, desired = upd+tr.prog.Updated, ready+tr.prog.Ready, desired+tr.prog.Desired
		if tr.prog.Observed {
			observed++
		}
		if started.IsZero() || tr.started.Before(started) {
			started = tr.started
		}
	}
	frac /= float64(len(keys))
	subject := m.rollouts[keys[0]].target.label() + " rolling out"
	if len(keys) > 1 {
		subject = fmt.Sprintf("%d rollouts", len(keys))
	}
	counts := fmt.Sprintf("%d/%d updated · %d ready", upd, desired, ready)
	if observed < len(keys) {
		counts = "waiting for the controller"
	}
	spin, bar := "⏳", m.theme.Faint.Render("[")+m.theme.Bar(rolloutBarW, frac, 0)+m.theme.Faint.Render("]")
	if m.animEnabled() {
		spin = m.theme.Spinner(m.animFrame)
		bar = m.theme.Faint.Render("▕") + m.theme.Bar(rolloutBarW, frac, m.animFrame) + m.theme.Faint.Render("▏")
	}
	return spin + " " + m.theme.StatusBar.Render(subject) + " " + bar + " " +
		m.theme.StatusBar.Render(counts) + m.theme.Faint.Render(" · "+elapsed(time.Since(started)))
}
