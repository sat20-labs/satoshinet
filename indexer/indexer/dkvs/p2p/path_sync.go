package p2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	pathSyncFilterType    = "path"
	pathSyncMetaFlag      = uint32(1 << 31)
	pathSyncMetaVersion   = uint32(2)
	pathSyncCursorVersion = byte(3)
	// offset + captured snapshot root. No height or node-local generation.
	pathSyncCursorSize     = 1 + 8 + chainhash.HashSize
	PathSyncRequestTimeout = 10 * time.Second
	PathSyncRetryDelay     = time.Second
)

func pathSyncFilter(filters []wire.DKVSSyncFilter) (string, bool) {
	if len(filters) != 1 || filters[0].Type != pathSyncFilterType {
		return "", false
	}
	path := strings.TrimSuffix(strings.TrimSpace(filters[0].Target), "/")
	if path == "" {
		return "", false
	}
	return path, true
}

func pathSyncMatchesKey(path, key string) bool {
	candidate, err := dkvs.CollectionPathForKey(key)
	return err == nil && candidate == path
}

func pathSyncMetaKey(path string) string {
	hash := sha256.Sum256([]byte(path))
	return "\x00dkvs-pathmeta:" + hex.EncodeToString(hash[:])
}

func encodePathSyncMeta(snapshot *dkvs.PathSnapshot) (*wire.DKVSRecord, error) {
	if snapshot == nil || snapshot.PathMeta == nil || snapshot.Path == "" {
		return nil, dkvs.ErrInvalidSnapshot
	}
	path := []byte(snapshot.Path)
	if len(path) > wire.MaxDKVSKeySize {
		return nil, dkvs.ErrInvalidKey
	}
	value := make([]byte, 2+len(path)+4+5*8+chainhash.HashSize)
	binary.BigEndian.PutUint16(value[0:2], uint16(len(path)))
	copy(value[2:2+len(path)], path)
	offset := 2 + len(path)
	binary.BigEndian.PutUint32(value[offset:offset+4], snapshot.PathMeta.Version)
	offset += 4
	for _, field := range []uint64{snapshot.PathMeta.ActiveRecords,
		snapshot.PathMeta.ActiveTotalSize, snapshot.PathMeta.MinExpiryHeight, snapshot.PathMeta.ViewHeight, snapshot.ServerTimeMS} {
		binary.BigEndian.PutUint64(value[offset:offset+8], field)
		offset += 8
	}
	copy(value[offset:], snapshot.PathMeta.StateRoot[:])
	return &wire.DKVSRecord{Version: pathSyncMetaVersion, Key: pathSyncMetaKey(snapshot.Path),
		Value: value, Seq: 1, Flags: pathSyncMetaFlag}, nil
}

