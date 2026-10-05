package ui

// Datadog logs deep-link (FR-039, US17): 'D' and the 'a' palette hand a
// scoped URL to the browser; no link → an explicit message and NO launch;
// a browser that cannot open → the URL is shown to copy (SC-024).

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/model"
)

// urlRecorder replaces the browser launcher and records what it was given.
type urlRecorder struct {
	urls []string
	err  error
}

func (r *urlRecorder) open(u string) error {
	r.urls = append(r.urls, u)
	return r.err
}

// runCmd executes a command and feeds its message back (one round-trip).
func runCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	mi, _ := m.Update(cmd())
	return asModel(t, mi)
}

func ddQuery(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("bad link %q: %v", link, err)
	}
	return u.Query().Get("query")
}

// runAction opens the 'a' palette, picks the entry and runs its command
// (selectAction drops the command — a launch happens inside it).
func runAction(t *testing.T, m Model, id string) Model {
	t.Helper()
	m = pressRune(t, m, 'a')
	for i, row := range m.pickerWin.rows {
		if strings.HasPrefix(strings.TrimSpace(row[0]), id+" ") {
			m.pickerWin.cursor = i
			mi, cmd := m.pickerSelect()
			return runCmd(t, asModel(t, mi), cmd)
		}
	}
	t.Fatalf("action %q not in palette:\n%s", id, pickerOptions(m))
	return m
}

// ddModel is the admin Deployment list with a prefix sibling loaded, a
// recording launcher and a workload template without {context} (the fake
// client has no kube context).
func ddModel(t *testing.T) (Model, *urlRecorder) {
	t.Helper()
	m := adminModel(t)
	m.cfg.Datadog.WorkloadQuery = "@namespace:{namespace} @pod_owner:{owner}"
	m.objects = append(m.objects, model.ResourceObject{
		Type: deploymentsType, Namespace: "demo", Name: "back-traceability",
		Raw: fakeDeployment("demo", "back-traceability", 1)})
	m.applyRows()
	rec := &urlRecorder{}
	m.openURL = rec.open
	return m, rec
}

func TestDatadogKeyOpensScopedLink(t *testing.T) {
	m, rec := ddModel(t)
	if obj, _ := m.selectedObject(); obj.Name != "back" {
		t.Fatalf("precondition: 'back' selected, got %q", obj.Name)
	}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m = runCmd(t, asModel(t, mi), cmd)
	if len(rec.urls) != 1 {
		t.Fatalf("'D' must open exactly one link, got %v", rec.urls)
	}
	if !strings.HasPrefix(rec.urls[0], "https://app.datadoghq.eu/logs?") {
		t.Errorf("default site is the EU app: %s", rec.urls[0])
	}
	want := "@namespace:demo @pod_owner:(ReplicaSet/back-* -ReplicaSet/back-traceability-*)"
	if got := ddQuery(t, rec.urls[0]); got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
	if m.screen != screenList || !strings.Contains(m.statusMsg, "✓") {
		t.Errorf("a successful launch stays on the list and confirms (screen=%v status=%q)", m.screen, m.statusMsg)
	}
}

func TestDatadogInActionsPalette(t *testing.T) {
	m, rec := ddModel(t)
	if opts := pickerOptions(pressRune(t, m, 'a')); !strings.Contains(opts, "datadog") {
		t.Fatalf("the actions palette must offer the Datadog link:\n%s", opts)
	}
	runAction(t, m, "datadog")
	if len(rec.urls) != 1 {
		t.Fatalf("selecting 'datadog' must open the link, got %v", rec.urls)
	}
}

