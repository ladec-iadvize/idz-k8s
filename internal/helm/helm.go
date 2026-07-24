// Package helm reads Helm release state (US12) — installed releases and
// their revision history, straight from Helm's in-cluster storage — and
// carries the v3 admin actions (rollback, uninstall), each behind an
// explicit confirmation in the UI.
package helm

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/iadvize/idz-k8s/internal/model"
)

// ConfigProvider builds an action.Configuration scoped to a namespace ("" =
// all namespaces for List). Injected so tests can use an in-memory storage.
type ConfigProvider func(namespace string) (*action.Configuration, error)

// Client reads Helm releases.
type Client struct {
	provide ConfigProvider
}

// New builds a Client reading the cluster's Helm storage via the given
// kubeconfig/context (same credentials as everything else, read access only).
func New(kubeconfigPath, contextName string) *Client {
	return &Client{provide: func(namespace string) (*action.Configuration, error) {
		flags := genericclioptions.NewConfigFlags(false)
		if kubeconfigPath != "" {
			flags.KubeConfig = &kubeconfigPath
		}
		if contextName != "" {
			flags.Context = &contextName
		}
		if namespace != "" {
			flags.Namespace = &namespace
		}
		cfg := new(action.Configuration)
		// "secret" is Helm's default release storage driver.
		if err := cfg.Init(flags, namespace, "secret", func(string, ...interface{}) {}); err != nil {
			return nil, fmt.Errorf("initializing helm storage access: %w", err)
		}
		return cfg, nil
	}}
}

// NewWithProvider is the test seam: inject a ready-made configuration
// (e.g. backed by Helm's in-memory storage driver).
func NewWithProvider(p ConfigProvider) *Client { return &Client{provide: p} }

// Releases lists installed releases (namespace "" = all namespaces): the
// latest revision of each release, sorted by namespace/name. Reads Helm's
// release storage directly — no Helm action is involved, so nothing can
// mutate anything.
func (c *Client) Releases(namespace string) ([]model.HelmRelease, error) {
	cfg, err := c.provide(namespace)
	if err != nil {
		return nil, err
	}
	rels, err := cfg.Releases.ListReleases()
	if err != nil {
		return nil, fmt.Errorf("listing helm releases: %w", err)
	}
	// Keep the latest revision per namespace/name.
	latest := map[string]*release.Release{}
	for _, r := range rels {
		if namespace != "" && r.Namespace != namespace {
			continue
		}
		key := r.Namespace + "/" + r.Name
		if cur, ok := latest[key]; !ok || r.Version > cur.Version {
			latest[key] = r
		}
	}
	out := make([]model.HelmRelease, 0, len(latest))
	for _, r := range latest {
		hr := model.HelmRelease{
			Name:      r.Name,
			Namespace: r.Namespace,
			Revision:  r.Version,
		}
		if r.Chart != nil && r.Chart.Metadata != nil {
			hr.Chart = r.Chart.Metadata.Name
			hr.ChartVersion = r.Chart.Metadata.Version
			hr.AppVersion = r.Chart.Metadata.AppVersion
		}
		if r.Info != nil {
			hr.Status = r.Info.Status.String()
			hr.Updated = r.Info.LastDeployed.Time
		}
		out = append(out, hr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// History returns a release's revisions, most recent first. Reads the release
// storage directly (read-only).
func (c *Client) History(namespace, name string) ([]model.HelmRevision, error) {
	cfg, err := c.provide(namespace)
	if err != nil {
		return nil, err
	}
	rels, err := cfg.Releases.History(name)
	if err != nil {
		return nil, fmt.Errorf("reading history of %s/%s: %w", namespace, name, err)
	}
	return revisionsOf(rels, namespace), nil
}

// revisionsOf maps stored releases to model revisions, most recent first
// (shared by History and Detail).
func revisionsOf(rels []*release.Release, namespace string) []model.HelmRevision {
	out := make([]model.HelmRevision, 0, len(rels))
	for _, r := range rels {
		if namespace != "" && r.Namespace != namespace {
			continue
		}
		rev := model.HelmRevision{Revision: r.Version}
		if r.Info != nil {
			rev.Status = r.Info.Status.String()
			rev.Updated = r.Info.LastDeployed.Time
			rev.Description = r.Info.Description
		}
		out = append(out, rev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	return out
}

// ReleaseDetail is everything the release detail view shows: revision history,
// the resources the chart deployed (from the rendered manifest), and the
// user-supplied values. All read straight from Helm's release storage.
type ReleaseDetail struct {
	History   []model.HelmRevision
	Resources []model.HelmResource
	Values    string // YAML of user-supplied values; "" when none
}

// Detail loads a release's history, deployed resources, and values (read-only).
func (c *Client) Detail(namespace, name string) (ReleaseDetail, error) {
	cfg, err := c.provide(namespace)
	if err != nil {
		return ReleaseDetail{}, err
	}
	rels, err := cfg.Releases.History(name)
	if err != nil {
		return ReleaseDetail{}, fmt.Errorf("reading %s/%s: %w", namespace, name, err)
	}
	det := ReleaseDetail{History: revisionsOf(rels, namespace)}
	var latest *release.Release
	for _, r := range rels {
		if namespace != "" && r.Namespace != namespace {
			continue
		}
		if latest == nil || r.Version > latest.Version {
			latest = r
		}
	}
	if latest == nil {
		return det, fmt.Errorf("release %s/%s not found", namespace, name)
	}
	det.Resources = parseManifestResources(latest.Manifest)
	if len(latest.Config) > 0 {
		if data, err := yaml.Marshal(latest.Config); err == nil {
			det.Values = string(data)
		}
	}
	return det, nil
}

// Rollback rolls a release back (v3 admin action, UI-confirmed).
// revision 0 targets the previous revision, Helm's default.
func (c *Client) Rollback(namespace, name string, revision int) error {
	cfg, err := c.provide(namespace)
	if err != nil {
		return err
	}
	rb := action.NewRollback(cfg)
	rb.Version = revision
	rb.Wait = false
	if err := rb.Run(name); err != nil {
		return fmt.Errorf("rolling back %s/%s: %w", namespace, name, err)
	}
	return nil
}

// Uninstall removes a release (v3 admin action, UI-confirmed).
func (c *Client) Uninstall(namespace, name string) error {
	cfg, err := c.provide(namespace)
	if err != nil {
		return err
	}
	un := action.NewUninstall(cfg)
	if _, err := un.Run(name); err != nil {
		return fmt.Errorf("uninstalling %s/%s: %w", namespace, name, err)
	}
	return nil
}

// parseManifestResources extracts the objects (apiVersion/kind/name) from a
// rendered multi-document manifest. Documents without kind+name are skipped.
func parseManifestResources(manifest string) []model.HelmResource {
	var out []model.HelmResource
	for _, doc := range strings.Split(manifest, "\n---") {
		var head struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(doc), &head); err != nil {
			continue
		}
		if head.Kind == "" || head.Metadata.Name == "" {
			continue
		}
		out = append(out, model.HelmResource{
			APIVersion: head.APIVersion,
			Kind:       head.Kind,
			Namespace:  head.Metadata.Namespace,
			Name:       head.Metadata.Name,
		})
	}
	return out
}
