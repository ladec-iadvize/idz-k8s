package ui

// v3 admin actions: the 'a' actions palette (per-selection admin verbs), the
// confirmation modal every mutation goes through, the value prompts (scale
// replicas, port-forward ports), the $EDITOR edit-YAML flow, and the
// port-forward registry. Analysis stays in the '>' palette; ADMIN lives here.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// promptKind selects the active value prompt (Enter commits, Esc cancels —
// the same contract as every typing mode, invariant 0).
type promptKind int

const (
	promptNone promptKind = iota
	promptScale
	promptForward
)

// actionEntry is one admin action offered by the 'a' palette for the current
// selection. Same shape as the '>' palette entries (consistency, invariant 0).
type actionEntry struct {
	id, desc string
	run      func(m *Model) (tea.Model, tea.Cmd)
}

// adminTimeout bounds every one-shot mutation call.
const adminTimeout = 15 * time.Second

// openActions opens the actions palette for the current selection (list) or
// the selected helm release (helm view). Consistent with '>' — one modal,
// type-to-filter included.
func (m Model) openActions() (tea.Model, tea.Cmd) {
	var entries []actionEntry
	var target string
	switch m.screen {
	case screenHelm:
		entries, target = m.helmActions()
	default:
		entries, target = m.listActions()
	}
	if len(entries) == 0 {
		m.statusMsg = "no actions for this selection"
		return m, nil
	}
	m.actionList = entries
	m.actionFor = target
	m.pickerKind = pickAction
	m.pickerReturn = m.screen
	m.pickerQuery = ""
	opts := make([]string, len(entries))
	for i, e := range entries {
		opts[i] = fmt.Sprintf("%-14s %s", e.id, e.desc)
	}
	m.pickerOpts = opts
	m.applyPickerRows()
	m.pickerWin.Home()
	m.screen = screenPicker
	m.layout()
	return m, nil
}

// listActions builds the admin actions for the object under the cursor.
func (m *Model) listActions() ([]actionEntry, string) {
	obj, ok := m.selectedObject()
	if !ok {
		return nil, ""
	}
	kind := m.curType.Kind
	label := kind + "/" + obj.Name
	t := m.curType
	var out []actionEntry

	if kindIs(kind, "Deployment", "StatefulSet", "ReplicaSet") {
		out = append(out, actionEntry{"scale", "set replicas of " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.openScalePrompt(obj)
		}})
	}
	if kindIs(kind, "Deployment", "StatefulSet", "DaemonSet") {
		out = append(out, actionEntry{"restart", "rolling restart of " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.requestConfirm("rolling restart of "+label,
				m.adminCmd(label+" restarted", func(ctx context.Context, cl *kube.Client) error {
					return cl.RolloutRestart(ctx, t, obj.Namespace, obj.Name, time.Now())
				}))
		}})
	}
	if kindIs(kind, "Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Service") {
		out = append(out, actionEntry{"port-forward", "localhost tunnel to " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.openForwardPrompt(obj)
		}})
	}
	for _, fw := range m.forwardsFor(obj) {
		out = append(out, actionEntry{"stop-forward", "stop " + fw.Label(), func(m *Model) (tea.Model, tea.Cmd) {
			fw.Stop()
			delete(m.forwards, fw.Key())
			m.statusMsg = "✓ stopped " + fw.Label()
			return m, nil
		}})
	}
	if kindIs(kind, "Node") {
		cordoned, _, _ := unstructured.NestedBool(obj.Raw, "spec", "unschedulable")
		id, verb := "cordon", "mark "+label+" unschedulable"
		if cordoned {
			id, verb = "uncordon", "mark "+label+" schedulable again"
		}
		out = append(out, actionEntry{id, verb, func(m *Model) (tea.Model, tea.Cmd) {
			return m.requestConfirm(verb,
				m.adminCmd(label+" "+id+"ed", func(ctx context.Context, cl *kube.Client) error {
					return cl.SetCordon(ctx, t, obj.Name, !cordoned)
				}))
		}})
	}
	if kindIs(kind, "CronJob") {
		suspended, _, _ := unstructured.NestedBool(obj.Raw, "spec", "suspend")
		id, verb := "suspend", "suspend scheduling of "+label
		if suspended {
			id, verb = "resume", "resume scheduling of "+label
		}
		out = append(out, actionEntry{id, verb, func(m *Model) (tea.Model, tea.Cmd) {
			return m.requestConfirm(verb,
				m.adminCmd(label+" "+id+"d", func(ctx context.Context, cl *kube.Client) error {
					return cl.SetSuspend(ctx, t, obj.Namespace, obj.Name, !suspended)
				}))
		}})
	}
	out = append(out,
		actionEntry{"edit", "edit " + label + " in $EDITOR", func(m *Model) (tea.Model, tea.Cmd) {
			return m, m.startEdit()
		}},
		actionEntry{"delete", "⚠ delete " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.requestConfirm("⚠ DELETE "+label+" ("+orDash(obj.Namespace)+")",
				m.adminCmd(label+" deleted", func(ctx context.Context, cl *kube.Client) error {
					return cl.DeleteObject(ctx, t, obj.Namespace, obj.Name)
				}))
		}},
	)
	return out, label
}

