package p2p

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerSyncLifecycle(t *testing.T) {
	t.Run("PendingTimersAreCoalescedAndCanceled", func(t *testing.T) {
		state := &PeerState{}
		defer state.Close()
		var calls atomic.Int64
		for n := 0; n < 100; n++ {
			if !state.scheduleSync("request", time.Hour, func() { calls.Add(1) }) {
				t.Fatal("schedule rejected")
			}
		}
		state.syncLifecycle.mu.Lock()
		count := len(state.syncLifecycle.timers)
		state.syncLifecycle.mu.Unlock()
		if count != 1 {
			t.Fatalf("pending page timers=%d want=1", count)
		}
		state.Close()
		state.syncLifecycle.mu.Lock()
		count = len(state.syncLifecycle.timers)
		state.syncLifecycle.mu.Unlock()
		if count != 0 || calls.Load() != 0 {
			t.Fatalf("closed state timers=%d callbacks=%d", count, calls.Load())
		}
		if state.scheduleSync("request", 0, func() { calls.Add(1) }) {
			t.Fatal("closed peer accepted another timer")
		}
	})

	t.Run("CloseWaitsForCallbackAlreadyInFlight", func(t *testing.T) {
		state := &PeerState{}
		started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer state.Close()
		defer unblock()
		if !state.scheduleSync("retry", 0, func() { close(started); <-release }) {
			t.Fatal("schedule rejected")
		}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("callback did not start")
		}
		go func() { state.Close(); close(closed) }()
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for !state.Closed() {
			select {
			case <-deadline.C:
				t.Fatal("Close did not mark the peer closed")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		select {
		case <-closed:
			t.Fatal("Close returned while the store callback was still running")
		default:
		}
		unblock()
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("Close did not drain the completed callback")
		}
	})

	t.Run("ClosedPeerCannotRestartSynchronization", func(t *testing.T) {
		state := &PeerState{}
		state.Close()
		start, err := state.StartPathSync("/svc/example", time.Now())
		if err != nil || start.Request != nil {
			t.Fatalf("closed StartPathSync=%+v err=%v", start, err)
		}
		start, err = state.StartSync(nil, true, nil, time.Now())
		if err != nil || start.Request != nil {
			t.Fatalf("closed StartSync=%+v err=%v", start, err)
		}
	})
}
