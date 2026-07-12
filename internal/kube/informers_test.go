package kube

import (
	"sync"
	"testing"
)

// TestNotifyChangeDuringCloseDoesNotPanic: notifyChange runs on informer
// event-handler goroutines while Close() (context switch) closes the changes
// channel. The send must happen under the same mutex as the close — the old
// read-then-send window panicked the whole app with "send on closed channel".
func TestNotifyChangeDuringCloseDoesNotPanic(t *testing.T) {
	c := &Client{}
	for i := 0; i < 500; i++ {
		_ = c.Changes() // (re)create the channel like the UI's waitForChange
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.notifyChange()
			}()
		}
		c.Close()
		wg.Wait()
	}
}
