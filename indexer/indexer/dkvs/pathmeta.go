package dkvs

import (
	"encoding/binary"
	"errors"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const pathMetaVersion = uint32(2)

var pathMetaKeyPrefix = []byte("dkvs:pathmeta:")

func collectionPath(parsed ParsedKey) string {
	if len(parsed.Segments) == 0 {
		return ""
	}
	switch parsed.Namespace {
	case "personal":
		return "/personal/" + parsed.Segments[0]
	case "svc":
		return "/svc/" + parsed.Segments[0]
	case "mail":
		if len(parsed.Segments) >= 3 && parsed.Segments[1] == "msg" {
			return "/mail/" + parsed.Segments[0] + "/msg/" + parsed.Segments[2]
		}
		if len(parsed.Segments) >= 2 && parsed.Segments[1] == "share" {
			return "/mail/" + parsed.Segments[0] + "/share"
		}
	case "blob":
		if len(parsed.Segments) >= 2 {
			return "/blob/" + parsed.Segments[0] + "/" + parsed.Segments[1]
		}
	}
	return ""
}

func collectionPathForPrefix(prefix string) string {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	parsed, err := ParsePrefix(prefix)
	if err != nil {
		return ""
	}
	switch parsed.Namespace {
	case "personal", "svc":
		if len(parsed.Segments) == 1 {
			return prefix
		}
	case "mail":
		if len(parsed.Segments) == 3 && parsed.Segments[1] == "msg" && validAccountID(parsed.Segments[2]) {
			return prefix
		}
		if len(parsed.Segments) == 2 && parsed.Segments[1] == "share" {
			return prefix
		}
	case "blob":
		if len(parsed.Segments) == 2 {
			return prefix
		}
	}
	return ""
}

// CollectionPathForKey returns the logical collection whose root and
// generation cover key.
func CollectionPathForKey(key string) (string, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return "", err
	}
	path := collectionPath(parsed)
	if path == "" {
		return "", ErrInvalidKey
	}
	return path, nil
}

func pathMetaDBKey(path string) []byte {
	out := make([]byte, 0, len(pathMetaKeyPrefix)+len(path))
	out = append(out, pathMetaKeyPrefix...)
	out = append(out, path...)
	return out
}

func marshalPathMeta(meta *PathMeta) ([]byte, error) {
	if meta == nil || meta.Version != pathMetaVersion || meta.Path == "" {
		return nil, ErrInvalidRecord
	}
	encoded := make([]byte, 4+7*8+chainhash.HashSize+1)
	binary.LittleEndian.PutUint32(encoded[0:4], meta.Version)
	values := []uint64{
		meta.Generation,
		meta.ActiveRecords,
		meta.ActiveTotalSize,
		meta.MinExpiryHeight,
		meta.MinExpiryTime,
		meta.UpdatedHeight,
		meta.UpdatedAt,
	}
	offset := 4
	for _, value := range values {
		binary.LittleEndian.PutUint64(encoded[offset:offset+8], value)
		offset += 8
	}
	copy(encoded[offset:offset+chainhash.HashSize], meta.ActiveRoot[:])
	offset += chainhash.HashSize
	if meta.Dirty {
		encoded[offset] = 1
	}
	return encoded, nil
}

func unmarshalPathMeta(path string, encoded []byte) (*PathMeta, error) {
	if path == "" || len(encoded) != 4+7*8+chainhash.HashSize+1 {
		return nil, ErrInvalidRecord
	}
	meta := &PathMeta{Path: path}
	meta.Version = binary.LittleEndian.Uint32(encoded[0:4])
	if meta.Version != pathMetaVersion {
		return nil, ErrInvalidRecord
	}
	fields := []*uint64{
		&meta.Generation,
		&meta.ActiveRecords,
		&meta.ActiveTotalSize,
		&meta.MinExpiryHeight,
		&meta.MinExpiryTime,
		&meta.UpdatedHeight,
		&meta.UpdatedAt,
	}
	offset := 4
	for _, field := range fields {
		*field = binary.LittleEndian.Uint64(encoded[offset : offset+8])
		offset += 8
	}
	copy(meta.ActiveRoot[:], encoded[offset:offset+chainhash.HashSize])
	offset += chainhash.HashSize
	meta.Dirty = encoded[offset] != 0
	return meta, nil
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

func pathMetaNeedsRebuild(meta *PathMeta, height, now uint64) bool {
	if meta == nil || meta.Dirty {
		return true
	}
	if height != 0 && meta.MinExpiryHeight != 0 && meta.MinExpiryHeight <= height {
		return true
	}
	return now != 0 && meta.MinExpiryTime != 0 && meta.MinExpiryTime <= now
}

func recordExpiryTime(record *wire.DKVSRecord) uint64 {
	if record == nil || record.TTL == 0 || record.IssueTime == 0 || record.IssueTime > ^uint64(0)-record.TTL {
		return 0
	}
	return record.IssueTime + record.TTL
}

func updateMinExpiry(meta *PathMeta, record *wire.DKVSRecord) {
	if meta == nil || record == nil || IsTombstone(record.Flags) {
		return
	}
	if record.ExpiryHeight != 0 && (meta.MinExpiryHeight == 0 || record.ExpiryHeight < meta.MinExpiryHeight) {
		meta.MinExpiryHeight = record.ExpiryHeight
	}
	if expiryTime := recordExpiryTime(record); expiryTime != 0 && (meta.MinExpiryTime == 0 || expiryTime < meta.MinExpiryTime) {
		meta.MinExpiryTime = expiryTime
	}
}

func pathMetaRecordLeaf(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	recordHash := RecordHash(record)
	payload := make([]byte, 0, len(record.Key)+chainhash.HashSize)
	payload = append(payload, record.Key...)
	payload = append(payload, recordHash[:]...)
	return chainhash.DoubleHashH(payload)
}

func xorPathMetaRoot(root *chainhash.Hash, record *wire.DKVSRecord) {
	if root == nil || record == nil {
		return
	}
	leaf := pathMetaRecordLeaf(record)
	for n := range root {
		root[n] ^= leaf[n]
	}
}

func (i *Indexer) rebuildPathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	records, _, _, err := i.scanLocked(path, nil, 0, true, height, now)
	if err != nil {
		return nil, err
	}
	meta := &PathMeta{
		Version:       pathMetaVersion,
		Path:          path,
		UpdatedHeight: height,
		UpdatedAt:     now,
	}
	if previous, err := i.readPathMetaLocked(path); err == nil {
		meta.Generation = previous.Generation
	} else if !errors.Is(err, ErrRecordNotFound) {
		return nil, err
	}
	for _, record := range records {
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(RecordSize(record))
		xorPathMetaRoot(&meta.ActiveRoot, record)
		updateMinExpiry(meta, record)
	}
	encoded, err := marshalPathMeta(meta)
	if err != nil {
		return nil, err
	}
	if err := i.db.Write(pathMetaDBKey(path), encoded); err != nil {
		return nil, err
	}
	return meta, nil
}

