package peer

import (
	"github.com/decred/dcrd/lru"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"testing"
)

func TestQueueInventoryUsesTypeAndHashIdentity(t *testing.T) {
	hash := chainhash.DoubleHashH([]byte("known-inventory-regression"))
	otherHash := chainhash.DoubleHashH([]byte("different-inventory"))
	for _, tc := range []struct {
		name        string
		kind        wire.InvType
		hash        chainhash.Hash
		samePointer bool
		wantQueued  bool
	}{
		{"same_pointer_control", wire.InvTypeTx, hash, true, false},
		{"equivalent_distinct_pointer", wire.InvTypeTx, hash, false, false},
		{"different_hash_control", wire.InvTypeTx, otherHash, false, true},
		{"different_type_control", wire.InvTypeBlock, hash, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Peer{knownInventory: lru.NewCache(4), connected: 1, outputInvChan: make(chan *wire.InvVect, 4)}
			known := wire.NewInvVect(wire.InvTypeTx, &hash)
			p.AddKnownInventory(known)
			candidate := wire.NewInvVect(tc.kind, &tc.hash)
			if tc.samePointer {
				candidate = known
			}
			p.QueueInventory(candidate)
			queued := len(p.outputInvChan) > 0
			if queued != tc.wantQueued {
				t.Fatalf("QueueInventory queued=%t want=%t for inventory type/hash identity", queued, tc.wantQueued)
			}
		})
	}
}
