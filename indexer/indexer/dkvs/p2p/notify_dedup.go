package p2p

import (
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type peerNotifySeen struct {
	mutex sync.Mutex
	items map[chainhash.Hash]time.Time
}

var notifySeenByPeer sync.Map

// AcceptNotify provides bounded duplicate suppression without changing the
// wire record selector. It protects a peer from replaying the same signed
// record repeatedly while still allowing a later canonical record for the key.
func (s *PeerState) AcceptNotify(record *wire.DKVSRecord, now time.Time) bool {
	if s == nil || record == nil {
		return false
	}
	value, _ := notifySeenByPeer.LoadOrStore(s, &peerNotifySeen{items: make(map[chainhash.Hash]time.Time)})
	seen := value.(*peerNotifySeen)
	hash := dkvs.RecordHash(record)
	seen.mutex.Lock()
	defer seen.mutex.Unlock()
	for candidate, acceptedAt := range seen.items {
		if !now.Before(acceptedAt.Add(RequestTTL)) {
			delete(seen.items, candidate)
		}
	}
	if acceptedAt, ok := seen.items[hash]; ok && now.Before(acceptedAt.Add(RequestTTL)) {
		return false
	}
	seen.items[hash] = now
	return true
}
