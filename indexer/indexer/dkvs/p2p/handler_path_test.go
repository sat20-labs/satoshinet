package p2p

import (
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func (s *handlerTestStore) GetDKVSPathSnapshot(string) (*dkvs.PathSnapshot, error) {
	if s.pathSnapshot != nil {
		return s.pathSnapshot, nil
	}
	return nil, dkvs.ErrRecordNotFound
}

func (s *handlerTestStore) ApplyDKVSPathSnapshot(snapshot *dkvs.PathSnapshot) (int, error) {
	s.appliedPathSnapshot = snapshot
	if snapshot == nil {
		return 0, dkvs.ErrInvalidSnapshot
	}
	return len(snapshot.Records), nil
}

var _ Store = (*handlerTestStore)(nil)
var _ = wire.MaxDKVSRecordsPerMsg

func (s *handlerTestStore) DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error) {
	return dkvs.ActiveMeta{Scope: dkvs.ActiveScope{Prefix: path, Network: true}}, nil
}
func (s *handlerTestStore) ApplyDKVSPathSnapshotFrom(snapshot *dkvs.PathSnapshot, _ dkvs.ActiveMeta) (int, error) {
	return s.ApplyDKVSPathSnapshot(snapshot)
}
func (s *handlerTestStore) DKVSNetworkPaths() ([]string, error) { return nil, nil }
