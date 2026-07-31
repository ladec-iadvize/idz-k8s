package ui

// Shell-into-pod (owner request 2026-07-31): the actions palette's "shell"
// entry suspends the TUI (tea.Exec) and attaches the real terminal to a
// remote shell — bash when the image has it, sh otherwise. Workloads and
// services resolve to their first ready pod, like port-forward.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// shellCommand tries bash first and falls back to sh (BusyBox/distroless-ish
// images rarely carry bash).
var shellCommand = []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}

// podShell implements tea.ExecCommand: Run() executes while the TUI has
// released the terminal, so it can go raw and stream the remote TTY.
type podShell struct {
	cl  *kube.Client
	t   model.ResourceType
	obj model.ResourceObject

	stdin          io.Reader
	stdout, stderr io.Writer
}

func (s *podShell) SetStdin(r io.Reader)  { s.stdin = r }
func (s *podShell) SetStdout(w io.Writer) { s.stdout = w }
func (s *podShell) SetStderr(w io.Writer) { s.stderr = w }

func (s *podShell) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Resolve the target pod (the object itself, or the first ready pod
	// behind a workload/service selector) and its default container.
	pod, container := s.obj.Name, kube.DefaultContainer(s.obj.Raw)
	if !strings.EqualFold(s.t.Kind, "Pod") {
		sel, ok := kube.PodSelector(s.obj.Raw)
		if !ok {
			return fmt.Errorf("%s/%s has no pod selector to shell into", s.t.Kind, s.obj.Name)
		}
		name, err := s.cl.FirstReadyPod(ctx, s.obj.Namespace, sel)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", s.t.Kind, s.obj.Name, err)
		}
		pod, container = name, ""
	}

	// Raw mode on the real terminal (arrow keys, ctrl-c reach the remote
	// shell); restored on exit. Skipped when stdin is not a TTY.
	var resize remotecommand.TerminalSizeQueue
	if f, ok := s.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		state, err := term.MakeRaw(int(f.Fd()))
		if err == nil {
			defer func() { _ = term.Restore(int(f.Fd()), state) }()
		}
		resize = newResizeQueue(ctx, int(f.Fd()))
	}
	_, _ = fmt.Fprintf(s.stdout, "⇢ shell into %s/%s — exit or ctrl-d to return to idz-k8s\r\n", s.obj.Namespace, pod)
	return s.cl.ExecInPod(ctx, s.obj.Namespace, pod, container, shellCommand,
		s.stdin, s.stdout, s.stderr, true, resize)
}

// resizeQueue feeds the remote TTY the local terminal size: once at start,
// then on every SIGWINCH.
type resizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func newResizeQueue(ctx context.Context, fd int) *resizeQueue {
	q := &resizeQueue{ch: make(chan remotecommand.TerminalSize, 1)}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	push := func() {
		if w, h, err := term.GetSize(fd); err == nil {
			select {
			case q.ch <- remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}:
			default:
			}
		}
	}
	push()
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-ctx.Done():
				close(q.ch)
				return
			case <-sig:
				push()
			}
		}
	}()
	return q
}

func (q *resizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

// openShell hands the terminal to a remote shell in the selection.
func (m Model) openShell(obj model.ResourceObject) (tea.Model, tea.Cmd) {
	label := m.curType.Kind + "/" + obj.Name
	sh := &podShell{cl: m.client, t: m.curType, obj: obj}
	return m, tea.Exec(sh, func(err error) tea.Msg {
		if err != nil && !strings.Contains(err.Error(), "terminated with exit code") {
			// Real failures (unreachable, no shell binary) — a non-zero exit
			// typed by the user is not an error worth a banner.
			return adminMsg{summary: "shell " + label, err: err}
		}
		return adminMsg{summary: "shell closed — " + label}
	})
}
