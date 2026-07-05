// Package kube is the read-only Kubernetes access layer. It only ever issues
// read-oriented verbs (list/get/watch and pods/log). No create/update/patch/
// delete/exec code path exists here (FR-012, FR-018, SC-006).
package kube

import (
	"fmt"
	"sort"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Client bundles the read-only clients built from a kubeconfig context.
type Client struct {
	Dynamic    dynamic.Interface
	Discovery  discovery.DiscoveryInterface
	Clientset  kubernetes.Interface
	restConfig *rest.Config

	kubeconfigPath string
	contextName    string
	Namespace      string
}

// Options configure how the client connects.
type Options struct {
	KubeconfigPath string // explicit path; empty → default loading rules
	Context        string // context name; empty → current-context
	Namespace      string // starting namespace; empty → context default
}

// NewClient builds read-only clients for the selected context.
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
	disc, err := discovery.NewDiscoveryClientForConfig(restCfg)
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
