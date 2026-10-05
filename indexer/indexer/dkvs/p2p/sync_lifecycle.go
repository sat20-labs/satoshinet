package p2p

import (
	"sync"
	"time"
)

// Timers belong to one connection, not to its database or to a global retry
// registry. At most one page timeout, one serve timeout and one retry per pending path are kept.
// They contain no KV history and are never persisted.
type peerSyncLifecycle struct {
	mu      sync.Mutex
	closed  bool
	timers  map[string]*time.Timer
	running sync.WaitGroup
}

func (s *PeerState) Closed() bool {
	if s == nil {
		return true
	}
	s.syncLifecycle.mu.Lock()
	defer s.syncLifecycle.mu.Unlock()
	return s.syncLifecycle.closed
}

func (s *PeerState) cancelSync(key string) {
	life := &s.syncLifecycle
	life.mu.Lock()
	defer life.mu.Unlock()
	if timer := life.timers[key]; timer != nil {
		timer.Stop()
		delete(life.timers, key)
	}
}

func (s *PeerState) scheduleSync(key string, delay time.Duration, callback func()) bool {
	if s == nil || key == "" || callback == nil {
		return false
	}
	life := &s.syncLifecycle
	life.mu.Lock()
	defer life.mu.Unlock()
	if life.closed {
		return false
	}
	if life.timers == nil {
		life.timers = make(map[string]*time.Timer)
	}
	if previous := life.timers[key]; previous != nil {
		previous.Stop()
	} else if len(life.timers) >= MaxPendingChanges+2 {
		return false
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		life.mu.Lock()
		if life.closed || life.timers[key] != timer {
			life.mu.Unlock()
			return
		}
		delete(life.timers, key)
		// Add is protected by the same lock as Close's closed flag. After
		// Close releases the lock no new callback can enter the wait group.
		life.running.Add(1)
		life.mu.Unlock()
		defer life.running.Done()
		callback()
	})
	life.timers[key] = timer
	return true
}

// Close cancels delayed synchronization and waits for callbacks that already
// started, then releases the peer's temporary synchronization/request state.
// The connection owner calls it after stopping direct message dispatch and
// before releasing the Store. It must not be called from a timer callback.
func (s *PeerState) Close() {
	if s == nil {
		return
	}
	life := &s.syncLifecycle
	life.mu.Lock()
	life.closed = true
	for _, timer := range life.timers {
		timer.Stop()
	}
	life.timers = nil
	life.mu.Unlock()
	life.running.Wait()
	s.syncMtx.Lock()
	s.resetSyncLocked()
	node := s.syncNode
	s.syncNode = nil
	s.syncMtx.Unlock()
	if node != nil {
		node.finishSync(s, false)
	}
	s.CancelServe()
	s.requestMtx.Lock()
	s.requested = nil
	s.requestMtx.Unlock()
}
