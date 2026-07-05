package kube

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

var nodesGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}

const unscheduledNode = "(unscheduled)"

// Topology returns the cluster's nodes with the pods scheduled on each (US4,
// read-only). Pods are scoped to `namespace` (empty → all namespaces). Node
// health is derived from Ready + pressure conditions. Pods with no assigned
// node are grouped under a synthetic "(unscheduled)" node.
func (c *Client) Topology(ctx context.Context, namespace string) ([]model.TopologyNode, error) {
	nodeList, err := c.Dynamic.Resource(nodesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	podRI := c.Dynamic.Resource(podsGVR)
	var podList *unstructured.UnstructuredList
	if namespace != "" {
		podList, err = podRI.Namespace(namespace).List(ctx, metav1.ListOptions{})
	} else {
		podList, err = podRI.List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	// Group pods by their spec.nodeName, computing each pod's requests.
	byNode := map[string][]model.TopologyPod{}
	reqCPU := map[string]float64{}
	reqMem := map[string]float64{}
	for i := range podList.Items {
		p := &podList.Items[i]
		nodeName, _, _ := unstructured.NestedString(p.Object, "spec", "nodeName")
		if nodeName == "" {
			nodeName = unscheduledNode
		}
		cpuR, _, memR, _ := PodResources(p.Object)
		byNode[nodeName] = append(byNode[nodeName], model.TopologyPod{
			Namespace: p.GetNamespace(),
			Name:      p.GetName(),
			Status:    deriveStatus(p).Level,
			CPUReq:    cpuR,
			MemReq:    memR,
		})
		reqCPU[nodeName] += cpuR
		reqMem[nodeName] += memR
	}

	out := make([]model.TopologyNode, 0, len(nodeList.Items)+1)
	for i := range nodeList.Items {
		n := &nodeList.Items[i]
		name := n.GetName()
		level, reason := nodeHealth(n)
		allocCPU, allocMem := nodeAllocatable(n)
		pods := byNode[name]
		sortPodsByFootprint(pods, allocCPU, allocMem)
		out = append(out, model.TopologyNode{
			Name: name, Status: level, Reason: reason,
			AllocCPU: allocCPU, AllocMem: allocMem,
			ReqCPU: reqCPU[name], ReqMem: reqMem[name],
			Pods: pods,
		})
		delete(byNode, name)
	}
	// Any remaining groups (unscheduled, or pods on unknown nodes).
	for name, pods := range byNode {
		sortPodsByFootprint(pods, 0, 0)
		out = append(out, model.TopologyNode{
			Name: name, Status: model.HealthWarning, Reason: "unscheduled",
			ReqCPU: reqCPU[name], ReqMem: reqMem[name], Pods: pods,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// nodeHealth derives a node's display health from its conditions.
func nodeHealth(n *unstructured.Unstructured) (model.HealthLevel, string) {
	conds, _, _ := unstructured.NestedSlice(n.Object, "status", "conditions")
	ready := false
	pressure := ""
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		ctype, _ := cm["type"].(string)
		cstatus, _ := cm["status"].(string)
		switch ctype {
		case "Ready":
			ready = cstatus == "True"
		case "MemoryPressure", "DiskPressure", "PIDPressure":
			if cstatus == "True" {
				pressure = ctype
			}
		}
	}
	switch {
	case !ready:
		return model.HealthError, "NotReady"
	case pressure != "":
		return model.HealthWarning, pressure
	default:
		return model.HealthOk, "Ready"
	}
}

// nodeAllocatable reads a node's allocatable CPU (cores) and memory (bytes).
func nodeAllocatable(n *unstructured.Unstructured) (cpu, mem float64) {
	alloc, _, _ := unstructured.NestedStringMap(n.Object, "status", "allocatable")
	return parseQuantity(alloc["cpu"], true), parseQuantity(alloc["memory"], false)
}

// sortPodsByFootprint orders pods so the biggest consumers of the node come
// first — answering "which pod fills the node". The footprint score is the
// larger of the pod's CPU or memory share of the node's allocatable; when
// allocatable is unknown, it falls back to raw requests (CPU-weighted).
func sortPodsByFootprint(pods []model.TopologyPod, allocCPU, allocMem float64) {
	score := func(p model.TopologyPod) float64 {
		if allocCPU > 0 || allocMem > 0 {
			var c, m float64
			if allocCPU > 0 {
				c = p.CPUReq / allocCPU
			}
			if allocMem > 0 {
				m = p.MemReq / allocMem
			}
			if c > m {
				return c
			}
			return m
		}
		return p.CPUReq*1e9 + p.MemReq
	}
	sort.SliceStable(pods, func(i, j int) bool {
		si, sj := score(pods[i]), score(pods[j])
		if si != sj {
			return si > sj
		}
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
}
