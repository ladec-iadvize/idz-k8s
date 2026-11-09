package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/ui/theme"
)

func plain(s string) string { return xansi.Strip(s) }

// TestLoaderShowsWhileViewFetches: a fetching view draws the animated loader
// in its body — same height, so nothing below it moves — and the result
// message (error or not) clears it.
func TestLoaderShowsWhileViewFetches(t *testing.T) {
	m := adminModel(t)
	before := strings.Count(m.View().Content, "\n")
	mi, _ := withAnim(m.openTopology())
	m = asModel(t, mi)
	if !m.animOn {
		t.Fatal("opening a fetching view must start the frame tick")
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "loading topology") || !strings.Contains(out, "━") {
		t.Fatalf("loader (label + comet) missing:\n%s", out)
	}
	if got := strings.Count(m.View().Content, "\n"); got != before {
		t.Fatalf("loader changed the frame height: %d lines, want %d (geometry is sacred)", got, before)
	}
	mi, _ = m.Update(topologyMsg{})
	m = asModel(t, mi)
	if _, still := m.loading[screenTopology]; still {
		t.Fatal("the result message must clear the loader")
	}
	if strings.Contains(plain(m.View().Content), "━") {
		t.Fatal("loader still drawn after the result arrived")
	}
}

func TestAnimationsOffKeepsStaticText(t *testing.T) {
	m := adminModel(t)
	m.cfg.Animations = "off"
	mi, _ := withAnim(m.openTopology())
	m = asModel(t, mi)
	if m.animOn {
		t.Fatal("animations: off must never start the tick")
	}
	out := plain(m.View().Content)
	if !strings.Contains(out, "loading topology…") || strings.Contains(out, "━") {
		t.Fatalf("animations off must show the plain placeholder:\n%s", out)
	}
}

// TestAnimTickStopsWhenIdle: an idle app never re-renders on a timer.
func TestAnimTickStopsWhenIdle(t *testing.T) {
	m := adminModel(t)
	m.animOn = true
	mi, cmd := m.Update(animTickMsg{})
	m = asModel(t, mi)
	if cmd != nil || m.animOn {
		t.Fatal("the tick must stop once nothing animates")
	}

	m.startLoading(screenList, "loading")
	m.animOn = true
	frame := m.animFrame
	mi, cmd = m.Update(animTickMsg{})
	m = asModel(t, mi)
	if cmd == nil || !m.animOn || m.animFrame != frame+1 {
		t.Fatal("the tick must keep running (and advance the frame) while something animates")
	}
}

func TestBusySpinnerUntilMutationResult(t *testing.T) {
	m := adminModel(t)
	m = selectAction(t, m, "delete")
	mi, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, mi)
	if res, ok := adminResult(cmd); !ok || len(res.track) != 0 {
		t.Fatalf("a delete starts no rollout: %+v", res)
	}
	if !m.busy() || !m.animOn {
		t.Fatal("a confirmed mutation must show as in flight")
	}
	if line := plain(m.statusLine()); !strings.Contains(line, "DELETE Deployment/back") || !strings.ContainsAny(line, "⣾⣽⣻⢿⡿⣟⣯⣷") {
		t.Fatalf("status line must spin on the in-flight mutation: %q", line)
	}
	mi, _ = m.Update(adminMsg{summary: "Deployment/back deleted"})
	m = asModel(t, mi)
	if m.busy() || m.statusMsg != "✓ Deployment/back deleted" {
		t.Fatalf("the result must replace the spinner (busy=%v status=%q)", m.busy(), m.statusMsg)
	}
}

// TestRestartTracksRollout: a confirmed rolling restart follows the
// workload's rollout on the status line with the API's own counts, then
// reports the completion.
func TestRestartTracksRollout(t *testing.T) {
	m := adminModel(t)
	m = selectAction(t, m, "restart")
	mi, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, mi)
	res, ok := adminResult(cmd)
	if !ok || res.err != nil || len(res.track) != 1 || res.track[0].key() != "Deployment/demo/back" {
		t.Fatalf("restart must carry its rollout target: %+v", res)
	}
	mi, _ = m.Update(res)
	m = asModel(t, mi)
	if len(m.rollouts) != 1 {
		t.Fatal("the restart's rollout must be tracked")
	}
	if line := plain(m.statusLine()); !strings.Contains(line, "Deployment/back rolling out") || !strings.Contains(line, "waiting for the controller") {
		t.Fatalf("before the first GET the line must not invent progress: %q", line)
	}

	key := "Deployment/demo/back"
	mi, cmd = m.Update(rolloutMsg{key: key, ok: true,
		prog: kube.RolloutProgress{Desired: 3, Updated: 1, Ready: 3, Observed: true}})
	m = asModel(t, mi)
	if cmd == nil || len(m.rollouts) != 1 {
		t.Fatal("an unfinished rollout keeps polling")
	}
	if line := plain(m.statusLine()); !strings.Contains(line, "1/3 updated · 3 ready") || !strings.Contains(line, "█") {
		t.Fatalf("progress line must show the real counts and the bar: %q", line)
	}

	mi, _ = m.Update(rolloutMsg{key: key, ok: true,
		prog: kube.RolloutProgress{Desired: 3, Updated: 3, Ready: 3, Observed: true, Done: true}})
	m = asModel(t, mi)
	if len(m.rollouts) != 0 || !strings.Contains(m.statusMsg, "✓ Deployment/back rolled out — 3/3 ready") {
		t.Fatalf("completion must end tracking and say so (status=%q)", m.statusMsg)
	}
}

