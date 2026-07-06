package kube

import (
	"bufio"
	"sync"

	"context"
	"github.com/iadvize/idz-k8s/internal/model"

	corev1 "k8s.io/api/core/v1"
)

// LogLine is a single streamed log line (or a terminal error). Pod names the
// source pod in merged multi-pod streams ("" for single-pod streams) so the UI
// can render a per-pod colored prefix.
type LogLine struct {
	Pod  string
	Text string
	Err  error
	Done bool
}

// StreamPodLogs follows a pod's logs (read-only "get" on pods/log, FR-005).
// Lines are delivered on the returned channel until ctx is cancelled or the
// stream ends. tailLines bounds the initial backlog.
func (c *Client) StreamPodLogs(ctx context.Context, namespace, pod, container string, tailLines int64, follow bool) <-chan LogLine {
	out := make(chan LogLine)
	go func() {
		defer close(out)
		opts := &corev1.PodLogOptions{Follow: follow}
		if container != "" {
			opts.Container = container
		}
		if tailLines > 0 {
			opts.TailLines = &tailLines
		}
		req := c.Clientset.CoreV1().Pods(namespace).GetLogs(pod, opts)
		stream, err := req.Stream(ctx)
		if err != nil {
			out <- LogLine{Err: err, Done: true}
			return
		}
		defer func() { _ = stream.Close() }()
		sc := bufio.NewScanner(stream)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case <-ctx.Done():
				out <- LogLine{Done: true}
				return
			case out <- LogLine{Text: sc.Text()}:
			}
		}
		if err := sc.Err(); err != nil {
			out <- LogLine{Err: err, Done: true}
			return
		}
		out <- LogLine{Done: true}
	}()
	return out
}

// StreamWorkloadLogs merges the live logs of every pod matching a label
// selector (FR-034): one channel, each line prefixed with its pod name. The
// pod set is resolved when the stream starts; the channel closes when all pod
// streams end or ctx is cancelled.
func (c *Client) StreamWorkloadLogs(ctx context.Context, namespace, labelSelector string, tailLines int64, follow bool) <-chan LogLine {
	out := make(chan LogLine)
	go func() {
		defer close(out)
		podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
		pods, err := c.ListSelected(ctx, podType, namespace, labelSelector)
		if err != nil {
			out <- LogLine{Err: err, Done: true}
			return
		}
		if len(pods) == 0 {
			out <- LogLine{Text: "(no pods match the selector)", Done: true}
			return
		}
		var wg sync.WaitGroup
		for _, p := range pods {
			wg.Add(1)
			go func(ns, name string) {
				defer wg.Done()
				for line := range c.StreamPodLogs(ctx, ns, name, "", tailLines, follow) {
					if line.Err != nil {
						select {
						case out <- LogLine{Pod: name, Text: "stream error: " + line.Err.Error()}:
						case <-ctx.Done():
						}
						return
					}
					if line.Text != "" {
						select {
						case out <- LogLine{Pod: name, Text: line.Text}:
						case <-ctx.Done():
							return
						}
					}
					if line.Done {
						return
					}
				}
			}(p.Namespace, p.Name)
		}
		wg.Wait()
		select {
		case out <- LogLine{Done: true}:
		case <-ctx.Done():
		}
	}()
	return out
}
