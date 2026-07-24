# Feature Specification: Kubernetes TUI Overview & Admin Client

**Feature Branch**: `001-k8s-tui-client`

**Created**: 2026-07-02

**Status**: Draft

**Input**: User description: "L'objectif du projet est d'avoir une TUI en tant que client Kubernetes. Il va servir à administrer le cluster Kubernetes au quotidien. Il faut que la TUI puisse être cliquable. Il faut aussi des raccourcis maniables non exotiques" (amended: visual elements / charts; amended: customizable views; **amended (pivot): the tool is READ-ONLY — an overview & debugging tool, no mutating administrative actions. Actions are performed elsewhere (e.g. k9s). New overview capabilities: a topology view (which pods run on which nodes), app sizing recommendations, and an events timeline.**)

## Clarifications

### Session 2026-07-02

- Q: Data freshness / update mechanism for "near real time" (FR-006)? → A: Periodic refresh, configurable by the operator, default ~5 s.
- Q: Should the client support Custom Resources (CRDs), not just built-in types (FR-002)? → A: Yes — dynamic discovery of all API resource types, including CRDs.
- Q: Policy for revealing secret values in plaintext (FR-015)? → A: Masked by default; revealed freely on explicit request, with no gating or audit beyond the operator's existing cluster access.
- Q: Should the client keep a local audit/history of mutating actions? → A: No — and superseded by the read-only pivot below: the tool performs no mutating actions at all.
- Q: Multi-cluster — simultaneous view or single active context (FR-003)? → A: Single active context at a time, with fast switching between contexts.

### Session 2026-07-02 (read-only pivot)

- Q: Should the tool perform administrative/mutating actions? → A: No. The tool is strictly read-only — an overview and debugging client. Mutating administration is out of scope and is done in a separate tool (e.g. k9s).

### Session 2026-07-24 (v3 pivot: administration)

- Q: Should the read-only posture be kept? → A: No — owner decision 2026-07-24: the read-only pivot is REVERSED. The tool administers the cluster directly (edit YAML, scale, rolling restart, delete, cordon/uncordon, suspend/resume, port-forward, Helm rollback/uninstall), with no separate opt-in flag. The safety contract replacing "read-only" is: EVERY mutating action requires an explicit confirmation step (confirmation modal or value prompt) before any API call, runs under the operator's own RBAC, and reports its outcome explicitly. This supersedes the 2026-07-05 "opt-in --admin flag" intent and the 2026-07-02 read-only pivot (kept below as history).

### Session 2026-07-06 (v2 kickoff)

- Q: v2 scope? → A: All six deferred P3 stories — US8 customizable views (first), US6 sizing recommendations, US13 posture, US14 connectivity, US15 access view, US16 read-only diff — plus the shared-informers optimization (ex-T010). Read-only invariant unchanged.

### Session 2026-07-03

- Q: v1 scope across the 16 user stories? → A: v1 = P1 + P2 (US1–US5, US7, US9–US12); P3 stories (US6 sizing, US8 customizable views, US13 posture, US14 connectivity, US15 access, US16 diff) are deferred to backlog.
- Q: Metrics source for usage gauges and time-series trend charts? → A: Requires Prometheus (or equivalent) for history; trend window is the last 1 hour. Without such a source, trend charts show "unavailable".
- Q: One metrics backend or two (instantaneous vs history)? → A: Prometheus is the single metrics source for both instantaneous usage and history; metrics-server is not required.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Cluster overview & inspection (Priority: P1)

An operator opens the client to understand the current state of the cluster.
They browse resource types (pods, deployments, services, nodes, and any CRD),
drill into a resource to read its details and events, follow a pod's logs, and
switch the active namespace or cluster context. The client never changes cluster
state — it only reads.

**Why this priority**: Inspection is the foundation of the overview
tool and the minimum viable product on its own.

**Independent Test**: Point the client at a cluster with existing workloads,
browse a namespace, open a pod, read its details and logs — and confirm that no
action anywhere in the interface can modify the cluster.

**Acceptance Scenarios**:

1. **Given** a reachable cluster, **When** the operator launches the client, **Then** the current context, active namespace, and a resource list are displayed.
2. **Given** a resource list, **When** the operator selects a resource, **Then** its details (status, metadata, events) are shown.
3. **Given** a running pod, **When** the operator opens its logs, **Then** logs stream live and can be paused, scrolled, and filtered.
4. **Given** any view, **When** the operator triggers an admin action (v3), **Then** it is reachable through the actions palette or its dedicated key and ALWAYS asks for confirmation before touching the cluster.
5. **Given** several namespaces or contexts, **When** the operator switches them, **Then** the views update to the selected scope.

