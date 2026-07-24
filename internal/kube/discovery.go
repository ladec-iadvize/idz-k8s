package kube

import (
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

// builtinGroups is a small allowlist used only to flag CRDs vs built-ins for
// display; discovery itself returns every served type including CRDs (FR-002).
var builtinGroups = map[string]bool{
	"":                             true,
	"apps":                         true,
	"batch":                        true,
	"networking.k8s.io":            true,
	"rbac.authorization.k8s.io":    true,
	"storage.k8s.io":               true,
	"policy":                       true,
	"autoscaling":                  true,
	"apiextensions.k8s.io":         true,
	"coordination.k8s.io":          true,
	"discovery.k8s.io":             true,
	"events.k8s.io":                true,
	"authentication.k8s.io":        true,
	"authorization.k8s.io":         true,
	"admissionregistration.k8s.io": true,
	"scheduling.k8s.io":            true,
	"node.k8s.io":                  true,
	"certificates.k8s.io":          true,
}

// ResourceTypes discovers every listable resource type served by the cluster,
// including CRDs (FR-002). Only types supporting "list" are returned, since the
// tool browses by listing.
func (c *Client) ResourceTypes() ([]model.ResourceType, error) {
	lists, err := c.Discovery.ServerPreferredResources()
	// ServerPreferredResources can return partial results with an error when
	// some group is unavailable; we use what we got rather than failing hard.
	out := ParseResourceTypes(lists)
	if len(out) > 0 {
		return out, nil
	}
	return out, err
}

// ParseResourceTypes turns discovery output into browsable resource types. It is
// pure (no client) so the CRD/subresource/verb filtering is unit-testable.
// Only types supporting "list" are kept, since the tool browses by
// listing; subresources (e.g. pods/log) are skipped.
func ParseResourceTypes(lists []*metav1.APIResourceList) []model.ResourceType {
	var out []model.ResourceType
	for _, list := range lists {
		if list == nil {
			continue
		}
		gv, gvErr := schema.ParseGroupVersion(list.GroupVersion)
		if gvErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // skip subresources (e.g. pods/log)
			}
			if !canList(r.Verbs) {
				continue
			}
			out = append(out, model.ResourceType{
				Group:      gv.Group,
				Version:    gv.Version,
				Kind:       r.Kind,
				Resource:   r.Name,
				Namespaced: r.Namespaced,
				IsCRD:      !builtinGroups[gv.Group],
				ShortNames: r.ShortNames,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Resource < out[j].Resource
	})
	return out
}

func canList(verbs []string) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}

// gvr builds the schema.GroupVersionResource for a resource type.
func gvr(t model.ResourceType) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: t.Group, Version: t.Version, Resource: t.Resource}
}
