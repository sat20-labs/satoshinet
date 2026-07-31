package p2p

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	pathSyncFilterType = "path"
	pathSyncMetaFlag   = uint32(1 << 31)
	pathSyncFloorFlag  = uint32(1 << 30)
	pathSyncMetaV1     = uint32(1)
	pathSyncCursorV1   = byte(1)
)

const pathSyncCursorSize = 1 + 8 + chainhash.HashSize + 8

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
	value := make([]byte, 2+len(path)+4+6*8+chainhash.HashSize)
	binary.BigEndian.PutUint16(value[0:2], uint16(len(path)))
	copy(value[2:2+len(path)], path)
	offset := 2 + len(path)
	binary.BigEndian.PutUint32(value[offset:offset+4], snapshot.PathMeta.Version)
	offset += 4
	for _, field := range []uint64{
		snapshot.PathMeta.Generation,
		snapshot.PathMeta.ActiveRecords,
		snapshot.PathMeta.ActiveTotalSize,
		snapshot.PathMeta.MinExpiryHeight,
		snapshot.PathMeta.ViewHeight,
		snapshot.ServerTimeMS,
	} {
		binary.BigEndian.PutUint64(value[offset:offset+8], field)
		offset += 8
	}
	copy(value[offset:], snapshot.PathMeta.StateRoot[:])
	return &wire.DKVSRecord{
		Version:        pathSyncMetaV1,
		Key:            pathSyncMetaKey(snapshot.Path),
		Value:          value,
		Seq:            snapshot.PathMeta.Generation,
		PathGeneration: snapshot.PathMeta.Generation,
		Flags:          pathSyncMetaFlag,
	}, nil
}

