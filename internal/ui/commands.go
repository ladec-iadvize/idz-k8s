package ui

// tea.Cmd factories: the async, read-only fetch/load commands that
// feed the messages in messages.go (Init itself stays in app.go).

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
)

// podResourceType is the built-in v1 Pods type, used whenever a flow needs
// pods regardless of the discovered type list.
var podResourceType = model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}

// exactNamespace returns ns unless it is a namespace pattern — Prometheus
// scope queries take an exact namespace, or "" for the whole cluster.
func exactNamespace(ns string) string {
	if kube.IsNamespacePattern(ns) {
		return ""
	}
	return ns
}

// topConsumersToMap indexes TopN results by "namespace/name".
func topConsumersToMap(rows []model.TopConsumer) map[string]float64 {
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		out[r.Namespace+"/"+r.Name] = r.Value
	}
	return out
}

func (m Model) loadTypes() tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.ResourceTypes()
		return typesMsg{types: ts, err: err}
	}
}

// waitForChange blocks on the client's coalesced watch signal and turns it
// into a message (same pattern as the log stream). One waiter at a time.
func (m Model) waitForChange() tea.Cmd {
	if m.client == nil {
		return nil
	}
	ch := m.client.Changes()
	return func() tea.Msg {
		_, ok := <-ch
		return changeMsg{ok: ok}
	}
}

func (m Model) listObjects() tea.Cmd {
	c, t, ns, sel, node := m.client, m.curType, m.client.Namespace, m.drillSelector, m.drillNode
	if sel != "" && m.drillNamespace != "" {
		// Drilling: query in the workload's namespace so its selector cannot
		// match same-labelled pods elsewhere. The user's ns filter is untouched.
		ns = m.drillNamespace
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if node != "" {
			// Node drill: every pod scheduled on the node, all namespaces.
			objs, err := c.PodsOnNode(ctx, node)
			return objectsMsg{objects: objs, err: err}
		}
		objs, err := c.ListSelected(ctx, t, ns, sel)
		stale := c.CacheStale()
		// Services get a real status from their backends (one extra LIST).
		if err == nil && t.Group == "" && t.Resource == "services" {
			if eps, eerr := c.EndpointsByService(ctx, ns); eerr == nil {
				for i := range objs {
					objs[i].Status = kube.ServiceStatus(objs[i].Raw, eps, objs[i].Namespace, objs[i].Name)
				}
			}
		}
		var nodePods map[string][2]int
		// Nodes: count ready/total pods per node (one extra LIST) for the
		// PODS READY column.
		if err == nil && t.Group == "" && t.Resource == "nodes" {
			if pods, perr := c.List(ctx, podResourceType, ""); perr == nil {
				nodePods = map[string][2]int{}
				for _, p := range pods {
					node := kube.PodNode(p.Raw)
					if node == "" {
						continue
					}
					cnt := nodePods[node]
					cnt[1]++
					if r, d, ok := kube.ReadyCount("Pod", p.Raw); ok && d > 0 && r == d {
						cnt[0]++
					}
					nodePods[node] = cnt
				}
			}
		}
		return objectsMsg{objects: objs, nodePods: nodePods, err: err, stale: stale}
	}
}

func (m Model) tick() tea.Cmd {
	d := time.Duration(m.cfg.RefreshIntervalSeconds) * time.Second
	if d < time.Second {
		d = 5 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

// discoverMetrics finds the current cluster's Prometheus and builds a client
// that reaches it through the API server proxy (autonomous, per-context).
func (m Model) discoverMetrics() tea.Cmd {
	cl := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ref, ok, err := cl.DiscoverPrometheus(ctx)
		if err != nil || !ok {
			return metricsMsg{client: &metrics.Client{}, note: "no in-cluster Prometheus found (press 'u' shows unavailable)"}
		}
		mc, err := metrics.NewViaProxy(cl.RESTConfig(), ref.Namespace, ref.Name, ref.Port)
		if err != nil {
			return metricsMsg{client: &metrics.Client{}, note: "prometheus link failed: " + err.Error()}
		}
		return metricsMsg{client: mc, note: fmt.Sprintf("metrics via %s/%s:%d", ref.Namespace, ref.Name, ref.Port)}
	}
}

func (m Model) fetchDiag() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	marked := make([]model.ResourceObject, 0, len(m.marked))
	for _, o := range m.marked {
		marked = append(marked, o)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := cl.Diagnostics(ctx, ns)
		if err == nil && len(marked) > 0 {
			// Scope findings to the marked resources: marked pods directly,
			// marked workloads through the pods their selector owns.
			allowed, aerr := kube.ResolveMarkedPods(ctx, cl, marked)
			if aerr == nil {
				kept := rows[:0]
				for _, d := range rows {
					if allowed[d.Namespace+"/"+d.Pod] {
						kept = append(kept, d)
					}
				}
				rows = kept
			}
		}
		return diagMsg{rows: rows, err: err}
	}
}