func decodePathSyncMeta(record *wire.DKVSRecord) (string, *dkvs.PathMeta, uint64, error) {
	if record == nil || record.Flags != pathSyncMetaFlag || record.Version != pathSyncMetaVersion || len(record.Value) < 2+4+5*8+chainhash.HashSize {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	pathSize := int(binary.BigEndian.Uint16(record.Value[0:2]))
	if pathSize == 0 || pathSize > wire.MaxDKVSKeySize || len(record.Value) != 2+pathSize+4+5*8+chainhash.HashSize {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	path := string(record.Value[2 : 2+pathSize])
	if record.Key != pathSyncMetaKey(path) || record.Seq != 1 {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	offset := 2 + pathSize
	meta := &dkvs.PathMeta{Version: binary.BigEndian.Uint32(record.Value[offset : offset+4]), Path: path}
	offset += 4
	for _, field := range []*uint64{&meta.ActiveRecords, &meta.ActiveTotalSize, &meta.MinExpiryHeight, &meta.ViewHeight} {
		*field = binary.BigEndian.Uint64(record.Value[offset : offset+8])
		offset += 8
	}
	serverTimeMS := binary.BigEndian.Uint64(record.Value[offset : offset+8])
	offset += 8
	copy(meta.StateRoot[:], record.Value[offset:])
	if meta.Version == 0 || serverTimeMS == 0 {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	return path, meta, serverTimeMS, nil
}

func encodePathSyncCursor(offset uint64, root chainhash.Hash) []byte {
	cursor := make([]byte, pathSyncCursorSize)
	cursor[0] = pathSyncCursorVersion
	binary.BigEndian.PutUint64(cursor[1:9], offset)
	copy(cursor[9:9+chainhash.HashSize], root[:])
	return cursor
}

func decodePathSyncCursor(cursor []byte) (uint64, chainhash.Hash, error) {
	if len(cursor) != pathSyncCursorSize || cursor[0] != pathSyncCursorVersion {
		return 0, chainhash.Hash{}, dkvs.ErrInvalidSnapshot
	}
	offset := binary.BigEndian.Uint64(cursor[1:9])
	var root chainhash.Hash
	copy(root[:], cursor[9:9+chainhash.HashSize])
	return offset, root, nil
}

// The only control row in a current-state snapshot is its prefix metadata.
// Deletion commands travel as live operations, never as snapshot history.
func pathSnapshotWireRecords(snapshot *dkvs.PathSnapshot) ([]*wire.DKVSRecord, error) {
	meta, err := encodePathSyncMeta(snapshot)
	if err != nil {
		return nil, err
	}
	records := make([]*wire.DKVSRecord, 0, 1+len(snapshot.Records))
	records = append(records, meta)
	seen := make(map[string]struct{}, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if record == nil || record.Flags != 0 || !pathSyncMatchesKey(snapshot.Path, record.Key) {
			return nil, dkvs.ErrInvalidSnapshot
		}
		if _, duplicate := seen[record.Key]; duplicate {
			return nil, dkvs.ErrInvalidSnapshot
		}
		seen[record.Key] = struct{}{}
		records = append(records, record)
	}
	return records, nil
}

func pathSnapshotPage(snapshot *dkvs.PathSnapshot, cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, error) {
	if snapshot == nil || snapshot.PathMeta == nil {
		return nil, nil, false, dkvs.ErrInvalidSnapshot
	}
	offset := uint64(0)
	if len(cursor) != 0 {
		previousOffset, previousRoot, err := decodePathSyncCursor(cursor)
		if err != nil {
			return nil, nil, false, err
		}
		if previousRoot != snapshot.PathMeta.StateRoot {
			return nil, nil, false, dkvs.ErrPathDiverged
		}
		offset = previousOffset
	}
	meta, err := encodePathSyncMeta(snapshot)
	if err != nil {
		return nil, nil, false, err
	}
	total := 1 + len(snapshot.Records)
	if offset > uint64(total) {
		return nil, nil, false, dkvs.ErrInvalidSnapshot
	}
	maxRecords := int(limit)
	if maxRecords <= 0 || maxRecords > wire.MaxDKVSRecordsPerMsg {
		maxRecords = wire.MaxDKVSRecordsPerMsg
	}
	page := make([]*wire.DKVSRecord, 0, maxRecords)
	size, index := 64, int(offset)
	for index < total && len(page) < maxRecords {
		record := meta
		if index != 0 {
			record = snapshot.Records[index-1]
		}
		recordSize := wire.DKVSRecordSerializeSize(record)
		if recordSize > wire.MaxDKVSRecordsPayloadSize-64 {
			return nil, nil, false, dkvs.ErrRecordTooLarge
		}
		if recordSize > wire.MaxDKVSRecordsPayloadSize-size {
			break
		}
		page = append(page, record)
		size += recordSize
		index++
	}
	if len(page) == 0 && index < total {
		return nil, nil, false, dkvs.ErrRecordTooLarge
	}
	if index == total {
		return page, nil, true, nil
	}
	return page, encodePathSyncCursor(uint64(index), snapshot.PathMeta.StateRoot), false, nil
}

func decodePathSnapshot(path string, root chainhash.Hash, records, deletes []*wire.DKVSRecord) (*dkvs.PathSnapshot, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" || len(deletes) != 0 {
		return nil, dkvs.ErrInvalidSnapshot
	}
	var meta *dkvs.PathMeta
	var serverTimeMS uint64
	snapshot := &dkvs.PathSnapshot{Path: path}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record == nil {
			return nil, dkvs.ErrInvalidSnapshot
		}
		switch record.Flags {
		case pathSyncMetaFlag:
			decodedPath, decodedMeta, decodedTime, err := decodePathSyncMeta(record)
			if err != nil || decodedPath != path || meta != nil {
				return nil, dkvs.ErrInvalidSnapshot
			}
			meta, serverTimeMS = decodedMeta, decodedTime
		case 0:
			if !pathSyncMatchesKey(path, record.Key) {
				return nil, dkvs.ErrInvalidSnapshot
			}
			if _, duplicate := seen[record.Key]; duplicate {
				return nil, dkvs.ErrInvalidSnapshot
			}
			seen[record.Key] = struct{}{}
			snapshot.Records = append(snapshot.Records, record)
		default:
			return nil, dkvs.ErrInvalidSnapshot
		}
	}
	if meta == nil || meta.StateRoot != root {
		return nil, dkvs.ErrPathDiverged
	}
	snapshot.PathMeta, snapshot.ServerTimeMS = meta, serverTimeMS
	return snapshot, nil
}

func (s *PeerState) startPathSyncLocked(path string, now time.Time) (SyncStart, error) {
	session, err := wire.RandomUint64()
	if err != nil || session == 0 {
		return SyncStart{}, err
	}
	filters := []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: path}}
	s.syncSession, s.syncCursor, s.syncFilters = session, nil, filters
	s.syncRecords = make(map[string]*wire.DKVSRecord)
	s.syncDeletes = make(map[string]*wire.DKVSRecord)
	s.syncLastResponse = nil
	s.syncBytes, s.syncRoot, s.syncRootSet = 0, chainhash.Hash{}, false
	s.syncActive, s.syncUpdated, s.syncBaseline = true, now, nil
	return SyncStart{Request: &wire.MsgDKVSSyncRequest{SessionID: session, Limit: wire.MaxDKVSRecordsPerMsg, Filters: filters}, Started: true}, nil
}

