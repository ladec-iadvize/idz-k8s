package ui

// Bubble Tea messages: every async result delivered to Update
// (list/usage/sizing/diag/events/helm payloads and the change-watch ticks).

import (
	"time"

	"github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
)

type typesMsg struct {
	types []model.ResourceType
	err   error
}

// nsOptionsMsg carries the cluster's namespace list for the namespace picker,
// fetched asynchronously so a slow apiserver never freezes the Update loop.
type nsOptionsMsg struct {
	opts []string
	err  error
}

type objectsMsg struct {
	objects  []model.ResourceObject
	nodePods map[string][2]int // only set when listing nodes
	err      error
	stale    error // set when data comes from a cache whose watch is failing
}

type logLineMsg kube.LogLine

type tickMsg struct{}

// changeMsg: the watch reported a change (ok=false → the client was
// replaced and the loop must re-arm on the new one).
type changeMsg struct{ ok bool }

// changeFlushMsg fires after the throttle window to apply coalesced changes.
type changeFlushMsg struct{}

// changeFlushDelay throttles watch-driven re-renders (a rolling update
// emits dozens of events per second; the eye needs ~4 fps, not 50).
const changeFlushDelay = 250 * time.Millisecond

type errMsg struct{ err error }

type podUsageMsg struct {
	cpu, mem map[string]float64
}

type usageTableMsg struct {
	rows  []model.UsageRow
	isAgg bool
	err   error
}

type usageMsg struct {
	ns, name string
	cpu, mem model.Usage
}

type metricsMsg struct {
	client *metrics.Client
	note   string
}

type sizingMsg struct {
	advice model.SizingAdvice
	err    error
}

type accessMsg struct {
	report model.AccessReport
	err    error
}

type connectivityMsg struct {
	report model.ConnectivityReport
	err    error
}

type postureMsg struct {
	rows []model.PostureFinding
	err  error
}

type sizingListMsg struct {
	rows []model.SizingAdvice
	objs []model.ResourceObject
	err  error
}

type diagMsg struct {
	rows []model.Diagnostic
	err  error
}

type topologyMsg struct {
	nodes []model.TopologyNode
	err   error
}

type eventsMsg struct {
	rows []model.Event
	err  error
}

type describeMsg struct {
	ns, name string
	events   []model.Event
	extra    string // e.g. Service endpoints summary
}

type helmMsg struct {
	rows []model.HelmRelease
	err  error
}

type helmDetailMsg struct {
	ns, name string
	detail   helm.ReleaseDetail
	live     []helmResLive // mirrors detail.Resources: live state of each object
	err      error
}

type helmResLive struct {
	status model.StatusSummary
	found  bool
	known  bool // type resolvable via discovery
}
