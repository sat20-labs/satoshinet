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
var _ PathSnapshotStore = (*handlerTestStore)(nil)
var _ = wire.MaxDKVSRecordsPerMsg
