package dkvs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const pathMetaVersion = uint32(4)

var (
	pathMetaKeyPrefix   = []byte("dkvs:pathmeta:")
	pathStatusKeyPrefix = []byte("dkvs:pathstatus:")
	pathLeafDomain      = []byte("dkvs-path-leaf-v1")
	deleteFloorDomain   = []byte("dkvs-delete-floor-v1")
)

func pathMetaDBKey(path string) []byte {
	out := make([]byte, 0, len(pathMetaKeyPrefix)+len(path))
	out = append(out, pathMetaKeyPrefix...)
	return append(out, path...)
}

func pathStatusDBKey(path string) []byte {
	out := make([]byte, 0, len(pathStatusKeyPrefix)+len(path))
	out = append(out, pathStatusKeyPrefix...)
	return append(out, path...)
}

func normalizePathMetaAliases(meta *PathMeta) {
	if meta == nil {
		return
	}
	// ActiveRoot is a deprecated in-memory compatibility view containing only
	// active records. StateRoot is the canonical network root and additionally
	// commits delete floors. ActiveRoot is therefore populated only when a
	// caller explicitly asks for path metadata and must never alias StateRoot.
	meta.UpdatedHeight = meta.ViewHeight
}

func marshalPathMeta(meta *PathMeta) ([]byte, error) {
	if meta == nil || meta.Version != pathMetaVersion || meta.Path == "" {
		return nil, ErrInvalidRecord
	}
	encoded := make([]byte, 4+6*8+chainhash.HashSize)
	binary.LittleEndian.PutUint32(encoded[0:4], meta.Version)
	values := []uint64{meta.Generation, meta.EndpointGeneration, meta.ActiveRecords, meta.ActiveTotalSize, meta.MinExpiryHeight, meta.ViewHeight}
	offset := 4
	for _, value := range values {
		binary.LittleEndian.PutUint64(encoded[offset:offset+8], value)
		offset += 8
	}
	copy(encoded[offset:], meta.StateRoot[:])
	return encoded, nil
}

func unmarshalPathMeta(path string, encoded []byte) (*PathMeta, error) {
	if path == "" || len(encoded) != 4+6*8+chainhash.HashSize {
		return nil, ErrInvalidRecord
	}
	meta := &PathMeta{Path: path, Version: binary.LittleEndian.Uint32(encoded[0:4])}
	if meta.Version != pathMetaVersion {
		return nil, ErrInvalidRecord
	}
	fields := []*uint64{&meta.Generation, &meta.EndpointGeneration, &meta.ActiveRecords, &meta.ActiveTotalSize, &meta.MinExpiryHeight, &meta.ViewHeight}
	offset := 4
	for _, field := range fields {
		*field = binary.LittleEndian.Uint64(encoded[offset : offset+8])
		offset += 8
	}
	copy(meta.StateRoot[:], encoded[offset:])
	normalizePathMetaAliases(meta)
	return meta, nil
}

func marshalPathStatus(status *PathLocalStatus) ([]byte, error) {
	if status == nil || status.Path == "" {
		return nil, ErrInvalidRecord
	}
	return json.Marshal(status)
}

func unmarshalPathStatus(path string, encoded []byte) (*PathLocalStatus, error) {
	if path == "" || len(encoded) == 0 {
		return nil, ErrInvalidRecord
	}
	var status PathLocalStatus
	if err := json.Unmarshal(encoded, &status); err != nil {
		return nil, ErrInvalidRecord
	}
	if status.Path == "" {
		status.Path = path
	}
	if status.Path != path {
		return nil, ErrInvalidRecord
	}
	return &status, nil
}

func (i *Indexer) readPathMetaLocked(path string) (*PathMeta, error) {
	encoded, err := i.db.Read(pathMetaDBKey(path))
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return unmarshalPathMeta(path, encoded)
}

func (i *Indexer) readPathStatusLocked(path string) (*PathLocalStatus, error) {
	encoded, err := i.db.Read(pathStatusDBKey(path))
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return &PathLocalStatus{Path: path}, nil
		}
		return nil, err
	}
	return unmarshalPathStatus(path, encoded)
}

func pathMetaNeedsRebuild(meta *PathMeta, status *PathLocalStatus, height uint64) bool {
	if meta == nil || (status != nil && status.Dirty) || meta.ViewHeight != height {
		return true
	}
	return height != 0 && meta.MinExpiryHeight != 0 && meta.MinExpiryHeight <= height
}

