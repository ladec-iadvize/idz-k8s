// Package model holds toolkit-agnostic domain types consumed by the UI.
// These types are independent of client-go and Bubble Tea so the data layers
// and the UI can be tested and evolved separately (constitution Principle II).
package model

import (
	"strings"
	"time"
)

// HealthLevel drives status color coding, with a non-color fallback (FR-020).
type HealthLevel int

const (
	HealthUnknown HealthLevel = iota
	HealthOk
	HealthWarning
	HealthError
)

// Symbol returns a non-color glyph for the health level (FR-020 fallback).
func (h HealthLevel) Symbol() string {
	switch h {
	case HealthOk:
		return "✓"
	case HealthWarning:
		return "!"
	case HealthError:
		return "✗"
	default:
		return "?"
	}
}

// Label returns a short textual label for the health level.
func (h HealthLevel) Label() string {
	switch h {
	case HealthOk:
		return "OK"
	case HealthWarning:
		return "Warning"
	case HealthError:
		return "Error"
	default:
		return "Unknown"
	}
}

// StatusSummary is a display-oriented health summary (FR-020).
type StatusSummary struct {
	Level  HealthLevel
	Reason string
}

// ResourceType is a discovered API resource type (built-in or CRD, FR-002).
type ResourceType struct {
	Group      string
	Version    string
	Kind       string
	Resource   string // plural wire name, e.g. "pods"
	Namespaced bool
	IsCRD      bool
}

// Key returns a stable identifier: <group>/<version>/<resource> (core group empty).
func (t ResourceType) Key() string {
	if t.Group == "" {
		return t.Version + "/" + t.Resource
	}
	return t.Group + "/" + t.Version + "/" + t.Resource
}

// ResourceObject is a single instance of a ResourceType (read-only projection).
type ResourceObject struct {
	Type      ResourceType
	Namespace string
	Name      string
	Status    StatusSummary
	CreatedAt time.Time
	// Raw is the full unstructured object (map form) for the detail view.
	Raw map[string]interface{}
}

// Context is a named kubeconfig context (single active at a time, FR-003).
type Context struct {
	Name      string
	Cluster   string
	Namespace string
	Active    bool
}

// MetricKind identifies a usage metric.
type MetricKind int

const (
	MetricCPU MetricKind = iota
	MetricMemory
)

func (k MetricKind) String() string {
	if k == MetricMemory {
		return "memory"
	}
	return "cpu"
}

// MetricSample is a single timestamped value in a series.
type MetricSample struct {
	T     time.Time
	Value float64
}

// Usage is the current usage of a metric for a subject, its request/limit, and
// a recent time series (last 1h). Available is false when no metrics source is
// reachable — the UI then shows "unavailable" rather than a fabricated value
// (FR-021, constitution data-integrity).
type Usage struct {
	Kind      MetricKind
	Current   float64        // CPU in cores, memory in bytes
	Request   float64        // 0 if unset
	Limit     float64        // 0 if unset
	Series    []MetricSample // rolling last 1h
	Available bool
}

// TopConsumer is one row of the top-consumers view.
type TopConsumer struct {
	Namespace string
	Name      string
	Kind      MetricKind
	Value     float64
}

// Event is a cluster event for the timeline (US5).
type Event struct {
	Time      time.Time
	Type      string // Normal | Warning
	Reason    string
	Message   string
	ObjKind   string
	ObjName   string
	Namespace string
	Count     int
}

// Warning reports whether the event is a Warning (or worse).
func (e Event) Warning() bool { return e.Type == "Warning" }

// TopologyPod is a pod placed on a node (US4), with the CPU (cores) and memory
// (bytes) it reserves via requests — "how much room it takes".
type TopologyPod struct {
	Namespace string
	Name      string
	Status    HealthLevel
	CPUReq    float64
	MemReq    float64
}

// TopologyNode is a node and the pods scheduled on it (US4). AllocCPU/AllocMem
// are the node's allocatable capacity; ReqCPU/ReqMem are the summed requests of
// its pods. Remaining room = alloc - req. A synthetic node "(unscheduled)"
// carries pods with no assigned node.
type TopologyNode struct {
	Name     string
	Status   HealthLevel // Ready + pressure derived
	Reason   string      // e.g. NotReady, MemoryPressure
	AllocCPU float64
	AllocMem float64
	ReqCPU   float64
	ReqMem   float64
	Pods     []TopologyPod
}

// HelmRelease is a deployed Helm release (US12, read-only).
type HelmRelease struct {
	Name         string
	Namespace    string
	Chart        string
	ChartVersion string
	AppVersion   string
	Revision     int
	Status       string // deployed | failed | pending-* | superseded | …
	Updated      time.Time
}

// Health maps a Helm release status to a display level.
func (r HelmRelease) Health() HealthLevel {
	switch {
	case r.Status == "deployed":
		return HealthOk
	case r.Status == "failed":
		return HealthError
	case strings.HasPrefix(r.Status, "pending"):
		return HealthWarning
	default:
		return HealthUnknown
	}
}

// HelmResource is one Kubernetes object deployed by a release, parsed from
// its rendered manifest (US12).
type HelmResource struct {
	APIVersion string
	Kind       string
	Namespace  string // empty → the release namespace
	Name       string
}

// HelmRevision is one entry of a release's history (US12).
type HelmRevision struct {
	Revision    int
	Status      string
	Updated     time.Time
	Description string
}

// Diagnostic is one workload-failure finding (US10): a crashlooping/OOMKilled/
// evicted/restarting container or pod, derived read-only from pod status.
type Diagnostic struct {
	Namespace string
	Pod       string
	Container string
	Restarts  int
	Reason    string // e.g. OOMKilled, CrashLoopBackOff, Evicted, Error (exit 1)
	Level     HealthLevel
}