---

### User Story 2 - Graphical resource views for debugging (Priority: P1)

An operator diagnoses problems visually rather than by reading raw numbers:
color-coded health, usage gauges (CPU/memory vs requests/limits), and time-series
charts of how a workload's or node's resource usage evolves. These visuals are
the core value of the tool for spotting saturation, throttling, or imbalance.

**Why this priority**: The explicit purpose of the pivot is a graphical overview
to debug and understand the cluster; the visuals are central, not decorative.

**Independent Test**: Open a node or workload and confirm CPU/memory usage is
shown as a gauge plus a trend chart, with health conveyed by color, matching the
underlying numeric values.

**Acceptance Scenarios**:

1. **Given** a selected node or workload, **When** the operator views its usage, **Then** usage is shown graphically (gauge/bar) relative to request/limit, alongside the numeric value.
2. **Given** a resource with a health/status, **When** it is listed or opened, **Then** status is color-coded with a non-color fallback (symbol/label).
3. **Given** a metric that changes over time, **When** the operator opens its trend view, **Then** a time-series chart of recent values is displayed.
4. **Given** a terminal without color/rich rendering, **When** the operator uses the client, **Then** the same information is available in readable text.
5. **Given** any visual, **When** it renders, **Then** it accurately reflects the data and shows an explicit "unavailable/stale" state rather than a misleading one.

---

### User Story 3 - Ergonomic keyboard-driven control (Priority: P1)

An operator drives the entire interface from the keyboard using familiar,
predictable shortcuts (navigation, search/filter, back, quit, help), with a
discoverable help overlay listing every active shortcut for the current view.

**Why this priority**: A terminal overview tool is only usable at daily-driver
speed if navigation is fast and shortcuts match what operators already expect.

**Independent Test**: Using only the keyboard, navigate list → detail → back,
filter a list, open help, and quit — confirming every shortcut is discoverable
and conventional.

**Acceptance Scenarios**:

1. **Given** any view, **When** the operator requests help, **Then** an overlay lists all shortcuts active in that view.
2. **Given** a resource list, **When** the operator uses navigation keys, **Then** the selection moves and the list scrolls to keep it visible.
3. **Given** a resource list, **When** the operator filters and types, **Then** the list narrows in real time.
4. **Given** a nested view, **When** the operator presses "back", **Then** the previous view is restored.
5. **Given** the shortcut set, **When** reviewed, **Then** it uses common conventions, not exotic combinations.

---

### User Story 4 - Topology view (pods ↔ nodes) (Priority: P2)

An operator opens a topology view to see how workloads are placed across the
cluster: which pods run on which nodes, how pods distribute across nodes, and
where a node is hot or unbalanced. Selecting a node shows its pods; selecting a
pod highlights its node.