func updateMinExpiry(meta *PathMeta, record *wire.DKVSRecord) {
	if meta == nil || record == nil || IsTombstone(record.Flags) || isFreeLocalRecord(record) {
		return
	}
	expiry := RecordExpiryHeight(record)
	if expiry != 0 && (meta.MinExpiryHeight == 0 || expiry < meta.MinExpiryHeight) {
		meta.MinExpiryHeight = expiry
	}
}

func pathStateLeaf(key string, effectiveHash chainhash.Hash) chainhash.Hash {
	h := sha256.New()
	_, _ = h.Write(pathLeafDomain)
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], uint64(len(key)))
	_, _ = h.Write(scratch[:])
	_, _ = h.Write([]byte(key))
	_, _ = h.Write(effectiveHash[:])
	var out chainhash.Hash
	copy(out[:], h.Sum(nil))
	return out
}

func deleteFloorEffectiveHash(key string, floorSeq, pathGeneration uint64, pubKey []byte) chainhash.Hash {
	h := sha256.New()
	_, _ = h.Write(deleteFloorDomain)
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], uint64(len(key)))
	_, _ = h.Write(scratch[:])
	_, _ = h.Write([]byte(key))
	binary.BigEndian.PutUint64(scratch[:], floorSeq)
	_, _ = h.Write(scratch[:])
	binary.BigEndian.PutUint64(scratch[:], pathGeneration)
	_, _ = h.Write(scratch[:])
	binary.BigEndian.PutUint64(scratch[:], uint64(len(pubKey)))
	_, _ = h.Write(scratch[:])
	_, _ = h.Write(pubKey)
	var out chainhash.Hash
	copy(out[:], h.Sum(nil))
	return out
}

func pathMetaRecordLeaf(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	return pathStateLeaf(record.Key, RecordHash(record))
}

func xorHash(root *chainhash.Hash, leaf chainhash.Hash) {
	if root == nil {
		return
	}
	for n := range root {
		root[n] ^= leaf[n]
	}
}

func xorPathMetaRoot(root *chainhash.Hash, record *wire.DKVSRecord) {
	if root != nil && record != nil {
		xorHash(root, pathMetaRecordLeaf(record))
	}
}

func xorDeleteFloorRoot(root *chainhash.Hash, key string, state *deleteState) {
	if root != nil && state != nil {
		xorHash(root, pathStateLeaf(key, state.effectiveHash(key)))
	}
}

func pathIncludesCollection(path, candidate string) bool {
	return candidate == path || (!isCanonicalCollectionPath(path) && strings.HasPrefix(candidate, path+"/"))
}

func (i *Indexer) scanPathDeleteStatesLocked(path string) (map[string]*deleteState, error) {
	states := make(map[string]*deleteState)
	prefix := deleteDBKey(path)
	err := i.db.BatchReadV2(prefix, prefix, false, func(key, value []byte) error {
		if len(key) < len(deleteKeyPrefix) {
			return ErrInvalidRecord
		}
		recordKey := string(key[len(deleteKeyPrefix):])
		parsed, err := ParseKey(recordKey)
		if err != nil {
			return err
		}
		if !pathIncludesCollection(path, collectionPath(parsed)) {
			return nil
		}
		state, err := unmarshalDeleteState(value)
		if err != nil {
			return err
		}
		if !state.LocalOnly {
			states[recordKey] = state
		}
		return nil
	})
	return states, err
}

func (i *Indexer) scanEndpointDeleteStatesLocked(path string) (map[string]*deleteState, error) {
	states := make(map[string]*deleteState)
	prefix := deleteDBKey(path)
	err := i.db.BatchReadV2(prefix, prefix, false, func(key, value []byte) error {
		if len(key) < len(deleteKeyPrefix) {
			return ErrInvalidRecord
		}
		recordKey := string(key[len(deleteKeyPrefix):])
		parsed, err := ParseKey(recordKey)
		if err != nil {
			return err
		}
		if !pathIncludesCollection(path, collectionPath(parsed)) {
			return nil
		}
		state, err := unmarshalDeleteState(value)
		if err != nil {
			return err
		}
		if state.LocalOnly {
			states[recordKey] = state
		}
		return nil
	})
	return states, err
}