// Only the node scheduler starts a new path session. PeerState owns its pages.
func (s *PeerState) StartPathSync(path string, now time.Time) (SyncStart, error) {
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	if s.Closed() {
		return SyncStart{}, nil
	}
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" {
		return SyncStart{}, dkvs.ErrInvalidKey
	}
	if s.syncActive {
		return SyncStart{}, nil
	}
	return s.startPathSyncLocked(path, now)
}

// Expire only this outstanding page, never a newer cursor/session.
func (s *PeerState) TimeoutPathSyncRequest(session uint64, cursor []byte) bool {
	if s == nil || session == 0 || s.Closed() {
		return false
	}
	s.syncMtx.Lock()
	if !s.syncActive || s.syncSession != session || !bytes.Equal(s.syncCursor, cursor) {
		s.syncMtx.Unlock()
		return false
	}
	node := s.syncNode
	s.resetSyncLocked()
	s.syncMtx.Unlock()
	if node != nil {
		node.finishSync(s, false)
	}
	return true
}

func (s *PeerState) ActivePathSync() (string, bool) {
	if s == nil {
		return "", false
	}
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	if !s.syncActive {
		return "", false
	}
	return pathSyncFilter(s.syncFilters)
}

func (h Handler) sendPathSyncRequest(request *wire.MsgDKVSSyncRequest) {
	h.sendPathSyncRequestAttempt(request, 1)
}