// helmActions builds the admin actions for the selected helm release.
func (m *Model) helmActions() ([]actionEntry, string) {
	row, ok := m.helmWin.Selected()
	if !ok || len(row) < 2 || m.helm == nil {
		return nil, ""
	}
	ns, name := row[0], row[1]
	label := "release " + ns + "/" + name
	hc := m.helm
	return []actionEntry{
		{"rollback", "roll " + label + " back to its previous revision", func(m *Model) (tea.Model, tea.Cmd) {
			return m.requestConfirm("rollback "+label+" to the previous revision",
				m.helmCmd(label+" rolled back", func() error { return hc.Rollback(ns, name, 0) }))
		}},
		{"uninstall", "⚠ uninstall " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.requestConfirm("⚠ UNINSTALL "+label,
				m.helmCmd(label+" uninstalled", func() error { return hc.Uninstall(ns, name) }))
		}},
	}, label
}

// kindIs reports whether kind matches any of the given kinds (case-insensitive).
func kindIs(kind string, kinds ...string) bool {
	for _, k := range kinds {
		if strings.EqualFold(kind, k) {
			return true
		}
	}
	return false
}

// forwardsFor lists the active forwards attached to a pod (or resolved from
// a workload/service — matched by namespace only, best effort for display).
func (m *Model) forwardsFor(obj model.ResourceObject) []*kube.PortForward {
	var out []*kube.PortForward
	for _, fw := range m.forwards {
		if fw.Namespace != obj.Namespace {
			continue
		}
		if strings.EqualFold(m.curType.Kind, "Pod") && fw.Pod != obj.Name {
			continue
		}
		out = append(out, fw)
	}
	return out
}

// ---- confirmation ----

// requestConfirm arms the confirmation modal: Enter runs cmd, Esc cancels.
// EVERY mutation goes through here or through a value prompt — nothing
// mutates on a single keypress (FR-012 v3).
func (m Model) requestConfirm(title string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.confirming = true
	m.confirmTitle = title
	m.confirmCmd = cmd
	return m, nil
}

// confirmModal renders the centered confirmation box.
func (m Model) confirmModal() string {
	w := m.modalW()
	inner := w - 4
	title := "confirm action"
	style := m.theme.TableHeader
	if strings.HasPrefix(m.confirmTitle, "⚠") {
		title = "confirm — destructive action"
		style = m.theme.Error
	}
	lines := []string{
		style.Render(padTo(title, inner)),
		padTo(m.confirmTitle, inner),
		m.theme.Faint.Render(padTo("Enter confirm · Esc cancel", inner)),
	}
	return m.theme.ModalBorder.Render(strings.Join(lines, "\n"))
}

// handleConfirmKey consumes every key while the confirmation modal is open.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		cmd := m.confirmCmd
		m.confirming, m.confirmCmd = false, nil
		m.statusMsg = "⏳ " + m.confirmTitle
		return m, cmd
	case tea.KeyEscape:
		m.confirming, m.confirmCmd = false, nil
		m.statusMsg = "cancelled — nothing was changed"
		return m, nil
	}
	return m, nil
}

// ---- value prompts (scale, port-forward) ----