func (i *Indexer) rebuildPathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	records, _, _, err := i.scanLocked(path, nil, 0, true, height, now)
	if err != nil {
		return nil, err
	}
	meta := &PathMeta{Version: pathMetaVersion, Path: path, ViewHeight: height}
	if previous, readErr := i.readPathMetaLocked(path); readErr == nil {
		meta.Generation = previous.Generation
		meta.EndpointGeneration = previous.EndpointGeneration
	} else if !errors.Is(readErr, ErrRecordNotFound) {
		return nil, readErr
	}
	for _, record := range records {
		if !i.networkPathRecordVisible(record) {
			continue
		}
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(RecordSize(record))
		xorPathMetaRoot(&meta.StateRoot, record)
		xorPathMetaRoot(&meta.ActiveRoot, record)
		updateMinExpiry(meta, record)
	}
	deleteStates, err := i.scanPathDeleteStatesLocked(path)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(deleteStates))
	for key := range deleteStates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state := deleteStates[key]
		xorDeleteFloorRoot(&meta.StateRoot, key, state)
		if state.PathGeneration > meta.Generation {
			meta.Generation = state.PathGeneration
		}
	}
	normalizePathMetaAliases(meta)
	encoded, err := marshalPathMeta(meta)
	if err != nil {
		return nil, err
	}
	if err := i.db.Write(pathMetaDBKey(path), encoded); err != nil {
		return nil, err
	}
	statusBytes, err := marshalPathStatus(&PathLocalStatus{Path: path, UpdatedAt: now})
	if err != nil {
		return nil, err
	}
	if err := i.db.Write(pathStatusDBKey(path), statusBytes); err != nil {
		return nil, err
	}
	return meta, nil
}

func (i *Indexer) ensurePathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	if path == "" || !isCanonicalCollectionPath(path) {
		return nil, ErrInvalidKey
	}
	meta, err := i.readPathMetaLocked(path)
	if errors.Is(err, ErrRecordNotFound) {
		return i.rebuildPathMetaLocked(path, height, now)
	}
	if err != nil {
		return nil, err
	}
	status, err := i.readPathStatusLocked(path)
	if err != nil {
		return nil, err
	}
	if pathMetaNeedsRebuild(meta, status, height) {
		return i.rebuildPathMetaLocked(path, height, now)
	}
	return meta, nil
}

func clonePathMeta(meta *PathMeta) *PathMeta {
	if meta == nil {
		return nil
	}
	copyMeta := *meta
	normalizePathMetaAliases(&copyMeta)
	return &copyMeta
}

func (i *Indexer) activePathRootLocked(path string, height, now uint64) (chainhash.Hash, error) {
	records, _, _, err := i.scanLocked(path, nil, 0, true, height, now)
	if err != nil {
		return chainhash.Hash{}, err
	}
	var root chainhash.Hash
	for _, record := range records {
		if !i.networkPathRecordVisible(record) {
			continue
		}
		xorPathMetaRoot(&root, record)
	}
	return root, nil
}

