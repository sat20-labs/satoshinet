package p2p

import "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

// PathSnapshotStore is the mandatory full-path reconciliation surface used by
// DKVS v1 gap recovery and anti-entropy.
type PathSnapshotStore interface {
	GetDKVSPathSnapshot(path string) (*dkvs.PathSnapshot, error)
	ApplyDKVSPathSnapshot(snapshot *dkvs.PathSnapshot) (int, error)
}