func (m Model) fetchTopology() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		nodes, err := cl.Topology(ctx, ns)
		return topologyMsg{nodes: nodes, err: err}
	}
}

func (m Model) fetchEvents() tea.Cmd {
	cl, ns := m.client, m.client.Namespace
	if m.drillSelector != "" && m.drillNamespace != "" {
		ns = m.drillNamespace // drilled: fetch only the workload's namespace
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := cl.Events(ctx, ns)
		return eventsMsg{rows: rows, err: err}
	}
}

func (m Model) fetchPodUsage(ns, name string, raw map[string]interface{}) tea.Cmd {
	mc := m.metrics
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cpuReq, cpuLim, memReq, memLim := kube.PodResources(raw)

		cpuVal, cpuOK := mc.InstantScalar(ctx, metrics.PodUsage(ns, name, model.MetricCPU))
		cpuSeries := mc.Range(ctx, metrics.PodUsageRange(ns, name, model.MetricCPU), metrics.TrendWindow, time.Minute)
		memVal, memOK := mc.InstantScalar(ctx, metrics.PodUsage(ns, name, model.MetricMemory))
		memSeries := mc.Range(ctx, metrics.PodUsageRange(ns, name, model.MetricMemory), metrics.TrendWindow, time.Minute)

		return usageMsg{
			ns:   ns,
			name: name,
			cpu:  model.Usage{Kind: model.MetricCPU, Current: cpuVal, Request: cpuReq, Limit: cpuLim, Series: cpuSeries, Available: cpuOK},
			mem:  model.Usage{Kind: model.MetricMemory, Current: memVal, Request: memReq, Limit: memLim, Series: memSeries, Available: memOK},
		}
	}
}

// fetchListUsage feeds the pods list's CPU/MEM columns (two instant batch
// queries for the whole scope, at tick cadence only).
func (m Model) fetchListUsage() tea.Cmd {
	mc, ns := m.metrics, m.client.Namespace
	exactNS := exactNamespace(ns)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return podUsageMsg{
			cpu: topConsumersToMap(mc.TopN(ctx, metrics.ScopeNowByPod(exactNS, model.MetricCPU), model.MetricCPU)),
			mem: topConsumersToMap(mc.TopN(ctx, metrics.ScopeNowByPod(exactNS, model.MetricMemory), model.MetricMemory)),
		}
	}
}

// fetchUsage builds the usage rows: two instant per-pod queries feed both
// the pods view and the per-workload aggregation (cost independent of the
// row count).
func (m Model) fetchUsage(workloads []model.ResourceObject, markedKeys map[string]bool) tea.Cmd {
	cl, mc, ns := m.client, m.metrics, m.client.Namespace
	isAgg := len(workloads) > 0
	exactNS := exactNamespace(ns)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cpu := topConsumersToMap(mc.TopN(ctx, metrics.ScopeNowByPod(exactNS, model.MetricCPU), model.MetricCPU))
		mem := topConsumersToMap(mc.TopN(ctx, metrics.ScopeNowByPod(exactNS, model.MetricMemory), model.MetricMemory))
		pods, err := cl.List(ctx, podResourceType, ns)
		if err != nil {
			return usageTableMsg{err: err}
		}
		var rows []model.UsageRow
		if !isAgg {
			rows = make([]model.UsageRow, 0, len(pods))
			for _, p := range pods {
				key := p.Namespace + "/" + p.Name
				if len(markedKeys) > 0 && !markedKeys[key] {
					continue // Space-marked pods scope the view
				}
				c, hc := cpu[key]
				mv, hm := mem[key]
				rows = append(rows, model.UsageRow{
					Namespace: p.Namespace, Name: p.Name, Pods: 1,
					CPU: c, Mem: mv, HasCPU: hc, HasMem: hm,
				})
			}
		} else {
			rows = make([]model.UsageRow, 0, len(workloads))
			for _, wl := range workloads {
				sel, ok := kube.PodSelectorLabels(wl.Raw)
				if !ok {
					continue
				}
				row := model.UsageRow{Namespace: wl.Namespace, Name: wl.Name}
				for _, p := range pods {
					if p.Namespace != wl.Namespace || !kube.LabelsMatch(p.Raw, sel) {
						continue
					}
					row.Pods++
					key := p.Namespace + "/" + p.Name
					if v, found := cpu[key]; found {
						row.CPU += v
						row.HasCPU = true
					}
					if v, found := mem[key]; found {
						row.Mem += v
						row.HasMem = true
					}
				}
				rows = append(rows, row)
			}
		}
		return usageTableMsg{rows: rows, isAgg: isAgg}
	}
}
