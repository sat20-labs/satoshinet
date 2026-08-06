package p2p

import (
	"bytes"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func (s *PeerState) ContinuePathSync(cursor []byte, now time.Time) (*wire.MsgDKVSSyncRequest, error) {
	if s == nil || len(cursor) == 0 {
		return nil, dkvs.ErrInvalidSnapshot
	}
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	path, ok := pathSyncFilter(s.syncFilters)
	if !s.syncActive || !ok || s.syncSession == 0 ||
		bytes.Equal(cursor, s.syncCursor) || syncSessionExpired(s.syncUpdated, now) {
		return nil, ErrSyncCursor
	}
	s.syncCursor = append(s.syncCursor[:0], cursor...)
	s.syncUpdated = now
	return &wire.MsgDKVSSyncRequest{
		SessionID: s.syncSession,
		Cursor:    append([]byte(nil), cursor...),
		Limit:     wire.MaxDKVSRecordsPerMsg,
		Filters: []wire.DKVSSyncFilter{{
			Type: pathSyncFilterType, Target: path,
		}},
	}, nil
}

func (h Handler) queuePathSyncContinuation(cursor []byte) {
	if !h.valid() {
		return
	}
	request, err := h.Peer.ContinuePathSync(cursor, time.Now())
	if err == nil && request != nil {
		h.sendPathSyncRequest(request)
	}
}