func (m Model) openScalePrompt(obj model.ResourceObject) (tea.Model, tea.Cmd) {
	t := m.curType
	label := t.Kind + "/" + obj.Name
	cur := ""
	if v, found, _ := unstructured.NestedInt64(obj.Raw, "spec", "replicas"); found {
		cur = strconv.FormatInt(v, 10)
	}
	m.promptKind = promptScale
	m.promptTitle = "scale " + label + " — replicas"
	m.promptInput = cur
	m.promptHint = "Enter apply · Esc cancel"
	m.promptAction = func(m *Model, value string) (tea.Model, tea.Cmd) {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			m.errMsg = "replicas must be a non-negative integer, got " + strconv.Quote(value)
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("⏳ scaling %s to %d", label, n)
		return m, m.adminCmd(fmt.Sprintf("%s scaled to %d", label, n),
			func(ctx context.Context, cl *kube.Client) error {
				return cl.ScaleWorkload(ctx, t, obj.Namespace, obj.Name, n)
			})
	}
	return m, nil
}

func (m Model) openForwardPrompt(obj model.ResourceObject) (tea.Model, tea.Cmd) {
	t := m.curType
	label := t.Kind + "/" + obj.Name
	m.promptKind = promptForward
	m.promptTitle = "port-forward " + label
	m.promptInput = suggestForwardPorts(t.Kind, obj.Raw)
	m.promptHint = "local:remote (or one port for both) · Enter start · Esc cancel"
	m.promptAction = func(m *Model, value string) (tea.Model, tea.Cmd) {
		local, remote, err := parseForwardPorts(value)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("⏳ port-forward %s → %s:%d", value, label, remote)
		return m, m.startForward(obj, local, remote)
	}
	return m, nil
}

// handlePromptKey consumes every key while a value prompt is open.
func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch act, _ := editText(&m.promptInput, msg); act {
	case editCommit:
		action := m.promptAction
		value := strings.TrimSpace(m.promptInput)
		m.promptKind, m.promptInput, m.promptAction = promptNone, "", nil
		if action == nil {
			return m, nil
		}
		return action(&m, value)
	case editCancel:
		m.promptKind, m.promptInput, m.promptAction = promptNone, "", nil
		m.statusMsg = "cancelled — nothing was changed"
	}
	return m, nil
}

// suggestForwardPorts derives a sensible default from the object's first
// declared port ("8080:80"-style); empty when none is declared.
func suggestForwardPorts(kind string, raw map[string]any) string {
	var port int64
	switch {
	case strings.EqualFold(kind, "Pod"):
		if cs, _, _ := unstructured.NestedSlice(raw, "spec", "containers"); len(cs) > 0 {
			if cm, ok := cs[0].(map[string]any); ok {
				if ps, _, _ := unstructured.NestedSlice(cm, "ports"); len(ps) > 0 {
					if pm, ok := ps[0].(map[string]any); ok {
						port, _ = pm["containerPort"].(int64)
					}
				}
			}
		}
	case strings.EqualFold(kind, "Service"):
		if ps, _, _ := unstructured.NestedSlice(raw, "spec", "ports"); len(ps) > 0 {
			if pm, ok := ps[0].(map[string]any); ok {
				port, _ = pm["port"].(int64)
			}
		}
	default: // workloads: first template container port
		if cs, _, _ := unstructured.NestedSlice(raw, "spec", "template", "spec", "containers"); len(cs) > 0 {
			if cm, ok := cs[0].(map[string]any); ok {
				if ps, _, _ := unstructured.NestedSlice(cm, "ports"); len(ps) > 0 {
					if pm, ok := ps[0].(map[string]any); ok {
						port, _ = pm["containerPort"].(int64)
					}
				}
			}
		}
	}
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", port, port)
}

// parseForwardPorts parses "local:remote" ("8080:80") or a single port used
// for both ends ("80").
func parseForwardPorts(s string) (local, remote int, err error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	bad := func() (int, int, error) {
		return 0, 0, fmt.Errorf("ports must be \"local:remote\" or a single port, got %q", s)
	}
	remote, err = strconv.Atoi(parts[len(parts)-1])
	if err != nil || remote <= 0 {
		return bad()
	}
	local = remote
	if len(parts) == 2 {
		local, err = strconv.Atoi(parts[0])
		if err != nil || local < 0 {
			return bad()
		}
	}
	return local, remote, nil
}

