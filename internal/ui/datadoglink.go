package ui

// Datadog logs deep-link (FR-039, US17): 'D' (or "datadog" in the 'a'
// palette) opens the OS browser on Datadog Logs scoped to the selection.
// The URL is built by internal/datadog — pure string work, zero calls to
// Datadog. When no valid link exists the status line says why; when the
// browser cannot launch (headless/SSH) the URL is shown to copy by hand.

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/datadog"
	"github.com/iadvize/idz-k8s/internal/model"
)

// datadogOpenedMsg reports the browser launch.
type datadogOpenedMsg struct {
	url, label string
	err        error
}

// datadogSettings maps the config block onto the link builder (read at use
// time, so a live config reload applies immediately).
func (m Model) datadogSettings() datadog.Settings {
	d := m.cfg.Datadog
	return datadog.Settings{Site: d.Site, Window: d.Window, PodQuery: d.PodQuery,
		WorkloadQuery: d.WorkloadQuery, NamespaceQuery: d.NamespaceQuery}
}

// datadogScope describes an object of the given kind for the link builder.
// siblings are its same-kind neighbours in the namespace (prefix collisions,
// see datadog.Scope).
func (m Model) datadogScope(kind string, obj model.ResourceObject, siblings []string) datadog.Scope {
	sc := datadog.Scope{Kind: kind, Namespace: obj.Namespace, Name: obj.Name, Siblings: siblings}
	if m.client != nil {
		sc.Context = m.client.ActiveContext()
	}
	return sc
}

// siblingsOf lists the other loaded objects of the current type in obj's
// namespace.
func (m Model) siblingsOf(obj model.ResourceObject) []string {
	var out []string
	for _, o := range m.objects {
		if o.Namespace == obj.Namespace && o.Name != obj.Name {
			out = append(out, o.Name)
		}
	}
	return out
}

// openDatadogSelection is the 'D' key of the list.
func (m Model) openDatadogSelection() (tea.Model, tea.Cmd) {
	obj, ok := m.selectedObject()
	if !ok {
		m.errMsg = "Datadog link: nothing selected"
		return m, nil
	}
	return m.openDatadog(m.datadogScope(m.curType.Kind, obj, m.siblingsOf(obj)))
}

// openDatadogPod is the 'D' key of the containers view: the pod's logs (the
// log pipeline does not tell containers apart, so the pod is the scope).
func (m Model) openDatadogPod() (tea.Model, tea.Cmd) {
	if m.containerPod.Name == "" {
		m.errMsg = "Datadog link: nothing selected"
		return m, nil
	}
	return m.openDatadog(m.datadogScope("Pod", m.containerPod, nil))
}

// datadogAction is the 'a' palette entry for an object, nil when its kind
// has no log scope (the palette only offers what can work).
func datadogAction(id, kind string, obj model.ResourceObject, siblings []string) *actionEntry {
	if !datadog.Supported(kind) {
		return nil
	}
	label := kind + "/" + obj.Name
	return &actionEntry{id, "open the logs of " + label + " in Datadog (browser)", func(m *Model) (tea.Model, tea.Cmd) {
		return m.openDatadog(m.datadogScope(kind, obj, siblings))
	}}
}

// openDatadog builds the link and hands it to the browser. No link, no
// launch: the reason goes to the status line instead of a guessed URL.
func (m *Model) openDatadog(sc datadog.Scope) (tea.Model, tea.Cmd) {
	label := sc.Kind + "/" + sc.Name
	u, err := datadog.LogsURL(m.datadogSettings(), sc, time.Now())
	if err != nil {
		m.errMsg = "Datadog link: " + err.Error()
		return m, nil
	}
	m.errMsg = ""
	m.statusMsg = "opening the Datadog logs of " + label + "…"
	open := m.openURL
	if open == nil {
		open = openInBrowser
	}
	return m, func() tea.Msg {
		return datadogOpenedMsg{url: u, label: label, err: open(u)}
	}
}

// handleDatadogOpened confirms the launch, or shows the URL full-screen so
// it can be copied when no browser could open it.
func (m *Model) handleDatadogOpened(msg datadogOpenedMsg) {
	if msg.err == nil {
		m.statusMsg = "✓ Datadog logs of " + msg.label + " opened in the browser"
		return
	}
	m.statusMsg = ""
	m.errMsg = "browser did not open: " + msg.err.Error()
	w := m.width
	if w < 20 {
		w = 80
	}
	content := fmt.Sprintf("Datadog logs of %s\n\nThe browser could not be opened (%v).\nCopy the link below ('m' turns the mouse off for text selection):\n\n%s\n",
		msg.label, msg.err, xansi.Hardwrap(msg.url, w, false))
	if m.screen != screenDetail {
		m.describeReturn = m.screen
	}
	m.detailHasUsage = false
	m.detailNS, m.detailName = "", "" // no late usageMsg may redraw over the link
	m.screen = screenDetail
	m.setDetailContent(content)
	m.detail.GotoTop()
	m.layout()
}

// openInBrowser hands the URL to the OS (open / xdg-open). Output is
// captured so nothing scribbles over the TUI; a launcher that cannot run
// (no browser, headless session) returns an error the caller reports.
func openInBrowser(u string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", u)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", u)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return fmt.Errorf("%s: %w (%s)", cmd.Args[0], err, truncate(s, 80))
		}
		return fmt.Errorf("%s: %w", cmd.Args[0], err)
	}
	return nil
}