func (i *Indexer) ensurePathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	if path == "" {
		return nil, ErrInvalidKey
	}
	meta, err := i.readPathMetaLocked(path)
	if errors.Is(err, ErrRecordNotFound) {
		return i.rebuildPathMetaLocked(path, height, now)
	}
	if err != nil {
		return nil, err
	}
	if pathMetaNeedsRebuild(meta, height, now) {
		return i.rebuildPathMetaLocked(path, height, now)
	}
	return meta, nil
}

func clonePathMeta(meta *PathMeta) *PathMeta {
	if meta == nil {
		return nil
	}
	copyMeta := *meta
	return &copyMeta
}

// GetPathMeta returns the aggregate for a supported logical collection path.
func (i *Indexer) GetPathMeta(path string) (*PathMeta, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if collectionPathForPrefix(path) == "" {
		return nil, ErrInvalidKey
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return nil, err
	}
	return clonePathMeta(meta), nil
}

func existingRecordActive(i *Indexer, record *wire.DKVSRecord, height, now uint64) bool {
	return record != nil && !IsTombstone(record.Flags) && i.activeError(record, height, now) == nil
}

func (i *Indexer) pathMetaForMutationLocked(parsed ParsedKey, existing, next *wire.DKVSRecord, height, now uint64) (*PathMeta, error) {
	path := collectionPath(parsed)
	if path == "" {
		return nil, nil
	}
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return nil, err
	}
	meta = clonePathMeta(meta)
	oldActive := existingRecordActive(i, existing, height, now)
	newActive := next != nil && !IsTombstone(next.Flags) && !IsExpired(next, height, now)
	if oldActive {
		xorPathMetaRoot(&meta.ActiveRoot, existing)
		oldSize := uint64(RecordSize(existing))
		if meta.ActiveRecords > 0 {
			meta.ActiveRecords--
		}
		if meta.ActiveTotalSize >= oldSize {
			meta.ActiveTotalSize -= oldSize
		} else {
			meta.ActiveTotalSize = 0
			meta.Dirty = true
		}
		if existing.ExpiryHeight != 0 && existing.ExpiryHeight == meta.MinExpiryHeight {
			meta.Dirty = true
		}
		if expiryTime := recordExpiryTime(existing); expiryTime != 0 && expiryTime == meta.MinExpiryTime {
			meta.Dirty = true
		}
	}
	if newActive {
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(RecordSize(next))
		xorPathMetaRoot(&meta.ActiveRoot, next)
		updateMinExpiry(meta, next)
	}
	meta.Generation++
	meta.UpdatedHeight = height
	meta.UpdatedAt = now
	return meta, nil
}

func putPathMetaBatch(batch indexercommon.WriteBatch, meta *PathMeta) error {
	if meta == nil {
		return nil
	}
	encoded, err := marshalPathMeta(meta)
	if err != nil {
		return err
	}
	return batch.Put(pathMetaDBKey(meta.Path), encoded)
}

func (i *Indexer) markPathMetaDirtyLocked(batch indexercommon.WriteBatch, records []*wire.DKVSRecord, height, now uint64) error {
	metas := make(map[string]*PathMeta)
	for _, record := range records {
		if record == nil {
			continue
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			continue
		}
		path := collectionPath(parsed)
		if path == "" {
			continue
		}
		meta := metas[path]
		if meta == nil {
			meta, err = i.ensurePathMetaLocked(path, height, now)
			if err != nil {
				return err
			}
			meta = clonePathMeta(meta)
			metas[path] = meta
		}
		meta.Dirty = true
	}
	for _, meta := range metas {
		meta.Generation++
		meta.UpdatedHeight = height
		meta.UpdatedAt = now
		if err := putPathMetaBatch(batch, meta); err != nil {
			return err
		}
	}
	return nil
}
