package kube

import (
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Shared informers (T089/ex-T010): the main list flow reads from a local
// cache kept fresh by ONE WATCH per resource type, instead of re-LISTing the
// cluster on every refresh tick. Strictly read-only (list+watch). Until a
// type's cache is synced — or if the watch cannot be established — callers
// fall back to a direct LIST, so the view never depends on the cache.
//
// Freshness is never faked: watch errors are tracked and surfaced via
// CacheStale(), so the UI shows its reconnect banner over the cached data
// instead of presenting stale content as live (FR-016, FR-021 spirit).

// staleWindow: a watch error within this window marks the cache unhealthy
// (the reflector retries with backoff; when errors stop, health returns).
const staleWindow = 15 * time.Second

// informerCache lazily manages one dynamic informer per resource type.
type informerCache struct {
	factory dynamicinformer.DynamicSharedInformerFactory
	stop    chan struct{}
	notify  func() // coalesced change signal for the live-refresh loop

	mu      sync.Mutex
	byGVR   map[schema.GroupVersionResource]informers.GenericInformer
	lastErr error
	errAt   time.Time
}

func newInformerCache(f dynamicinformer.DynamicSharedInformerFactory, notify func()) *informerCache {
	return &informerCache{
		factory: f,
		stop:    make(chan struct{}),
		notify:  notify,
		byGVR:   map[schema.GroupVersionResource]informers.GenericInformer{},
	}
}

// forGVR returns the informer for a type, creating and starting it on first
// use. The boolean reports whether its cache is synced and safe to read.
func (ic *informerCache) forGVR(gvr schema.GroupVersionResource) (informers.GenericInformer, bool) {
	ic.mu.Lock()
	gi, known := ic.byGVR[gvr]
	if !known {
		gi = ic.factory.ForResource(gvr)
		// Track watch failures for CacheStale; must be set before start.
		_ = gi.Informer().SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
			ic.mu.Lock()
			ic.lastErr, ic.errAt = err, time.Now()
			ic.mu.Unlock()
		})
		// Live refresh: any add/update/delete signals the UI, which then
		// re-reads the cache (a rolling update is visible as it happens).
		_, _ = gi.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(interface{}) { ic.notify() },
			UpdateFunc: func(interface{}, interface{}) { ic.notify() },
			DeleteFunc: func(interface{}) { ic.notify() },
		})
		ic.byGVR[gvr] = gi
		ic.factory.Start(ic.stop)
	}
	ic.mu.Unlock()
	return gi, gi.Informer().HasSynced()
}

// stale returns the recent watch error, if any.
func (ic *informerCache) stale() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.lastErr != nil && time.Since(ic.errAt) < staleWindow {
		return ic.lastErr
	}
	return nil
}

// cacheFor lazily builds the client's informer cache.
func (c *Client) cacheFor() *informerCache {
	c.infMu.Lock()
	defer c.infMu.Unlock()
	if c.inf == nil {
		c.inf = newInformerCache(
			dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.Dynamic, 0, metav1.NamespaceAll, nil),
			c.notifyChange)
	}
	return c.inf
}

// Changes returns a coalesced signal channel: one (buffered) tick whenever
// anything changes in a watched cache. Closed by Close() so waiters can
// re-arm on the replacement client.
func (c *Client) Changes() <-chan struct{} {
	c.infMu.Lock()
	defer c.infMu.Unlock()
	if c.changes == nil {
		c.changes = make(chan struct{}, 1)
	}
	return c.changes
}

// notifyChange signals without ever blocking an informer goroutine (the
// buffered channel coalesces bursts — a rolling update is one signal). The
// non-blocking send happens under infMu so it can never race with Close()
// closing the channel (send on closed channel would panic the whole app).
func (c *Client) notifyChange() {
	c.infMu.Lock()
	defer c.infMu.Unlock()
	if c.changes == nil {
		return
	}
	select {
	case c.changes <- struct{}{}:
	default:
	}
}

// Close stops every informer goroutine and the change stream. Call it when
// replacing the client (context switch); safe on a client that never
// started any.
func (c *Client) Close() {
	c.infMu.Lock()
	defer c.infMu.Unlock()
	if c.inf != nil {
		close(c.inf.stop)
		c.inf = nil
	}
	if c.changes != nil {
		close(c.changes)
		c.changes = nil
	}
}

// CacheStale reports a recent watch failure: the UI keeps showing the cached
// data but must announce the lost connection rather than fake freshness.
func (c *Client) CacheStale() error {
	c.infMu.Lock()
	inf := c.inf
	c.infMu.Unlock()
	if inf == nil {
		return nil
	}
	return inf.stale()
}

// UsingCache reports whether a type's list is currently served from the
// informer cache (test observability + telemetry).
func (c *Client) UsingCache(t model.ResourceType) bool {
	if c == nil || c.Dynamic == nil {
		return false
	}
	_, synced := c.cacheFor().forGVR(gvr(t))
	return synced
}

// cachedList serves ListSelected from the informer cache when possible.
// ok=false means "use a direct LIST" (no client, first call not yet synced,
// or an unparseable selector).
func (c *Client) cachedList(t model.ResourceType, namespace, labelSelector string) ([]model.ResourceObject, bool) {
	if c == nil || c.Dynamic == nil {
		return nil, false
	}
	sel := labels.Everything()
	if labelSelector != "" {
		parsed, err := labels.Parse(labelSelector)
		if err != nil {
			return nil, false
		}
		sel = parsed
	}
	gi, synced := c.cacheFor().forGVR(gvr(t))
	if !synced {
		return nil, false
	}
	apiNS, pattern := namespaceScope(namespace)
	var (
		items []runtime.Object
		err   error
	)
	if t.Namespaced && apiNS != "" {
		items, err = gi.Lister().ByNamespace(apiNS).List(sel)
	} else {
		items, err = gi.Lister().List(sel)
	}
	if err != nil {
		return nil, false
	}
	out := make([]model.ResourceObject, 0, len(items))
	for _, it := range items {
		u, ok := it.(*unstructured.Unstructured)
		if !ok {
			return nil, false
		}
		if t.Namespaced && pattern != "" && !MatchNamespace(pattern, u.GetNamespace()) {
			continue
		}
		// Cache objects are shared and MUST NOT be mutated in place —
		// admin operations always go through the API, never the cache.
		out = append(out, toResourceObject(t, u))
	}
	sortObjects(out)
	return out, true
}