**Why this priority**: Placement is a frequent debugging question ("why is this
node saturated / where does this pod run"). It is a headline overview capability
but builds on the inspect foundation.

**Independent Test**: Open the topology view on a multi-node cluster and confirm
each node lists the pods scheduled on it, and that selecting a pod reveals its
host node.

**Acceptance Scenarios**:

1. **Given** a multi-node cluster, **When** the operator opens the topology view, **Then** nodes are shown with the pods scheduled on each.
2. **Given** the topology view, **When** the operator selects a node, **Then** its pods (and its usage) are shown.
3. **Given** the topology view, **When** the operator selects a pod, **Then** its host node is highlighted and its details are reachable.
4. **Given** a node under resource pressure, **When** shown in topology, **Then** its state is visually distinguishable (color/indicator).
5. **Given** a large cluster, **When** the topology view opens, **Then** it stays readable and navigable (grouping/scrolling), not an unreadable wall.

---

### User Story 5 - Events timeline (Priority: P2)

An operator reviews cluster events on a timeline to debug what happened and
when: recent events ordered in time, filterable by namespace/resource/severity,
and correlatable with a selected resource.

**Why this priority**: Events are the primary breadcrumb trail for debugging;
a time-ordered, filterable view speeds root-cause analysis.

**Independent Test**: Trigger some cluster activity, open the events timeline,
and confirm events appear in time order and can be filtered to a single resource.

**Acceptance Scenarios**:

1. **Given** recent cluster activity, **When** the operator opens the events timeline, **Then** events are shown ordered by time (most recent discoverable).
2. **Given** the timeline, **When** the operator filters by namespace/resource/severity, **Then** only matching events remain.
3. **Given** a selected resource, **When** the operator views its events, **Then** the timeline is scoped to that resource.
4. **Given** warning/error events, **When** displayed, **Then** they are visually distinguishable from normal ones.
5. **Given** the known retention limit of cluster events, **When** older events have expired, **Then** the timeline indicates the visible window rather than implying completeness.

---

### User Story 6 - App sizing recommendations (Priority: P3)

An operator sees advisory right-sizing recommendations for workloads based on
observed usage versus configured requests/limits: flags for over-provisioned
(usage far below requests) and under-provisioned / at-risk (usage near or above
limits, throttling). Recommendations are advisory only and always show the
observed data they are based on.

**Why this priority**: High-value for cost and reliability, but depends on
metrics and observation history, so it comes after the core overview.

**Independent Test**: On a workload with a metrics history, confirm a
recommendation appears (e.g. "requests appear oversized") together with the
observed usage vs request/limit it is based on; on a workload without enough
data, confirm no recommendation is fabricated.

**Acceptance Scenarios**:

1. **Given** a workload with sufficient observed usage history, **When** the operator opens sizing, **Then** an advisory recommendation is shown with the observed usage vs requests/limits behind it.
2. **Given** usage consistently far below requests, **When** evaluated, **Then** it is flagged as over-provisioned.
3. **Given** usage near/above limits or evidence of throttling, **When** evaluated, **Then** it is flagged as under-provisioned / at-risk.
4. **Given** insufficient data or no metrics source, **When** the operator opens sizing, **Then** the tool states that no recommendation can be made — it never invents figures.
5. **Given** any recommendation, **When** shown, **Then** it is clearly labeled advisory and never applied automatically by the tool.

---

### User Story 7 - Clickable, mouse-driven navigation (Priority: P2)

An operator uses the mouse: clicking a resource selects it, clicking a
tab/breadcrumb switches views, clicking an on-screen element activates it, and
the wheel scrolls long lists, logs, and the timeline.

**Why this priority**: Explicit requirement; lowers the barrier for operators who
have not memorized every shortcut. Builds on the navigable interface.

**Independent Test**: With the mouse only, click into a namespace, open a pod,
open the topology and timeline, and scroll — confirming the same destinations
reachable by keyboard are reachable by clicking.

**Acceptance Scenarios**:

1. **Given** a resource list, **When** the operator clicks a row, **Then** it becomes the selection.
2. **Given** a list, **When** the operator double-clicks a row (or clicks an "open" affordance), **Then** the detail view opens.
3. **Given** on-screen tabs/menus/labels, **When** clicked, **Then** the corresponding view is activated.
4. **Given** a long list/log/timeline, **When** the operator wheel-scrolls, **Then** the content scrolls.
5. **Given** a terminal not forwarding mouse events, **When** the operator uses the client, **Then** all functionality remains reachable by keyboard.

---

### User Story 8 - Customizable views (Priority: P3)

An operator tailors resource lists to how they work: choosing which columns
appear and in what order, the default sort and filters, saving named views for
recurring lookups, and resetting to defaults. Customizations persist across
sessions.

**Why this priority**: Removes daily friction, but not required for a first
usable overview.

**Independent Test**: Hide/add/reorder columns, change sort, save, relaunch and
confirm restore; then reset and confirm defaults return.

**Acceptance Scenarios**:

1. **Given** a resource list, **When** the operator chooses/hides/reorders columns, **Then** the list reflects the choice immediately.
2. **Given** a customized view, **When** saved and relaunched, **Then** the customization is restored.
3. **Given** an arrangement, **When** saved as a named view, **Then** the operator can switch to it later.
4. **Given** a customized view, **When** reset, **Then** defaults return.
5. **Given** stored customizations, **When** they reference something no longer available, **Then** the client falls back to defaults without failing to start.

---

### User Story 9 - Ownership & dependency graph (Priority: P2)

An operator navigates the relationships between objects to debug routing and
ownership: `Deployment → ReplicaSet → Pods`, `Service → Endpoints → Pods`,
`Ingress → Service`, and up/down an object's owner chain.

**Why this priority**: Relationship navigation resolves frequent "why is my
service not routing / what owns this pod" questions; high value.

**Independent Test**: Open a Service with no ready backends and confirm the graph
shows it has zero endpoints and no backing pods.

**Acceptance Scenarios**:

1. **Given** a Deployment, **When** the operator opens its dependency graph, **Then** its ReplicaSets and Pods are shown linked.
2. **Given** a Service, **When** viewing its graph, **Then** its Endpoints and backing pods are shown, including when there are zero endpoints.
3. **Given** a Pod, **When** navigating up, **Then** its owning controller chain is shown.

---

### User Story 10 - Workload failure diagnostics (Priority: P2)

An operator quickly sees why workloads are failing: restart counts, last
container termination reason (e.g. **OOMKilled**, non-zero exit code), and
evicted pods with their reason.

**Why this priority**: Crash/OOM/eviction triage is the most common daily
debugging task; surfacing termination reasons saves digging through describe.

**Independent Test**: On a crashlooping pod, confirm its restart count and last
termination reason (e.g. OOMKilled) are shown without leaving the view.

**Acceptance Scenarios**:

1. **Given** pods restarting, **When** opening diagnostics, **Then** per-container restart counts and last termination reason (e.g. OOMKilled, exit code) are shown.
2. **Given** an OOMKilled container, **When** displayed, **Then** it is flagged distinctly.
3. **Given** evicted pods, **When** listed, **Then** each shows its eviction reason.

---

### User Story 11 - Scheduling & capacity (Priority: P2)

An operator understands scheduling pressure: why pods are Pending/unschedulable,
and per-node bin-packing (allocatable vs requested vs used) with remaining
headroom.

**Why this priority**: Complements topology; explains node saturation and why
new pods will not schedule.

**Independent Test**: With a Pending pod present, confirm the scheduling view
shows its unschedulable reason, and a node's allocatable vs requested capacity.

**Acceptance Scenarios**:

1. **Given** pending pods, **When** opening the scheduling view, **Then** each shows its scheduling reason (e.g. insufficient cpu/memory).
2. **Given** a node, **When** viewing capacity, **Then** allocatable vs requested (and used, when metrics available) are shown with headroom.
3. **Given** no metrics source, **When** "used" is unavailable, **Then** allocatable/requested still show and used shows "unavailable".

---

### User Story 12 - Helm release overview (Priority: P2)

Workloads are deployed via Helm charts. An operator reviews Helm releases: name,
namespace, chart and version, current revision, status (deployed / failed /
pending / superseded), and revision history — to debug bad or stuck deploys. The
tool can also roll back or uninstall a release behind an explicit confirmation (v3); install/upgrade stay out of scope.

**Why this priority**: Since deployment is Helm-based, release state is the
natural unit for debugging "what changed / why did this deploy fail". Reads come
inspection complements doing the actual rollback elsewhere.

**Independent Test**: On a Helm-managed workload, confirm its release, current
revision, and history are shown, and that no upgrade/rollback/uninstall action
exists anywhere.

**Acceptance Scenarios**:

1. **Given** Helm-managed workloads, **When** opening releases, **Then** installed releases are listed with name, namespace, chart, version, revision, and status.
2. **Given** a release, **When** viewing its history, **Then** its revisions and per-revision status are shown.
3. **Given** a failed or pending release, **When** displayed, **Then** it is flagged; rollback/uninstall (v3) go through the actions palette and its confirmation step.

---

### User Story 13 - Compliance & posture overview (Priority: P3)

An operator sees an advisory posture report across workloads, flagging config
that violates common security/reliability best practices: missing
requests/limits, privileged containers, running as root, missing liveness/
readiness probes, images pinned to `latest`, namespaces without a NetworkPolicy,
and TLS secrets near or past expiry. Findings are advisory.

**Why this priority**: Turns the overview into a posture lens aligned
with the project constitution (performance, security). Valuable but not core to
day-one debugging.

**Independent Test**: On a workload with no resource limits and a `latest` image,
confirm both are reported as posture findings referencing the concrete object.

**Acceptance Scenarios**:

1. **Given** workloads, **When** opening posture, **Then** findings are listed by rule (no requests/limits, privileged, runs as root, no probes, `latest` image, no NetworkPolicy).
2. **Given** TLS secrets, **When** checked, **Then** those near or past expiry are flagged with their expiry date.
3. **Given** any finding, **When** shown, **Then** it references the concrete object/field, is clearly advisory, and no figure is fabricated.

---

### User Story 14 - Connectivity / NetworkPolicy view (Priority: P3)

An operator debugs connectivity for a pod: which NetworkPolicies select it and
what ingress/egress they allow, or whether it is unrestricted.

**Why this priority**: NetworkPolicy effects are hard to reason about; a per-pod
summary speeds connectivity debugging.

**Independent Test**: On a pod selected by a policy, confirm the policy and its
allowed peers are listed; on a pod with no policy, confirm it is shown as
unrestricted.

**Acceptance Scenarios**:

1. **Given** a pod, **When** opening its connectivity view, **Then** the NetworkPolicies selecting it are listed.
2. **Given** those policies, **When** displayed, **Then** allowed ingress/egress peers are summarized.
3. **Given** a pod matched by no policy, **When** displayed, **Then** it is shown as unrestricted.

---

### User Story 15 - Access (RBAC) view (Priority: P3)

An operator sees what their credentials can read and why some resource types are
inaccessible, so the overview's blind spots are explicit.

**Why this priority**: Explains "why don't I see X" and sets expectations;
low effort, complements least-privilege operation.

**Independent Test**: With a restricted kubeconfig, confirm the access view
summarizes allowed reads and marks a forbidden type as inaccessible with a reason.

**Acceptance Scenarios**:

1. **Given** the operator's credentials, **When** opening the access view, **Then** their read permissions are summarized.
2. **Given** an inaccessible resource type, **When** browsing, **Then** it is marked inaccessible with the reason, without erroring the app.

---

### User Story 16 - Manifest drift diff (Priority: P3)

An operator compares an object's live state against its last-applied
configuration to see drift, without any ability to apply changes.

**Why this priority**: Drift is a common debugging clue; the diff exposes
it safely. Niche, hence P3.

**Independent Test**: On an object with a last-applied annotation, confirm the
diff between live and last-applied is shown and no apply/edit affordance exists.

**Acceptance Scenarios**:

1. **Given** an object with a last-applied annotation, **When** opening diff, **Then** the differences between live and last-applied are shown.
2. **Given** no such annotation, **When** opening diff, **Then** the tool states no baseline is available.
3. **Given** the diff, **When** shown, **Then** the diff itself offers no apply affordance; fixing drift goes through the edit action and its confirmation (FR-012 v3).

---

### Edge Cases

- What happens when the cluster is unreachable or the connection drops mid-session? Clear connection status; recover/reconnect without crashing.
- How does the client handle a namespace or cluster with thousands of resources? Lists and topology stay responsive (virtualized/grouped), never freezing.
- What happens when the operator's credentials expire during a session? Surface an auth error; allow re-auth or context switch without losing the session.
- How does the client behave in a terminal without mouse events or a small window? Keyboard access stays complete; layout degrades gracefully.
- How do visuals behave without color/rich rendering, or when a metrics source is unavailable? Fall back to readable text; show an explicit "unavailable" state, never an empty/misleading chart.
- What happens when saved customizations are missing/corrupted/outdated? Start on defaults; never fail to launch.
- How is completeness of the events timeline handled given cluster event retention? Indicate the visible window; do not imply events beyond retention.
- What if the operator lacks read permission on a resource type? Show it as inaccessible with a clear message, without erroring the whole app.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The client MUST connect to a Kubernetes cluster using the operator's existing standard credentials/configuration, and MUST NOT require a separate credential store.
- **FR-002**: The client MUST dynamically discover all resource types exposed by the cluster's API — including Custom Resources (CRDs), not only built-in types — and MUST allow the operator to switch between any discovered type.
- **FR-003**: The client MUST allow the operator to change the active namespace and the active cluster context from within the interface. Exactly one cluster context is active at a time, with fast switching; it does not display multiple clusters simultaneously.
- **FR-004**: The client MUST display the detailed state of a selected resource, including status, metadata, and related events.
- **FR-005**: The client MUST stream a pod/container's logs live, with the ability to pause, scroll, and filter.
- **FR-006**: The client MUST refresh displayed data on a periodic interval without a manual full refresh; the interval MUST be configurable and default to approximately 5 seconds.
- **FR-007**: Users MUST be able to filter/search within any resource list and the events timeline by typing.
- **FR-008**: The client MUST be fully operable using only the keyboard, with no function reachable exclusively by mouse.
- **FR-009**: Keyboard shortcuts MUST follow common, widely recognized conventions and avoid exotic or hard-to-reach combinations.
- **FR-010**: The client MUST provide a discoverable, context-aware help overlay listing all shortcuts active in the current view.
- **FR-011**: The client MUST support mouse interaction: clicking to select, clicking to activate on-screen controls/navigation, and wheel scrolling.
- **FR-012** *(v3, 2026-07-24 — supersedes the read-only rule)*: The client provides administration actions (edit YAML, scale, rolling restart, delete, cordon/uncordon, suspend/resume CronJobs, port-forward, Helm rollback/uninstall). EVERY mutating action MUST be preceded by an explicit confirmation step — a confirmation modal or a value prompt — and MUST never run from a single keypress. Mutations run strictly under the operator's own RBAC and their outcome (success or error) MUST be reported explicitly.
- **FR-013**: The client MUST provide a topology view showing which pods are scheduled on which nodes, allowing selection from node→pods and pod→node, and visually distinguishing nodes under resource pressure.
- **FR-014**: The client MUST provide an events timeline: cluster events ordered in time, filterable by namespace/resource/severity, scopable to a selected resource, with warning/error events visually distinguished, and it MUST indicate the visible window rather than implying completeness beyond event retention.
- **FR-015**: The client MUST mask sensitive values (e.g. secret contents) by default and reveal them only on explicit operator request; revealing requires no authorization beyond the operator's existing cluster access, and the client MUST NOT gate or audit the reveal action.
- **FR-016**: The client MUST show the current connection status and handle unreachable clusters, dropped connections, and expired credentials without crashing.
- **FR-017**: The client MUST remain responsive when browsing namespaces, resource types, topology, or timelines containing large numbers of items.
- **FR-018**: The client MUST operate strictly within the permissions granted to the operator's credentials and MUST NOT attempt to elevate privileges. RBAC denials on admin actions are surfaced explicitly (the access view shows the granted verbs).
- **FR-019**: The client MUST present visual representations of quantitative cluster data, including resource-usage gauges/bars (CPU/memory vs requests/limits) and time-series trend charts for workloads and nodes, to support debugging. Trend charts source their history from Prometheus (or an equivalent metrics backend) and display a rolling **last-1-hour** window; when no such source is available, trend charts show an explicit "unavailable" state (per FR-021).
- **FR-020**: The client MUST convey resource health/status with color coding AND a non-color fallback (symbol or label).
- **FR-021**: Visual elements MUST accurately reflect underlying data and MUST clearly indicate when the data source is unavailable or stale, rather than rendering an empty or misleading visual.
- **FR-022**: When the terminal lacks color or rich-rendering capability, the client MUST degrade gracefully and keep the same information available in readable text.
- **FR-023**: The client MUST provide advisory app sizing recommendations derived from observed usage versus configured requests/limits (flagging over-provisioned and under-provisioned / at-risk workloads). Recommendations MUST be based only on real observed data, MUST display the data behind them, MUST NOT be shown when data is insufficient (no fabricated figures), and are advisory only (never applied automatically).
- **FR-024**: Users MUST be able to customize resource views (which columns/fields are shown, their order, default sort and filter).
- **FR-025**: The client MUST persist view customizations across sessions and restore them on the next launch, MUST allow saving/switching named views and resetting to defaults, and MUST tolerate missing/invalid/outdated customizations by falling back to defaults without failing to start.
- **FR-026**: The client MUST let the operator navigate object relationships (ownership and routing) — e.g. Deployment→ReplicaSet→Pods, Service→Endpoints→Pods, Ingress→Service — up and down the graph, and MUST make a broken link (e.g. a Service with zero endpoints) visible.
- **FR-027**: The client MUST surface workload failure diagnostics: per-container restart counts, last termination reason (including OOMKilled and exit codes), and evicted pods with their eviction reason, with failure states visually distinguished.
- **FR-028**: The client MUST provide a scheduling & capacity view showing the reason each Pending/unschedulable pod cannot be scheduled, and per-node bin-packing (allocatable vs requested vs used, with remaining headroom); "used" is sourced from Prometheus and degrades to "unavailable" when Prometheus is not reachable.
- **FR-029**: The client MUST provide a Helm release overview: installed releases (name, namespace, chart, version, current revision, status) and per-release revision history, flagging failed/pending/stuck releases. *(v3)* Rollback and uninstall are available under the FR-012 confirmation contract; install and upgrade remain out of scope.
- **FR-030**: The client MUST provide an advisory compliance/posture overview flagging common issues (missing requests/limits, privileged containers, running as root, missing liveness/readiness probes, images pinned to `latest`, namespaces without a NetworkPolicy, TLS secrets near/after expiry). Findings MUST be derived only from observed configuration, MUST reference the concrete object/field, MUST NOT fabricate data, and are advisory only (never enforced automatically).
- **FR-031**: The client MUST provide a per-pod connectivity view listing the NetworkPolicies that select it and the ingress/egress they allow, and MUST indicate when a pod is unrestricted by any policy.
- **FR-032**: The client MUST provide an access (RBAC) view summarizing what the operator's credentials can do (read AND write verbs, read verbs listed first), and MUST mark inaccessible resource types with the reason rather than erroring the application.
- **FR-033**: The client MUST provide a diff between an object's live state and its last-applied configuration (when available), stating when no baseline exists. The diff view itself offers no apply affordance; changes go through the edit action (FR-012 v3).
- **FR-034**: The client MUST be able to tail logs merged across all pods of a selected workload in chronological order (in addition to single-pod logs per FR-005).
- **FR-035**: The client MUST provide a "top consumers" view (top pods/nodes by CPU/memory), sourced from Prometheus; when Prometheus is not reachable, it shows an explicit "unavailable" state.
- **FR-036**: The interface MUST be approachable by a general technical audience, not only Kubernetes experts: prefer graphical representations (charts, gauges, timelines, color) over raw text wherever they aid comprehension, keep every capability discoverable from the interface itself (visible menus/selectors, contextual shortcut help), and avoid jargon-only output. Prior kubectl/k9s experience MUST NOT be required to perform the core overview tasks.

### Key Entities *(include if feature involves data)*

- **Cluster Context**: A named connection to a cluster (cluster + identity). One is active at a time; the operator can switch.
- **Namespace**: A logical partition scoping most resources; the operator selects an active one.
- **Resource**: Any Kubernetes object the operator inspects (built-in or CRD), with type, name, status, metadata, and events.
- **Node**: A cluster machine hosting pods; carries usage/pressure state and a set of scheduled pods (topology).
- **Event**: A time-stamped cluster event with type/severity, involved object, reason, and message (timeline).
- **Metric Series**: Time-ordered observed usage (CPU/memory) for a workload or node, used for charts and sizing.
- **Sizing Recommendation**: An advisory assessment (over/under-provisioned/ok) for a workload, derived from Metric Series vs requests/limits, with the supporting data and a confidence/insufficient-data state.
- **Shortcut**: A keyboard binding scoped to a view, listed in the help overlay.
- **Saved View**: A named, persisted list arrangement (columns/order/sort/filter) the operator can restore, switch to, or reset.
- **Helm Release**: A deployed Helm release with name, namespace, chart, chart/app version, current revision, status (deployed/failed/pending/superseded), and a revision history.
- **Dependency Edge**: A relationship between two objects — owns (owner→owned), routes-to (Service→Endpoints/Pods, Ingress→Service), or selects (policy→pod) — used to build the ownership/connectivity graphs.
- **Posture Finding**: An advisory assessment against a best-practice rule, with rule id, severity, a reference to the concrete object/field, and an advisory message. Never fabricated; never enforced automatically.
- **Scheduling Reason**: For a Pending/unschedulable pod, the reason it cannot be scheduled (e.g. insufficient cpu/memory), surfaced in the scheduling & capacity view.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can locate a specific pod in a known namespace and open its details in under 15 seconds from launch.
- **SC-002**: An operator can view a pod's live logs within 3 interactions of the main view.
- **SC-003**: Every destination reachable by keyboard is also reachable by mouse and vice versa (100% parity).
- **SC-004**: A new operator familiar with terminal tools can perform the core overview tasks (browse, inspect, view usage) relying only on in-app help — 90% task completion on first attempt in usability testing.
- **SC-005**: Resource lists and the topology view remain interactive (no perceptible freeze) with at least 5,000 pods across at least 100 nodes.
- **SC-006** *(v3)*: The client performs zero UNCONFIRMED mutating API operations: every mutation is preceded by an explicit confirmation or value prompt — verifiable by test harness (a mutation without its confirmation step is a defect).
- **SC-007**: The client recovers from a dropped cluster connection and resumes without a restart in at least 95% of transient-disconnection cases.
- **SC-008**: Sensitive values are masked by default in 100% of views that display them.
- **SC-009**: An operator can identify an over-utilized or unhealthy workload/node from its visual indicators without reading raw numbers — ≥90% correct identification in usability testing.
- **SC-010**: Every visual element has a readable textual equivalent, so 100% of visually conveyed information remains accessible in a no-color / no-rich-rendering terminal.
- **SC-011**: An operator can determine which node hosts a given pod (and the pods on a given node) via the topology view in under 10 seconds.
- **SC-012**: The events timeline presents events in correct time order and can be scoped to a single resource in ≤ 2 interactions.
- **SC-013**: Sizing recommendations are shown only when backed by observed data; in 100% of insufficient-data cases the tool states no recommendation rather than showing a figure.
- **SC-014**: An operator can customize a resource list and have it restored after relaunch in 100% of cases, and reset it to defaults in a single action; a corrupted customization never prevents startup.
- **SC-015**: From a Service with no ready backends, an operator can identify the missing endpoints/pods via the dependency graph in under 20 seconds.
- **SC-016**: For a crashlooping pod, the last container termination reason (e.g. OOMKilled) is visible within 2 interactions of selecting it.
- **SC-017**: For a Helm-managed workload, its release status and current revision are discoverable in ≤ 2 interactions, and Helm rollback/uninstall always go through the FR-012 confirmation step (install/upgrade are not offered).
- **SC-018**: 100% of posture findings reference a concrete object/field; none are fabricated, and none are shown when the underlying configuration cannot be observed.
- **SC-019**: For a Pending pod, its scheduling reason is visible in the scheduling view within 2 interactions.

## Assumptions

- The audience is broader than SRE/platform experts: the tool targets a general technical audience (developers, support, anyone operating around the cluster). The interface therefore prioritizes **graphical, self-explanatory presentation** — visual cues, charts, discoverable menus and contextual help — over terse expert-only output; knowing kubectl/k9s must not be a prerequisite.
- The client reuses the operator's existing Kubernetes credentials and configuration and inherits the operator's permissions — it never elevates privileges (consistent with the constitution's least-privilege principle).
- *(v3)* The tool administers the cluster directly; exec-into-pod and node drain are the remaining out-of-scope actions (still done via kubectl when needed).
- Multi-context support relies on contexts already present in the operator's configuration; the client does not create or manage credentials.
- The primary environment is a standard terminal emulator supporting mouse reporting; graceful degradation is expected where it does not.
- Localization is out of scope for the first version; the interface default language is English.
- The interface must render rich visual elements (charts, gauges, topology, color) in a terminal; this constrains the choice of the underlying interface toolkit (deferred to planning) which MUST support these visuals while preserving full keyboard operability and graceful degradation.
- **Prometheus (or an equivalent) is the single metrics source** for all usage data — instantaneous gauges, per-node "used", top consumers, and the rolling **last-1-hour** trend charts (and later, sizing recommendations). metrics-server is not required. The events timeline depends on cluster event retention. When Prometheus is unreachable or event retention is limited, the tool shows an explicit "unavailable" / limited-window state rather than blocking or fabricating data.
- View customizations are per-operator, stored locally with the operator's other client settings; team-wide shared views are out of scope for the first version.
- No client-side audit trail is kept; mutations are attributable server-side via the API audit log and the "idz-k8s" field manager.
- Workloads are deployed via Helm charts; the tool reads Helm release state (releases, revisions, history, status) and *(v3)* can roll back or uninstall a release behind an explicit confirmation. Install/upgrade stay in the deployment pipeline.
- Ownership, routing, and connectivity graphs are derived from live API objects; relations that depend on annotations (e.g. the diff's last-applied configuration) are shown as unavailable when the annotation is absent.
- Posture rules cover a common baseline (requests/limits, privileged, run-as-root, probes, `latest` image, NetworkPolicy presence, TLS expiry); the specific rule set can grow later and is intentionally advisory, not enforced.
- **v1 scope**: the first version delivers the P1 and P2 user stories (US1 inspection, US2 graphical debug views, US3 keyboard, US4 topology, US5 events timeline, US7 mouse, US9 dependency graph, US10 failure diagnostics, US11 scheduling & capacity, US12 Helm overview). The P3 stories — US6 sizing recommendations, US8 customizable views, US13 posture, US14 connectivity/NetworkPolicy, US15 access/RBAC view, US16 drift diff — are deferred to a later version (backlog), along with their FRs (FR-023, FR-024/FR-025, FR-030, FR-031, FR-032, FR-033) and success criteria (SC-013, SC-014, SC-018).
- **v3 (owner decision, 2026-07-24)**: the administration mode is IN — not as an opt-in flag but as the product itself (see the 2026-07-24 clarification). Every mutating action carries a mandatory confirmation step and is bounded by the operator's RBAC; exec-into-pod and node drain are deferred. The former enforcement tests (zero-mutating-verb sweep, Helm mutating-action grep) are replaced by admin-operation tests plus the UI confirmation-gate tests.
