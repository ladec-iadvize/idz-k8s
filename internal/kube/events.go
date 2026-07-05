package kube

import (
	"context"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

var eventsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}

// Events lists cluster events in a namespace (empty → all), most recent first
// (US5, read-only). Cluster events have limited retention, so this is a window,
// not a complete history — the view labels it as such.
func (c *Client) Events(ctx context.Context, namespace string) ([]model.Event, error) {
	ri := c.Dynamic.Resource(eventsGVR)
	var (
		ul  *unstructured.UnstructuredList
		err error
	)
	if namespace != "" {
		ul, err = ri.Namespace(namespace).List(ctx, metav1.ListOptions{})
	} else {
		ul, err = ri.List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}
	out := make([]model.Event, 0, len(ul.Items))
	for i := range ul.Items {
		out = append(out, parseEvent(&ul.Items[i]))
	}
	// Most recent first.
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func parseEvent(u *unstructured.Unstructured) model.Event {
	obj := u.Object
	etype, _, _ := unstructured.NestedString(obj, "type")
	reason, _, _ := unstructured.NestedString(obj, "reason")
	message, _, _ := unstructured.NestedString(obj, "message")
	kind, _, _ := unstructured.NestedString(obj, "involvedObject", "kind")
	name, _, _ := unstructured.NestedString(obj, "involvedObject", "name")
	count := nestedInt(obj, "count")

	return model.Event{
		Time:      eventTime(obj),
		Type:      etype,
		Reason:    reason,
		Message:   message,
		ObjKind:   kind,
		ObjName:   name,
		Namespace: u.GetNamespace(),
		Count:     count,
	}
}

// eventTime picks the most relevant timestamp: lastTimestamp, then eventTime,
// then firstTimestamp, then creationTimestamp.
func eventTime(obj map[string]interface{}) time.Time {
	for _, path := range [][]string{
		{"lastTimestamp"},
		{"eventTime"},
		{"firstTimestamp"},
		{"metadata", "creationTimestamp"},
	} {
		if s, found, _ := unstructured.NestedString(obj, path...); found && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
