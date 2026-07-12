package unit

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/bubbles/key"

	"github.com/iadvize/idz-k8s/internal/ui/keys"
)

// bindingFields walks every key.Binding field of the KeyMap by reflection —
// the previous hand-written list silently missed 15 bindings added since v1,
// so the "fails on an undiscoverable binding" promise did not hold.
func bindingFields(t *testing.T) map[string]key.Binding {
	t.Helper()
	k := keys.Default()
	v := reflect.ValueOf(k)
	out := map[string]key.Binding{}
	for i := 0; i < v.NumField(); i++ {
		b, ok := v.Field(i).Interface().(key.Binding)
		if !ok {
			t.Errorf("KeyMap field %s is not a key.Binding — update the guard tests", v.Type().Field(i).Name)
			continue
		}
		out[v.Type().Field(i).Name] = b
	}
	if len(out) < 25 {
		t.Fatalf("reflection found only %d bindings — KeyMap changed shape?", len(out))
	}
	return out
}

// TestEveryBindingHasHelp keeps the help overlay in sync with the keymap:
// a binding without help text would be undiscoverable (FR-010).
func TestEveryBindingHasHelp(t *testing.T) {
	for name, b := range bindingFields(t) {
		if len(b.Keys()) == 0 {
			t.Errorf("%s: no key bound", name)
		}
		if b.Help().Key == "" || b.Help().Desc == "" {
			t.Errorf("%s: no help text (undiscoverable)", name)
		}
	}
}

// TestFullHelpListsEveryBinding: the '?' overlay is the discoverability
// contract — a binding absent from FullHelp is invisible to the user even
// when its help text exists.
func TestFullHelpListsEveryBinding(t *testing.T) {
	k := keys.Default()
	seen := map[string]bool{}
	for _, group := range k.FullHelp() {
		for _, b := range group {
			seen[b.Help().Key+" "+b.Help().Desc] = true
		}
	}
	for name, b := range bindingFields(t) {
		if !seen[b.Help().Key+" "+b.Help().Desc] {
			t.Errorf("%s (%q) missing from FullHelp — invisible in the help overlay", name, b.Help().Key)
		}
	}
}