func TestScaleTracksRollout(t *testing.T) {
	m := adminModel(t)
	m = selectAction(t, m, "scale")
	mi, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = asModel(t, mi)
	m = pressRune(t, m, '5')
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res, ok := adminResult(cmd); !ok || len(res.track) != 1 {
		t.Fatalf("scale must carry its rollout target: %+v", res)
	}
}

func TestRolloutFailuresSurface(t *testing.T) {
	m := adminModel(t)
	key := "Deployment/demo/back"
	track := func() Model {
		mm := m
		mm.rollouts = map[string]rolloutTrack{key: {target: rolloutTarget{t: deploymentsType, ns: "demo", name: "back"}, started: time.Now()}}
		return mm
	}

	mi, _ := track().Update(rolloutMsg{key: key, ok: true,
		prog: kube.RolloutProgress{Desired: 2, Updated: 1, Ready: 1, Observed: true, Failed: "ProgressDeadlineExceeded"}})
	got := asModel(t, mi)
	if len(got.rollouts) != 0 || !strings.Contains(got.errMsg, "stalled: ProgressDeadlineExceeded") {
		t.Fatalf("a stalled rollout must surface the controller's reason (err=%q)", got.errMsg)
	}

	mi, _ = track().Update(rolloutMsg{key: key, err: errors.New("forbidden")})
	got = asModel(t, mi)
	if len(got.rollouts) != 0 || !strings.Contains(got.errMsg, "status unavailable — forbidden") {
		t.Fatalf("an unreadable status must be said, never guessed (err=%q)", got.errMsg)
	}

	old := track()
	tr := old.rollouts[key]
	tr.started = time.Now().Add(-rolloutMax - time.Second)
	old.rollouts[key] = tr
	mi, _ = old.Update(rolloutMsg{key: key, ok: true, prog: kube.RolloutProgress{Desired: 2, Updated: 1, Observed: true}})
	got = asModel(t, mi)
	if len(got.rollouts) != 0 || !strings.Contains(got.statusMsg, "tracking stopped") {
		t.Fatalf("tracking must give up loudly past rolloutMax (status=%q)", got.statusMsg)
	}
}

// TestRolloutPollReadsTheObject: the poll is a real GET evaluated by
// kube.RolloutStatus — no counts come from anywhere else.
func TestRolloutPollReadsTheObject(t *testing.T) {
	m := adminModel(t)
	msg, ok := m.pollRollout(rolloutTarget{t: deploymentsType, ns: "demo", name: "back"}, 0)().(rolloutMsg)
	if !ok || msg.err != nil || !msg.ok || msg.prog.Desired != 3 {
		t.Fatalf("poll must GET the workload and evaluate it: %+v", msg)
	}
}

// TestAnimPrimitivesKeepWidth: every frame of every primitive has the same
// width (an animated cell never shifts the layout), and the motion is
// carried by glyphs, not color alone (NO_COLOR).
func TestAnimPrimitivesKeepWidth(t *testing.T) {
	th := theme.Default()
	for f := 0; f < 80; f++ {
		if w := xansi.StringWidth(th.Spinner(f)); w != 1 {
			t.Fatalf("spinner frame %d width %d", f, w)
		}
		if w := xansi.StringWidth(th.Comet(30, f)); w != 30 {
			t.Fatalf("comet frame %d width %d", f, w)
		}
		for _, frac := range []float64{0, 0.07, 0.5, 0.99, 1} {
			if w := xansi.StringWidth(th.Bar(20, frac, f)); w != 20 {
				t.Fatalf("bar frame %d frac %v width %d", f, frac, w)
			}
		}
	}
	if a, b := plain(th.Comet(30, 0)), plain(th.Comet(30, 5)); a == b {
		t.Fatal("the comet must move without color")
	}
	if got := strings.Count(plain(th.Bar(20, 0.5, 0)), "█"); got != 10 {
		t.Fatalf("half a 20-cell bar must fill 10 cells, got %d", got)
	}
}