// startForward resolves the target pod (the object itself, or the first
// ready pod behind its selector) and opens the tunnel off the Update loop.
func (m Model) startForward(obj model.ResourceObject, local, remote int) tea.Cmd {
	cl, t := m.client, m.curType
	label := t.Kind + "/" + obj.Name
	return func() tea.Msg {
		pod := obj.Name
		if !strings.EqualFold(t.Kind, "Pod") {
			sel, ok := kube.PodSelector(obj.Raw)
			if !ok {
				return forwardMsg{err: fmt.Errorf("%s has no pod selector to forward to", label)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
			defer cancel()
			name, err := cl.FirstReadyPod(ctx, obj.Namespace, sel)
			if err != nil {
				return forwardMsg{err: fmt.Errorf("%s: %w", label, err)}
			}
			pod = name
		}
		fw, err := cl.ForwardPort(obj.Namespace, pod, local, remote)
		if err != nil {
			return forwardMsg{err: err}
		}
		fw.For = label
		return forwardMsg{fw: fw, fo: label}
	}
}

// stopAllForwards tears down every active tunnel (quit, context switch).
func (m *Model) stopAllForwards() {
	for k, fw := range m.forwards {
		fw.Stop()
		delete(m.forwards, k)
	}
}

// ---- one-shot mutation commands ----

// adminCmd wraps one kube mutation into a tea.Cmd reporting adminMsg.
func (m Model) adminCmd(summary string, fn func(ctx context.Context, cl *kube.Client) error) tea.Cmd {
	cl := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		if err := fn(ctx, cl); err != nil {
			return adminMsg{summary: summary, err: err}
		}
		return adminMsg{summary: summary}
	}
}

// helmCmd wraps one helm mutation into a tea.Cmd reporting adminMsg.
func (m Model) helmCmd(summary string, fn func() error) tea.Cmd {
	return func() tea.Msg {
		if err := fn(); err != nil {
			return adminMsg{summary: summary, err: err}
		}
		return adminMsg{summary: summary}
	}
}

// fetchHelmRows reloads the helm release list (after a rollback/uninstall).
func (m Model) fetchHelmRows() tea.Cmd {
	hc, ns := m.helm, m.client.Namespace
	if hc == nil {
		return nil
	}
	return func() tea.Msg {
		rows, err := hc.Releases(ns)
		return helmMsg{rows: rows, err: err}
	}
}

// ---- edit-YAML flow ($EDITOR) ----

// startEdit fetches the live object as YAML into a temp file, then hands the
// terminal to the editor (editOpenMsg → tea.ExecProcess in Update).
func (m *Model) startEdit() tea.Cmd {
	obj, ok := m.selectedObject()
	if !ok {
		return nil
	}
	cl, t := m.client, m.curType
	ns, name := obj.Namespace, obj.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		doc, err := cl.ObjectYAML(ctx, t, ns, name)
		if err != nil {
			return editOpenMsg{err: err}
		}
		f, err := os.CreateTemp("", "idz-k8s-edit-*.yaml")
		if err != nil {
			return editOpenMsg{err: err}
		}
		if _, err := f.WriteString(doc); err != nil {
			_ = f.Close()
			return editOpenMsg{err: err}
		}
		if err := f.Close(); err != nil {
			return editOpenMsg{err: err}
		}
		return editOpenMsg{path: f.Name(), original: doc, t: t, ns: ns, name: name}
	}
}

// editorCommand builds the editor invocation: $KUBE_EDITOR, then $EDITOR,
// then vi — kubectl edit's resolution order.
func editorCommand(path string) *exec.Cmd {
	ed := os.Getenv("KUBE_EDITOR")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "vi"
	}
	parts := strings.Fields(ed)
	args := append(parts[1:], path) //nolint:gocritic // parts[1:] is never reused
	return exec.Command(parts[0], args...)
}

// applyEdit reads the edited file back and updates the object — unless the
// content is unchanged, in which case nothing is sent to the cluster.
func (m Model) applyEdit(msg editorClosedMsg) tea.Cmd {
	cl := m.client
	return func() tea.Msg {
		defer func() { _ = os.Remove(msg.path) }()
		label := msg.t.Kind + "/" + msg.name
		data, err := os.ReadFile(msg.path)
		if err != nil {
			return adminMsg{summary: label, err: err}
		}
		if strings.TrimSpace(string(data)) == strings.TrimSpace(msg.original) {
			return adminMsg{summary: label + " unchanged — nothing applied"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
		defer cancel()
		if err := cl.ApplyYAML(ctx, msg.t, data); err != nil {
			return adminMsg{summary: label, err: err}
		}
		return adminMsg{summary: label + " updated"}
	}
}
