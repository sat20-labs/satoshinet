package main

import (
	"container/list"
	"sync"
	"time"
)

const maxDirectReplayEntries = 65536

type directReplayEntry struct {
	key     string
	pending bool
	expires time.Time
	element *list.Element
}

// directReplayWindow is bounded, endpoint-local replay protection for Direct
// delivery retries. It deliberately is not DKVS state: mailbox entries may be
// physically deleted without leaving permanent per-message tombstones.
type directReplayWindow struct {
	mu      sync.Mutex
	entries map[string]*directReplayEntry
	order   *list.List
	max     int
	ttl     time.Duration
}

func newDirectReplayWindow(max int, ttl time.Duration) *directReplayWindow {
	if max <= 0 {
		max = maxDirectReplayEntries
	}
	if ttl <= 0 {
		ttl = defaultDirectAcceptanceTTL
	}
	return &directReplayWindow{
		entries: make(map[string]*directReplayEntry),
		order:   list.New(),
		max:     max,
		ttl:     ttl,
	}
}

func directReplayKey(message *DirectMessage) string {
	if message == nil {
		return ""
	}
	return message.RecipientAccount + "\x00" + message.SenderAccount + "\x00" + message.MessageID
}

// begin reserves one delivery key. duplicate is true only after a previous
// mailbox commit; pending means another handler is still committing the same
// message and therefore must not be acknowledged as durable yet.
func (window *directReplayWindow) begin(key string, now time.Time) (duplicate, pending bool) {
	if window == nil || key == "" {
		return false, false
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	window.pruneExpiredLocked(now)
	if entry := window.entries[key]; entry != nil {
		if entry.pending {
			return false, true
		}
		if now.Before(entry.expires) {
			return true, false
		}
		window.removeLocked(entry)
	}
	window.entries[key] = &directReplayEntry{key: key, pending: true}
	return false, false
}

func (window *directReplayWindow) finish(key string, committed bool, now time.Time) {
	if window == nil || key == "" {
		return
	}
	window.mu.Lock()
	defer window.mu.Unlock()
	entry := window.entries[key]
	if entry == nil || !entry.pending {
		return
	}
	if !committed {
		delete(window.entries, key)
		return
	}
	entry.pending = false
	entry.expires = now.Add(window.ttl)
	entry.element = window.order.PushBack(entry)
	window.pruneExpiredLocked(now)
	for len(window.entries) > window.max && window.order.Len() > 0 {
		oldest, _ := window.order.Front().Value.(*directReplayEntry)
		window.removeLocked(oldest)
	}
}

func (window *directReplayWindow) pruneExpiredLocked(now time.Time) {
	for element := window.order.Front(); element != nil; {
		next := element.Next()
		entry, _ := element.Value.(*directReplayEntry)
		if entry != nil && now.Before(entry.expires) {
			break
		}
		if entry == nil || !now.Before(entry.expires) {
			window.removeLocked(entry)
		}
		element = next
	}
}

func (window *directReplayWindow) removeLocked(entry *directReplayEntry) {
	if entry == nil {
		return
	}
	delete(window.entries, entry.key)
	if entry.element != nil {
		window.order.Remove(entry.element)
		entry.element = nil
	}
}