func (h Handler) sendPathSyncRequestAttempt(request *wire.MsgDKVSSyncRequest, attempt int) {
	if !h.valid() || request == nil || h.Peer.Closed() {
		return
	}
	session, cursor := request.SessionID, append([]byte(nil), request.Cursor...)
	h.Peer.syncMtx.Lock()
	if !h.Peer.syncActive || h.Peer.syncSession != session || !bytes.Equal(h.Peer.syncCursor, cursor) {
		h.Peer.syncMtx.Unlock()
		return
	}
	h.Peer.scheduleSync("request", PathSyncRequestTimeout, func() {
		if attempt == 1 {
			// Retry this outstanding page once. The helper checks the session
			// under the page mutex so an old timer cannot replace a new one.
			h.sendPathSyncRequestAttempt(request, 2)
			return
		}
		if h.Peer.TimeoutPathSyncRequest(session, cursor) {
			h.warnf("DKVS snapshot request timed out after two attempts")
			if h.RequestPathRepair != nil {
				path, _ := pathSyncFilter(request.Filters)
				h.RequestPathRepair(path)
			} else {
				h.queueNextPathSync()
			}
		}
	})
	h.Peer.syncMtx.Unlock()
	if attempt == 1 {
		h.send(request)
	} else if h.Send != nil {
		// Resending does not recapture the receiver's installation baseline.
		h.Send(request)
	}
}

func (h Handler) finishFailedPathSync(path string, retry bool) {
	h.Node.finishSync(h.Peer, false)
	// Keep the failed job queued even when this source cannot retry it.
	if h.queueNextNodeSync(path, true) {
		return
	}
	if retry && !h.Peer.Closed() {
		h.Peer.scheduleSync("retry:"+path, PathSyncRetryDelay, func() { h.queueNextPathSync() })
	} else if h.RequestPathRepair != nil {
		h.RequestPathRepair(path)
	}
}

func (h Handler) queueNextPathSync() bool { return h.queueNextNodeSync("", false) }

func (h Handler) QueuePathSync(path string) {
	if !h.valid() || !h.allowedPathSnapshotSource() || h.Peer.Closed() {
		return
	}
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" {
		return
	}
	h.Node.pendingMtx.Lock()
	h.Node.enqueuePathSyncLocked(path)
	h.Node.pendingMtx.Unlock()
	h.queueNextPathSync()
}

func (h Handler) queuePathRepair(record *wire.DKVSRecord, err error) bool {
	if record == nil || err == nil {
		return false
	}
	if !errors.Is(err, dkvs.ErrPathGenerationGap) && !errors.Is(err, dkvs.ErrStaleEndpoint) && !errors.Is(err, dkvs.ErrPathDiverged) {
		return false
	}
	path, pathErr := dkvs.CollectionPathForKey(record.Key)
	if pathErr != nil {
		return false
	}
	if h.allowedPathSnapshotSource() {
		h.QueuePathSync(path)
	} else if h.RequestPathRepair != nil {
		h.RequestPathRepair(path)
	}
	return true
}

func (h Handler) servePathSync(msg *wire.MsgDKVSSyncRequest) bool {
	path, ok := pathSyncFilter(msg.Filters)
	if !ok {
		return false
	}
	snapshot, err := h.Peer.pathServeSnapshot(msg, h.Store, path)
	if err != nil {
		h.Peer.CancelServe()
		h.debugf("DKVS path snapshot %s failed: %v", path, err)
		return true
	}
	records, next, done, err := pathSnapshotPage(snapshot, msg.Cursor, msg.Limit)
	if err != nil {
		h.Peer.CancelServe()
		h.debugf("DKVS path snapshot page %s failed: %v", path, err)
		return true
	}
	response := &wire.MsgDKVSSyncResponse{SessionID: msg.SessionID, Records: records, NextCursor: next, Done: done, CheckpointRoot: snapshot.PathMeta.StateRoot}
	if h.Sign == nil {
		h.Peer.CancelServe()
		return true
	}
	response.SourceSignature, err = h.Sign(SyncAuthPayload(h.Net, msg.Cursor, msg.Filters, response))
	if err != nil {
		h.Peer.CancelServe()
		h.debugf("sign DKVS path snapshot failed: %v", err)
		return true
	}
	h.send(response)
	if done {
		h.Peer.FinishServe(msg.SessionID)
	}
	return true
}

func pathActionSnapshot(action SyncAction) (*dkvs.PathSnapshot, bool, error) {
	path, ok := pathSyncFilter(action.Filters)
	if !ok {
		return nil, false, nil
	}
	snapshot, err := decodePathSnapshot(path, action.Root, action.MirrorRecords, action.MirrorDeletes)
	return snapshot, true, err
}
