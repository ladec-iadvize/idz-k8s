package kube

// Port-forwarding (v3 admin): the same SPDY tunnel kubectl port-forward
// uses. Forwards live until stopped from the actions palette or app exit.

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward is one active local→pod tunnel.
type PortForward struct {
	Namespace string
	Pod       string
	Local     int
	Remote    int
	// For label: the resource the forward was started from (may be the pod
	// itself, or the workload/service that resolved to it).
	For string

	stopCh chan struct{}
	once   sync.Once
}

// Key identifies a forward (one per pod+port pair).
func (p *PortForward) Key() string {
	return fmt.Sprintf("%s/%s:%d", p.Namespace, p.Pod, p.Remote)
}

// Label renders the forward for status lines and the actions palette.
func (p *PortForward) Label() string {
	return fmt.Sprintf("localhost:%d → %s/%s:%d", p.Local, p.Namespace, p.Pod, p.Remote)
}

// Stop tears the tunnel down (idempotent).
func (p *PortForward) Stop() { p.once.Do(func() { close(p.stopCh) }) }

// ForwardPort opens a tunnel from localhost:local to pod:remote and returns
// once it is ready (or failed). local 0 lets the OS pick a free port.
func (c *Client) ForwardPort(namespace, pod string, local, remote int) (*PortForward, error) {
	req := c.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("portforward")
	transport, upgrader, err := spdy.RoundTripperFor(c.restConfig)
	if err != nil {
		return nil, fmt.Errorf("port-forward transport: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", local, remote)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("port-forward setup: %w", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()
	select {
	case <-readyCh:
	case err := <-errCh:
		return nil, fmt.Errorf("port-forward to %s/%s: %w", namespace, pod, err)
	case <-time.After(10 * time.Second):
		close(stopCh)
		return nil, fmt.Errorf("port-forward to %s/%s: timed out", namespace, pod)
	}
	if local == 0 {
		if ports, perr := fw.GetPorts(); perr == nil && len(ports) > 0 {
			local = int(ports[0].Local)
		}
	}
	return &PortForward{Namespace: namespace, Pod: pod, Local: local, Remote: remote, stopCh: stopCh}, nil
}
