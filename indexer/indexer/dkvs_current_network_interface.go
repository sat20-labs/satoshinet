package indexer

import (
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// This surface is consumed only by the authenticated P2P handler. HTTP clients
// cannot select a replication source or install a node's current-state view.
func (p *IndexerMgr) AcceptDKVSCurrentRecord(record *wire.DKVSRecord) (bool, error) {
	if p == nil || p.dkvsIndexer == nil { return false, errDKVSNotInitialized }
	return p.dkvsIndexer.AcceptCurrentRecord(record)
}

func (p *IndexerMgr) DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error) {
	if p == nil || p.dkvsIndexer == nil { return dkvs.ActiveMeta{}, errDKVSNotInitialized }
	return p.dkvsIndexer.NetworkSyncBaseline(path)
}

func (p *IndexerMgr) ApplyDKVSPathSnapshotFrom(snapshot *dkvs.PathSnapshot, baseline dkvs.ActiveMeta) (int, error) {
	if p == nil || p.dkvsIndexer == nil { return 0, errDKVSNotInitialized }
	return p.dkvsIndexer.ApplyPathSnapshotFrom(snapshot, baseline)
}

func (p *IndexerMgr) DKVSNetworkPaths() ([]string, error) {
	if p == nil || p.dkvsIndexer == nil { return nil, errDKVSNotInitialized }
	return p.dkvsIndexer.NetworkPaths()
}