// SC-024: no valid link → say why, never launch a guessed URL.
func TestDatadogNeverOpensAGuessedLink(t *testing.T) {
	cases := map[string]func(m *Model){
		"site off":         func(m *Model) { m.cfg.Datadog.Site = "off" },
		"no kube context":  func(m *Model) { m.cfg.Datadog.WorkloadQuery = "" }, // default needs {context}
		"bad placeholder":  func(m *Model) { m.cfg.Datadog.WorkloadQuery = "service:{service}" },
		"unsupported kind": func(m *Model) { m.curType.Kind = "ConfigMap" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, rec := ddModel(t)
			mutate(&m)
			mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
			m = asModel(t, mi)
			if cmd != nil {
				m = runCmd(t, m, cmd)
			}
			if len(rec.urls) != 0 {
				t.Fatalf("no browser launch allowed, got %v", rec.urls)
			}
			if !strings.HasPrefix(m.errMsg, "Datadog link: ") {
				t.Errorf("the reason must be stated, errMsg=%q", m.errMsg)
			}
		})
	}
	// An unsupported kind is not even offered in the palette.
	m, _ := ddModel(t)
	m.curType.Kind = "ConfigMap"
	m = pressRune(t, m, 'a')
	if strings.Contains(pickerOptions(m), "datadog") {
		t.Errorf("no Datadog entry for a kind without log scope:\n%s", pickerOptions(m))
	}
}

// Headless/SSH: the launcher fails → the URL is shown in full to copy, and
// Esc returns to where the operator was.
func TestDatadogShowsURLWhenBrowserFails(t *testing.T) {
	m, rec := ddModel(t)
	rec.err = errors.New("exec: \"open\": executable file not found")
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m = runCmd(t, asModel(t, mi), cmd)
	if m.screen != screenDetail {
		t.Fatalf("a failed launch must show the URL, screen=%v", m.screen)
	}
	body := strings.ReplaceAll(m.vpRaw[screenDetail], "\n", "")
	if !strings.Contains(body, rec.urls[0]) {
		t.Errorf("the full URL must be on screen:\n%s", m.vpRaw[screenDetail])
	}
	if !strings.Contains(m.errMsg, "browser did not open") {
		t.Errorf("the failure must be explicit, errMsg=%q", m.errMsg)
	}
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = asModel(t, mi); m.screen != screenList {
		t.Errorf("Esc must return to the list, screen=%v", m.screen)
	}
}

// Inside a drill (CronJob → Jobs) the parent's link is offered, even on an
// empty level, and excludes the parent's prefix siblings snapshotted at
// drill time.
func TestDatadogParentLinkInDrilledList(t *testing.T) {
	m := cronDrilledModel(t)
	m.cfg.Datadog.WorkloadQuery = "@pod_owner:{owner}"
	rec := &urlRecorder{}
	m.openURL = rec.open
	m.drillParentSiblings = []string{"nightly-report"} // as snapshotted by drillInto
	if opts := pickerOptions(pressRune(t, m, 'a')); !strings.Contains(opts, "datadog-parent") {
		t.Fatalf("a drilled level must offer the parent's Datadog link:\n%s", opts)
	}
	runAction(t, m, "datadog-parent")
	if len(rec.urls) != 1 {
		t.Fatalf("datadog-parent must open one link, got %v", rec.urls)
	}
	if got, want := ddQuery(t, rec.urls[0]), "@pod_owner:(Job/nightly-* -Job/nightly-report-*)"; got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
}

// drillInto snapshots the parent's same-namespace siblings.
func TestDrillSnapshotsParentSiblings(t *testing.T) {
	m, _ := ddModel(t)
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if !m.drilling() || len(m.drillParentSiblings) != 1 || m.drillParentSiblings[0] != "back-traceability" {
		t.Errorf("parent siblings = %v, want [back-traceability]", m.drillParentSiblings)
	}
}

func TestDatadogFromContainersView(t *testing.T) {
	m, rec := ddModel(t)
	m.screen = screenContainers
	m.containerPod = model.ResourceObject{Namespace: "demo", Name: "back-5f7c9-abcde"}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	runCmd(t, asModel(t, mi), cmd)
	if len(rec.urls) != 1 {
		t.Fatalf("'D' in the containers view must open the pod's link, got %v", rec.urls)
	}
	if got, want := ddQuery(t, rec.urls[0]), "@namespace:demo @container_name:back-5f7c9-abcde"; got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
}