func decodePathSyncMeta(record *wire.DKVSRecord) (string, *dkvs.PathMeta, uint64, error) {
	if record == nil || record.Flags != pathSyncMetaFlag || record.Version != pathSyncMetaV1 ||
		len(record.Value) < 2+4+6*8+chainhash.HashSize {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	pathSize := int(binary.BigEndian.Uint16(record.Value[0:2]))
	want := 2 + pathSize + 4 + 6*8 + chainhash.HashSize
	if pathSize == 0 || len(record.Value) != want {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	path := string(record.Value[2 : 2+pathSize])
	if record.Key != pathSyncMetaKey(path) {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	offset := 2 + pathSize
	meta := &dkvs.PathMeta{Version: binary.BigEndian.Uint32(record.Value[offset : offset+4]), Path: path}
	offset += 4
	fields := []*uint64{
		&meta.Generation,
		&meta.ActiveRecords,
		&meta.ActiveTotalSize,
		&meta.MinExpiryHeight,
		&meta.ViewHeight,
	}
	for _, field := range fields {
		*field = binary.BigEndian.Uint64(record.Value[offset : offset+8])
		offset += 8
	}
	serverTimeMS := binary.BigEndian.Uint64(record.Value[offset : offset+8])
	offset += 8
	copy(meta.StateRoot[:], record.Value[offset:])
	if meta.Version == 0 || serverTimeMS == 0 || record.Seq != meta.Generation ||
		record.PathGeneration != meta.Generation {
		return "", nil, 0, dkvs.ErrInvalidSnapshot
	}
	return path, meta, serverTimeMS, nil
}

func encodePathSyncFloor(floor dkvs.DeleteFloor) (*wire.DKVSRecord, error) {
	if floor.Key == "" || floor.FloorSeq == 0 || floor.PathGeneration == 0 ||
		floor.EffectiveHash == (chainhash.Hash{}) {
		return nil, dkvs.ErrInvalidSnapshot
	}
	return &wire.DKVSRecord{
		Version:        pathSyncMetaV1,
		Key:            floor.Key,
		Value:          append([]byte(nil), floor.EffectiveHash[:]...),
		PubKey:         append([]byte(nil), floor.PubKey...),
		Seq:            floor.FloorSeq,
		PathGeneration: floor.PathGeneration,
		Flags:          pathSyncFloorFlag,
	}, nil
}

func decodePathSyncFloor(path string, record *wire.DKVSRecord) (dkvs.DeleteFloor, error) {
	if record == nil || record.Flags != pathSyncFloorFlag || record.Version != pathSyncMetaV1 ||
		record.Seq == 0 || record.PathGeneration == 0 || len(record.Value) != chainhash.HashSize ||
		!pathSyncMatchesKey(path, record.Key) {
		return dkvs.DeleteFloor{}, dkvs.ErrInvalidSnapshot
	}
	floor := dkvs.DeleteFloor{
		Key: record.Key, FloorSeq: record.Seq, PathGeneration: record.PathGeneration,
		PubKey: append([]byte(nil), record.PubKey...),
	}
	copy(floor.EffectiveHash[:], record.Value)
	if floor.EffectiveHash == (chainhash.Hash{}) {
		return dkvs.DeleteFloor{}, dkvs.ErrInvalidSnapshot
	}
	return floor, nil
}

func encodePathSyncCursor(offset uint64, root chainhash.Hash, generation uint64) []byte {
	cursor := make([]byte, pathSyncCursorSize)
	cursor[0] = pathSyncCursorV1
	binary.BigEndian.PutUint64(cursor[1:9], offset)
	copy(cursor[9:9+chainhash.HashSize], root[:])
	binary.BigEndian.PutUint64(cursor[9+chainhash.HashSize:], generation)
	return cursor
}

func decodePathSyncCursor(cursor []byte) (uint64, chainhash.Hash, uint64, error) {
	if len(cursor) != pathSyncCursorSize || cursor[0] != pathSyncCursorV1 {
		return 0, chainhash.Hash{}, 0, dkvs.ErrInvalidSnapshot
	}
	offset := binary.BigEndian.Uint64(cursor[1:9])
	var root chainhash.Hash
	copy(root[:], cursor[9:9+chainhash.HashSize])
	generation := binary.BigEndian.Uint64(cursor[9+chainhash.HashSize:])
	return offset, root, generation, nil
}

func pathSnapshotWireRecords(snapshot *dkvs.PathSnapshot) ([]*wire.DKVSRecord, error) {
	meta, err := encodePathSyncMeta(snapshot)
	if err != nil {
		return nil, err
	}
	records := make([]*wire.DKVSRecord, 0, 1+len(snapshot.Records)+len(snapshot.DeleteFloors))
	records = append(records, meta)
	for _, record := range snapshot.Records {
		if record == nil || record.Flags&(pathSyncMetaFlag|pathSyncFloorFlag) != 0 {
			return nil, dkvs.ErrInvalidSnapshot
		}
		records = append(records, record)
	}
	floors := append([]dkvs.DeleteFloor(nil), snapshot.DeleteFloors...)
	sort.Slice(floors, func(a, b int) bool { return floors[a].Key < floors[b].Key })
	for _, floor := range floors {
		record, err := encodePathSyncFloor(floor)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func pathSnapshotPage(snapshot *dkvs.PathSnapshot, cursor []byte, limit uint32) (
	[]*wire.DKVSRecord, []byte, bool, error) {
	if snapshot == nil || snapshot.PathMeta == nil {
		return nil, nil, false, dkvs.ErrInvalidSnapshot
	}
	offset := uint64(0)
	if len(cursor) != 0 {
		previousOffset, previousRoot, previousGeneration, err := decodePathSyncCursor(cursor)
		if err != nil {
			return nil, nil, false, err
		}
		if previousRoot == snapshot.PathMeta.StateRoot && previousGeneration == snapshot.PathMeta.Generation {
			offset = previousOffset
		}
	}
	all, err := pathSnapshotWireRecords(snapshot)
	if err != nil {
		return nil, nil, false, err
	}
	if offset > uint64(len(all)) {
		return nil, nil, false, dkvs.ErrInvalidSnapshot
	}
	maxRecords := int(limit)
	if maxRecords <= 0 || maxRecords > wire.MaxDKVSRecordsPerMsg {
		maxRecords = wire.MaxDKVSRecordsPerMsg
	}
	page := make([]*wire.DKVSRecord, 0, maxRecords)
	size := 64
	index := int(offset)
	for index < len(all) && len(page) < maxRecords {
		recordSize := wire.DKVSRecordSerializeSize(all[index])
		if len(page) > 0 && size+recordSize > wire.MaxDKVSRecordsPayloadSize {
			break
		}
		if recordSize > wire.MaxDKVSRecordsPayloadSize {
			return nil, nil, false, dkvs.ErrRecordTooLarge
		}
		page = append(page, all[index])
		size += recordSize
		index++
	}
	if len(page) == 0 && index < len(all) {
		return nil, nil, false, dkvs.ErrRecordTooLarge
	}
	done := index == len(all)
	if done {
		return page, nil, true, nil
	}
	return page, encodePathSyncCursor(uint64(index), snapshot.PathMeta.StateRoot,
		snapshot.PathMeta.Generation), false, nil
}

func decodePathSnapshot(path string, root chainhash.Hash, records, deletes []*wire.DKVSRecord) (*dkvs.PathSnapshot, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" {
		return nil, dkvs.ErrInvalidSnapshot
	}
	var meta *dkvs.PathMeta
	var serverTimeMS uint64
	snapshot := &dkvs.PathSnapshot{Path: path}
	combined := append(append([]*wire.DKVSRecord(nil), records...), deletes...)
	for _, record := range combined {
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
		case pathSyncFloorFlag:
			floor, err := decodePathSyncFloor(path, record)
			if err != nil {
				return nil, err
			}
			snapshot.DeleteFloors = append(snapshot.DeleteFloors, floor)
		default:
			if !pathSyncMatchesKey(path, record.Key) {
				return nil, dkvs.ErrInvalidSnapshot
			}
			snapshot.Records = append(snapshot.Records, record)
		}
	}
	if meta == nil || meta.StateRoot != root {
		return nil, dkvs.ErrPathDiverged
	}
	snapshot.PathMeta = meta
	snapshot.ServerTimeMS = serverTimeMS
	return snapshot, nil
}

func (s *PeerState) StartPathSync(path string, now time.Time) (SyncStart, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" {
		return SyncStart{}, dkvs.ErrInvalidKey
	}
	filters := []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: path}}
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	if s.syncActive && !syncSessionExpired(s.syncUpdated, now) {
		if activePath, ok := pathSyncFilter(s.syncFilters); ok && activePath == path {
			return SyncStart{}, nil
		}
		// A generation gap makes the local path unusable. Prioritize its
		// atomic repair over a lower-priority anti-entropy session.
		s.resetSyncLocked()
	}
	session, err := wire.RandomUint64()
	if err != nil || session == 0 {
		return SyncStart{}, err
	}
	s.syncSession = session
	s.syncCursor = nil
	s.syncFilters = filters
	s.syncMirror = true
	s.syncRecords = make(map[string]*wire.DKVSRecord)
	s.syncDeletes = make(map[string]*wire.DKVSRecord)
	s.syncBytes = 0
	s.syncRoot = chainhash.Hash{}
	s.syncRootSet = false
	s.syncActive = true
	s.syncUpdated = now
	return SyncStart{
		Request: &wire.MsgDKVSSyncRequest{
			SessionID: session, Limit: wire.MaxDKVSRecordsPerMsg, Filters: filters,
		},
		Started: true,
		Mirror:  true,
	}, nil
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

func (h Handler) QueuePathSync(path string) {
	if !h.valid() || !h.TrustedSource {
		return
	}
	start, err := h.Peer.StartPathSync(path, time.Now())
	if err != nil || start.Request == nil {
		return
	}
	if start.Started {
		h.Node.SetReady(false)
	}
	h.send(start.Request)
}

func (h Handler) queuePathRepair(record *wire.DKVSRecord, err error) bool {
	if record == nil || err == nil {
		return false
	}
	if !errors.Is(err, dkvs.ErrPathGenerationGap) &&
		!errors.Is(err, dkvs.ErrStaleEndpoint) &&
		!errors.Is(err, dkvs.ErrPathDiverged) {
		return false
	}
	path, pathErr := dkvs.CollectionPathForKey(record.Key)
	if pathErr != nil {
		return false
	}
	h.QueuePathSync(path)
	return true
}

func (h Handler) servePathSync(msg *wire.MsgDKVSSyncRequest) bool {
	path, ok := pathSyncFilter(msg.Filters)
	if !ok {
		return false
	}
	pathStore, ok := h.Store.(PathSnapshotStore)
	if !ok {
		h.Peer.CancelServe()
		h.debugf("DKVS path snapshot backend is unavailable")
		return true
	}
	snapshot, err := pathStore.GetDKVSPathSnapshot(path)
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
	response := &wire.MsgDKVSSyncResponse{
		SessionID: msg.SessionID, Records: records, NextCursor: next,
		Done: done, CheckpointRoot: snapshot.PathMeta.StateRoot,
	}
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
		for _, pending := range h.Peer.FinishServe(msg.SessionID) {
			h.send(pending)
		}
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
