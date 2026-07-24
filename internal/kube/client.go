// Package kube is the Kubernetes access layer: discovery, lists (informer
// cache with direct-LIST fallback), logs, topology, diagnostics — plus the v3
// admin operations (admin.go, portforward.go), each of which the UI runs only
// after an explicit confirmation (FR-012 v3).
package kube

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Client bundles the clients built from a kubeconfig context.
type Client struct {
	Dynamic    dynamic.Interface
	Discovery  discovery.DiscoveryInterface
	Clientset  kubernetes.Interface
	restConfig *rest.Config

	kubeconfigPath string
	contextName    string
	Namespace      string

	// Shared informer cache behind the main list flow (T089); lazily built,
	// stopped via Close() when the client is replaced. changes carries the
	// coalesced live-refresh signal derived from watch events.
	infMu   sync.Mutex
	inf     *informerCache
	changes chan struct{}
}

// Options configure how the client connects.
type Options struct {
	KubeconfigPath string // explicit path; empty → default loading rules
	Context        string // context name; empty → current-context
	Namespace      string // starting namespace; empty → context default
}

// NewClient builds the clients for the selected context.
func NewClient(opts Options) (*Client, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.KubeconfigPath != "" {
		loading.ExplicitPath = opts.KubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if opts.Context != "" {
		overrides.CurrentContext = opts.Context
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides)

	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	// Server deprecation warnings must never be printed over the TUI.
	restCfg.WarningHandler = rest.NoWarnings{}
	// Posture/topology/diagnostics fan out several parallel LISTs; the
	// client-go defaults (5 QPS, burst 10) can throttle them past their
	// per-request timeouts on large clusters.
	restCfg.QPS = 50
	restCfg.Burst = 100

	// Namespace scope: empty means ALL namespaces (the tool's default overview
	// scope). The kubeconfig context's default namespace is intentionally NOT
	// applied — an overview tool starts wide; narrow with -n or the picker.
	ns := opts.Namespace

	// Resolve the active context name for display. When no explicit context was
	// requested, fall back to the kubeconfig's current-context.
	resolvedContext := opts.Context
	if resolvedContext == "" {
		if raw, rawErr := cc.RawConfig(); rawErr == nil {
			resolvedContext = raw.CurrentContext
		}
	}

	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("building dynamic client: %w", err)
	}
	// Discovery calls (ServerPreferredResources) take no context, so an
	// unresponsive apiserver would block startup forever without a
	// client-side timeout. Only discovery gets one: a Timeout on the shared
	// config would kill long-lived streams (log follow, informer watches).
	discCfg := rest.CopyConfig(restCfg)
	discCfg.Timeout = 30 * time.Second
	disc, err := discovery.NewDiscoveryClientForConfig(discCfg)
	if err != nil {
		return nil, fmt.Errorf("building discovery client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("building clientset: %w", err)
	}

	return &Client{
		Dynamic:        dyn,
		Discovery:      disc,
		Clientset:      cs,
		restConfig:     restCfg,
		kubeconfigPath: opts.KubeconfigPath,
		contextName:    resolvedContext,
		Namespace:      ns,
	}, nil
}

// ActiveContext returns the name of the currently active kubeconfig context.
func (c *Client) ActiveContext() string { return c.contextName }

// Contexts lists the kubeconfig contexts, marking the active one (FR-003).
func Contexts(kubeconfigPath, active string) ([]model.Context, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loading.ExplicitPath = kubeconfigPath
	}
	raw, err := loading.Load()
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig: %w", err)
	}
	current := raw.CurrentContext
	if active != "" {
		current = active
	}
	out := make([]model.Context, 0, len(raw.Contexts))
	for name, ctx := range raw.Contexts {
		out = append(out, model.Context{
			Name:      name,
			Cluster:   ctx.Cluster,
			Namespace: ctx.Namespace,
			Active:    name == current,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
