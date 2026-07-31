package kube

// Exec-into-pod (v3.4, owner request 2026-07-31 — this lifts the former
// out-of-scope note): a native SPDY exec with TTY, no kubectl binary
// needed. The UI suspends the TUI (tea.Exec) and hands the real terminal
// to the remote shell.

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecInPod runs an interactive command in a pod's container, streams
// attached to the given reader/writers (a raw-mode terminal in practice).
func (c *Client) ExecInPod(ctx context.Context, namespace, pod, container string, command []string,
	stdin io.Reader, stdout, stderr io.Writer, tty bool, resize remotecommand.TerminalSizeQueue) error {
	req := c.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    true,
			Stderr:    !tty, // a TTY merges stderr into stdout
			TTY:       tty,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("exec transport: %w", err)
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin: stdin, Stdout: stdout, Stderr: stderr, Tty: tty, TerminalSizeQueue: resize,
	})
}

// DefaultContainer picks the container a shell should land in: the
// kubectl.kubernetes.io/default-container annotation when set, the first
// declared container otherwise ("" when none — the API then picks).
func DefaultContainer(raw map[string]any) string {
	if name, _, _ := unstructured.NestedString(raw, "metadata", "annotations", "kubectl.kubernetes.io/default-container"); name != "" {
		return name
	}
	if cs, _, _ := unstructured.NestedSlice(raw, "spec", "containers"); len(cs) > 0 {
		if cm, ok := cs[0].(map[string]any); ok {
			name, _ := cm["name"].(string)
			return name
		}
	}
	return ""
}
