package ui

// Advisory views: drift (US16), access/RBAC (US15),
// connectivity/NetworkPolicy (US14), posture (US13) and jump-to-ref.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

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
	b.WriteString(m.rule("Diff — "+subject+" vs last-applied") + "\n\n")
	switch {
	case !hasBaseline:
		b.WriteString(m.theme.Faint.Render("No baseline: this object has no last-applied configuration annotation\n"+
			"(it was not created with 'kubectl apply' or declarative tooling), so there\n"+
			"is nothing to diff against. Nothing is wrong — there is just no reference.") + "\n")
	case len(drifts) == 0:
		b.WriteString(m.theme.Ok.Render("✓ no drift — the live object matches its last-applied configuration") + "\n")
	default:
		fmt.Fprintf(&b, "%d drifted field(s) — fields present in the baseline only; server defaults are not drift:\n\n", len(drifts))
		// Columns scale with the terminal (content-driven layout, v3): FIELD
		// gets ~40% of the width, APPLIED/LIVE share the rest.
		fieldW := clampW(m.width*2/5, 30, 60)
		valueW := clampW((m.width-fieldW-8)/2, 20, 80)
		fmt.Fprintf(&b, "  %s\n", m.theme.Faint.Render(fmt.Sprintf("%-*s %-*s %s", fieldW+2, "FIELD", valueW, "APPLIED", "LIVE")))
		for _, d := range drifts {
			line := fmt.Sprintf("  ~ %-*s %-*s %s", fieldW, truncate(d.Path, fieldW), valueW, truncate(d.Applied, valueW), truncate(d.Live, valueW))
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

// objRef points a finding/event at the concrete object it references.
type objRef struct {
	typeKey string // e.g. "v1/pods"; "" = resolve by kind
	kind    string // used when typeKey is empty (events carry a Kind)
	ns      string
	name    string
}

// openDescribeRef fetches the referenced object and opens its describe;
// Esc returns to the view the jump came from.
func (m *Model) openDescribeRef(ref objRef) (tea.Model, tea.Cmd) {
	t, ok := findTypeByKey(m.types, ref.typeKey)
	if !ok && ref.kind != "" {
		for _, tt := range m.types {
			if strings.EqualFold(tt.Kind, ref.kind) {
				t, ok = tt, true
				break
			}
		}
	}
	if !ok {
		m.statusMsg = "cannot open: type not browsable on this cluster"
		return m, nil
	}
	m.describeReturn = m.screen
	m.screen = screenDetail
	m.detailNS, m.detailName = ref.ns, ref.name
	m.detailHasUsage = false
	m.setDetailContent("fetching " + t.Kind + " " + ref.ns + "/" + ref.name + "…")
	m.detail.GotoTop()
	m.layout()
	cl := m.client
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		obj, err := cl.GetObject(ctx, t, ref.ns, ref.name)
		if err != nil {
			return errMsg{err}
		}
		rows, _ := cl.Events(ctx, ref.ns)
		var own []model.Event
		for _, e := range rows {
			if e.ObjName == ref.name {
				own = append(own, e)
			}
		}
		return describeRefMsg{obj: obj, events: own}
	}
}

type describeRefMsg struct {
	obj    model.ResourceObject
	events []model.Event
}

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
	m.postureAll = rows
	m.postureSel = 0
	m.renderPostureView()
}

// postureRef maps a finding to the object it references.
func postureRef(f model.PostureFinding) objRef {
	switch f.Rule {
	case kube.RuleNoNetpol:
		return objRef{typeKey: "v1/namespaces", name: f.Name}
	case kube.RuleTLSExpiry:
		return objRef{typeKey: "v1/secrets", ns: f.Namespace, name: f.Name}
	default:
		return objRef{typeKey: "v1/pods", ns: f.Namespace, name: f.Name}
	}
}

func (m *Model) renderPostureView() {
	rows := filterFindings(m.postureAll, m.postureErrOnly,
		func(f model.PostureFinding) model.HealthLevel { return f.Severity }, &m.postureSel)
	m.renderPostureContent(rows)
}

func (m *Model) renderPostureContent(rows []model.PostureFinding) {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Posture (advisory) — %s, %d finding(s)", scope, len(rows))) + "\n\n")
	b.WriteString(m.theme.Faint.Render("Best-practice review of the observed configuration — advisory, nothing is applied automatically.") + "\n\n")
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
	groups := make([]findingGroup, 0, len(order))
	for _, rule := range order {
		fs := byRule[rule]
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].Severity > fs[j].Severity })
		items := make([]findingItem, 0, len(fs))
		for _, f := range fs {
			ref := f.Namespace + "/" + f.Name
			if f.Container != "" {
				ref += " [" + f.Container + "]"
			}
			items = append(items, findingItem{level: f.Severity, who: ref, detail: f.Detail,
				ref: postureRef(f)})
		}
		groups = append(groups, findingGroup{title: rule, items: items})
	}
	m.renderFindingGroups(&b, groups, m.postureSel, m.findingWhoWidth(), &m.postureRefs, &m.postureLines)
	b.WriteString(m.theme.Faint.Render("Enter opens the object · 'w' errors only · ↑/↓ select"))
	b.WriteString("\n")
	m.setContent(screenPosture, b.String())
	m.keepFindingVisible(&m.posture, m.postureLines, m.postureSel)
	m.layout()
}
