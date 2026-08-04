package ui

// Hierarchical drill-down (owner request 2026-07-31, k9s-like): Enter walks
// DOWN the ownership/routing chain — Deployment → Pods → Containers,
// CronJob → Jobs → Pods → Containers, Ingress → Services → Pods,
// Node → Pods, Namespace → Pods. Esc pops one level at a time (drillStack).
// Kinds with no child open the YAML detail, as before.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// drillBy is how a level selects its children.
type drillBy int

const (
	bySelector  drillBy = iota // controller/service label selector → pods
	byOwner                    // ownerReferences UID → e.g. a CronJob's Jobs
	byNode                     // pods scheduled on a node
	byNamespace                // everything in a namespace (scope switch)
	byNames                    // an explicit name allow-list (Ingress → Services)
)

// drillStep is one edge of the chain: the child type and how to select it.
type drillStep struct {
	childKey string             // resource type key, e.g. "v1/pods"
	fallback model.ResourceType // used when discovery did not return childKey
	by       drillBy
}

var (
	podsType     = model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	jobsType     = model.ResourceType{Group: "batch", Version: "v1", Kind: "Job", Resource: "jobs", Namespaced: true}
	servicesType = model.ResourceType{Version: "v1", Kind: "Service", Resource: "services", Namespaced: true}
)

// drillChain maps a kind (lowercased) to its child step. Pods are special —
// they drill into their containers, a different screen (see openContainers).
var drillChain = map[string]drillStep{
	"deployment":  {childKey: "v1/pods", fallback: podsType, by: bySelector},
	"statefulset": {childKey: "v1/pods", fallback: podsType, by: bySelector},
	"daemonset":   {childKey: "v1/pods", fallback: podsType, by: bySelector},
	"replicaset":  {childKey: "v1/pods", fallback: podsType, by: bySelector},
	"job":         {childKey: "v1/pods", fallback: podsType, by: bySelector},
	"service":     {childKey: "v1/pods", fallback: podsType, by: bySelector},
	"cronjob":     {childKey: "batch/v1/jobs", fallback: jobsType, by: byOwner},
	"node":        {childKey: "v1/pods", fallback: podsType, by: byNode},
	"namespace":   {childKey: "v1/pods", fallback: podsType, by: byNamespace},
	"ingress":     {childKey: "v1/services", fallback: servicesType, by: byNames},
}

// drillFrame remembers a level so Esc can restore it exactly.
type drillFrame struct {
	typ       model.ResourceType
	selector  string
	node      string
	ownerUID  string
	names     map[string]bool
	label     string
	namespace string // query scope of the drill (not the user's ns filter)
	nsScope   string // the user's namespace filter (byNamespace changes it)
	filter    string // the '/' filter of the level we are leaving
	// parent is the object this level was opened FROM (its CronJob, its
	// Deployment…) with its type — the actions palette offers the parent's
	// own actions from inside the child list (owner report 2026-08-03: 'a'
	// in CronJob → Jobs could not trigger the CronJob).
	parent     model.ResourceObject
	parentType model.ResourceType
}

// drilling reports whether the list is showing a drilled level.
func (m *Model) drilling() bool { return len(m.drillStack) > 0 }

// drillChildLabel renders the current level for the header chip.
func (m *Model) drillChildLabel() string {
	return m.curType.Resource + " ⊂ " + m.drillFor
}

// drillInto is the Enter action on a list row: open the selection's children
// (one level down) or, for a pod, its containers. ok=false when the kind has
// no child — the caller then opens the YAML detail.
func (m *Model) drillInto() (tea.Cmd, bool) {
	obj, found := m.selectedObject()
	if !found {
		return nil, false
	}
	// Pods are the leaf of the resource chain: they drill into containers.
	if strings.EqualFold(m.curType.Kind, "Pod") {
		return m.openContainers(obj), true
	}
	step, ok := drillChain[strings.ToLower(m.curType.Kind)]
	if !ok {
		return nil, false
	}
	child, okType := findTypeByKey(m.types, step.childKey)
	if !okType {
		child = step.fallback
	}
	// Snapshot the level we are leaving so Esc restores it exactly.
	frame := drillFrame{
		typ: m.curType, selector: m.drillSelector, node: m.drillNode,
		ownerUID: m.drillOwnerUID, names: m.drillNames, label: m.drillFor,
		namespace: m.drillNamespace, nsScope: m.client.Namespace,
		filter: m.filter.Value(),
	}
	label := m.curType.Kind + "/" + obj.Name
	// A fresh level starts unfiltered and unscoped by the previous one, and
	// remembers the object it was opened from as its parent.
	next := drillFrame{typ: child, label: label, namespace: obj.Namespace,
		parent: obj, parentType: m.curType}

	switch step.by {
	case bySelector:
		sel, ok := kube.PodSelector(obj.Raw)
		if !ok {
			return nil, false
		}
		next.selector = sel
	case byOwner:
		uid := kube.ObjectUID(obj.Raw)
		if uid == "" {
			m.statusMsg = label + ": no UID on the object — cannot list its children"
			return nil, false
		}
		next.ownerUID = uid
	case byNode:
		next.node = obj.Name
		next.namespace = "" // node pods span every namespace
	case byNamespace:
		// The only drill that moves the user's namespace scope; Esc restores it.
		m.client.Namespace = obj.Name
		next.namespace = ""
	case byNames:
		names := kube.IngressServiceNames(obj.Raw)
		if len(names) == 0 {
			m.statusMsg = label + ": no backend service to open"
			return nil, false
		}
		next.names = map[string]bool{}
		for _, n := range names {
			next.names[n] = true
		}
	}

	m.drillStack = append(m.drillStack, frame)
	m.applyDrillFrame(next)
	m.filter.SetValue("") // a child list starts unfiltered
	m.marked = map[string]model.ResourceObject{}
	m.statusMsg = child.Resource + " of " + label + " — Esc to go back"
	if m.metrics.Enabled() && strings.EqualFold(child.Kind, "Pod") {
		return tea.Batch(m.listObjects(), m.fetchListUsage()), true
	}
	return m.listObjects(), true
}

// applyDrillFrame makes a frame the current level.
func (m *Model) applyDrillFrame(f drillFrame) {
	m.curType = f.typ
	m.drillSelector, m.drillNode = f.selector, f.node
	m.drillOwnerUID, m.drillNames = f.ownerUID, f.names
	m.drillFor, m.drillNamespace = f.label, f.namespace
	m.drillParent, m.drillParentType = f.parent, f.parentType
}

// exitDrill pops one level (Esc). The list returns to the parent exactly as
// it was — type, scope and its own '/' filter.
func (m *Model) exitDrill() tea.Cmd {
	if !m.drilling() {
		return nil
	}
	f := m.drillStack[len(m.drillStack)-1]
	m.drillStack = m.drillStack[:len(m.drillStack)-1]
	m.applyDrillFrame(f)
	m.client.Namespace = f.nsScope
	m.filter.SetValue(f.filter)
	m.marked = map[string]model.ResourceObject{}
	m.statusMsg = ""
	if m.metrics.Enabled() && strings.EqualFold(f.typ.Kind, "Pod") {
		return tea.Batch(m.listObjects(), m.fetchListUsage())
	}
	return m.listObjects()
}

// resetDrill drops the whole chain (type switch, namespace change, reset).
func (m *Model) resetDrill() {
	m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
	m.drillOwnerUID, m.drillNames = "", nil
	m.drillParent, m.drillParentType = model.ResourceObject{}, model.ResourceType{}
	m.drillStack = nil
}