func (i *Indexer) GetPathMeta(path string) (*PathMeta, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if collectionPathForPrefix(path) == "" {
		return nil, ErrInvalidKey
	}
	parsed, err := ParsePrefix(path)
	if err != nil {
		return nil, err
	}
	if pathMode(parsed) == PathLocalOnly {
		return nil, ErrFreeLocalNotRelayable
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	var meta *PathMeta
	if isCanonicalCollectionPath(path) {
		meta, err = i.ensurePathMetaLocked(path, height, now)
	} else {
		meta, err = i.rebuildPathMetaLocked(path, height, now)
	}
	if err != nil {
		return nil, err
	}
	result := clonePathMeta(meta)
	result.ActiveRoot, err = i.activePathRootLocked(path, height, now)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (i *Indexer) GetPathLocalStatus(path string) (*PathLocalStatus, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if collectionPathForPrefix(path) == "" {
		return nil, ErrInvalidKey
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	status, err := i.readPathStatusLocked(path)
	if err != nil {
		return nil, err
	}
	copyStatus := *status
	return &copyStatus, nil
}

func existingRecordActive(i *Indexer, record *wire.DKVSRecord, height, now uint64) bool {
	return record != nil && !IsTombstone(record.Flags) && i.activeError(record, height, now) == nil
}

func mutationPathGeneration(meta *PathMeta) uint64 {
	if meta == nil || meta.Generation == ^uint64(0) {
		return 0
	}
	return meta.Generation + 1
}

func (i *Indexer) pathMetaForMutationLocked(parsed ParsedKey, existing, next *wire.DKVSRecord, nextVisible bool, height, now uint64) (*PathMeta, error) {
	path := collectionPath(parsed)
	if path == "" || pathMode(parsed) == PathLocalOnly {
		return nil, nil
	}
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return nil, err
	}
	meta = clonePathMeta(meta)
	if meta.EndpointGeneration == ^uint64(0) {
		return nil, ErrStaleGeneration
	}
	meta.EndpointGeneration++
	if existingRecordActive(i, existing, height, now) && i.networkPathRecordVisible(existing) {
		xorPathMetaRoot(&meta.StateRoot, existing)
		oldSize := uint64(RecordSize(existing))
		if meta.ActiveRecords > 0 {
			meta.ActiveRecords--
		}
		if meta.ActiveTotalSize >= oldSize {
			meta.ActiveTotalSize -= oldSize
		} else {
			meta.ActiveTotalSize = 0
		}
		if expiry := RecordExpiryHeight(existing); expiry != 0 && expiry == meta.MinExpiryHeight {
			meta.MinExpiryHeight = 0
		}
	}
	key := parsedKeyString(parsed)
	if state, stateErr := i.getDeleteStateLocked(key); stateErr == nil && !state.LocalOnly {
		xorDeleteFloorRoot(&meta.StateRoot, key, state)
	}
	if next != nil && !isFreeLocalRecord(next) && nextVisible {
		if IsTombstone(next.Flags) {
			state := &deleteState{
				FloorSeq: next.Seq, PathGeneration: mutationPathGeneration(meta),
				PubKey: append([]byte{}, next.PubKey...), Record: next, EffectiveHash: RecordHash(next),
			}
			xorDeleteFloorRoot(&meta.StateRoot, next.Key, state)
		} else if !IsExpired(next, height) {
			meta.ActiveRecords++
			meta.ActiveTotalSize += uint64(RecordSize(next))
			xorPathMetaRoot(&meta.StateRoot, next)
			updateMinExpiry(meta, next)
		}
		if generation := mutationPathGeneration(meta); generation > meta.Generation {
			meta.Generation = generation
		}
	}
	if next != nil && !isFreeLocalRecord(next) && nextVisible {
		meta.ViewHeight = height
	}
	normalizePathMetaAliases(meta)
	return meta, nil
}

func parsedKeyString(parsed ParsedKey) string {
	if parsed.Namespace == "" {
		return ""
	}
	return "/" + parsed.Namespace + "/" + strings.Join(parsed.Segments, "/")
}

func putPathMetaBatch(batch indexercommon.WriteBatch, meta *PathMeta) error {
	if meta == nil {
		return nil
	}
	normalizePathMetaAliases(meta)
	encoded, err := marshalPathMeta(meta)
	if err != nil {
		return err
	}
	return batch.Put(pathMetaDBKey(meta.Path), encoded)
}

func putPathStatusBatch(batch indexercommon.WriteBatch, status *PathLocalStatus) error {
	if status == nil {
		return nil
	}
	encoded, err := marshalPathStatus(status)
	if err != nil {
		return err
	}
	return batch.Put(pathStatusDBKey(status.Path), encoded)
}

func (i *Indexer) markPathMetaDirtyLocked(batch indexercommon.WriteBatch, records []*wire.DKVSRecord, height uint64, now uint64) error {
	paths := make(map[string]struct{})
	for _, record := range records {
		if record == nil {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			continue
		}
		if path := collectionPath(parsed); path != "" && pathMode(parsed) != PathLocalOnly {
			paths[path] = struct{}{}
		}
	}
	for path := range paths {
		meta, err := i.readPathMetaLocked(path)
		if errors.Is(err, ErrRecordNotFound) {
			meta = &PathMeta{Version: pathMetaVersion, Path: path, ViewHeight: height}
		} else if err != nil {
			return err
		}
		if meta.EndpointGeneration == ^uint64(0) {
			return ErrStaleGeneration
		}
		meta.EndpointGeneration++
		if err := putPathMetaBatch(batch, meta); err != nil {
			return err
		}
		status, err := i.readPathStatusLocked(path)
		if err != nil {
			return err
		}
		status.Dirty = true
		status.UpdatedAt = now
		if err := putPathStatusBatch(batch, status); err != nil {
			return err
		}
	}
	return nil
}
