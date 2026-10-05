package p2p

import (
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// The existing serving session owns one captured current view. Height advances
// and later writes cannot change its pages; the receiver buffers live Notify.
func (s *PeerState) pathServeSnapshot(msg *wire.MsgDKVSSyncRequest, store Store, path string) (*dkvs.PathSnapshot, error) {
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if !s.serveActive || s.serveSession != msg.SessionID || !filtersEqual(s.serveFilters, msg.Filters) {
		return nil, ErrUnsolicitedSync
	}
	if s.serveSnapshot == nil {
		if len(msg.Cursor) != 0 {
			return nil, dkvs.ErrInvalidSnapshot
		}
		snapshot, err := store.GetDKVSPathSnapshot(path)
		if err != nil {
			return nil, err
		}
		if snapshot == nil || snapshot.PathMeta == nil {
			return nil, dkvs.ErrInvalidSnapshot
		}
		if _, err := pathSnapshotWireRecords(snapshot); err != nil {
			return nil, err
		}
		var size uint64
		for _, record := range snapshot.Records {
			if record == nil {
				return nil, dkvs.ErrInvalidSnapshot
			}
			recordSize := uint64(dkvs.RecordSize(record))
			if recordSize > dkvs.MaxNetworkSnapshotBytes-size {
				return nil, dkvs.ErrBatchTooLarge
			}
			size += recordSize
		}
		s.serveSnapshot = snapshot
	}
	// Expire an abandoned snapshot using the existing connection timer facility.
	// Capture its pointer so an old timer cannot clear a replacement session.
	snapshot := s.serveSnapshot
	session := msg.SessionID
	if !s.scheduleSync("serve", SyncSessionTimeout, func() { s.expirePathServe(session, snapshot) }) {
		s.serveActive, s.serveSnapshot = false, nil
		return nil, ErrSyncStagingLimit
	}
	return snapshot, nil
}

func (s *PeerState) expirePathServe(session uint64, snapshot *dkvs.PathSnapshot) {
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if s.serveSession == session && s.serveSnapshot == snapshot {
		s.serveActive, s.serveSnapshot = false, nil
	}
}
