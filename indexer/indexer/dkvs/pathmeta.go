package dkvs

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const pathMetaVersion = uint32(1)

var pathMetaKeyPrefix = []byte("dkvs:pathmeta:")

// PathMeta summarizes one business collection path. It is internal indexer
// metadata and is not a signed DKVS record.
type PathMeta struct {
	Version          uint32 `json:"version"`
	Path             string `json:"path"`
	Generation       uint64 `json:"generation"`
	ActiveCount      uint64 `json:"active_count"`
	ActiveBytes      uint64 `json:"active_bytes"`
	UpdatedHeight    uint64 `json:"updated_height"`
	UpdatedAt        uint64 `json:"updated_at"`
	NextExpiryHeight uint64 `json:"next_expiry_height,omitempty"`
	NextExpiryTime   uint64 `json:"next_expiry_time,omitempty"`
	ExpiryDirty      bool   `json:"expiry_dirty,omitempty"`
}

func normalizeCollectionPath(path string) (string, bool) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	parsed, err := parseKeyParts(path)
	if err != nil {
		return "", false
	}

	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) == 1 && validAccountID(parsed.Segments[0]) {
			return path, true
		}
	case "svc":
		if len(parsed.Segments) == 1 {
			return path, true
		}
	case "mail":
		if len(parsed.Segments) == 2 &&
			(parsed.Segments[1] == "msg" || parsed.Segments[1] == "share") {
			return path, true
		}
	case "blob":
		if len(parsed.Segments) == 2 && validAccountID(parsed.Segments[0]) {
			return path, true
		}
	}
	return "", false
}

func collectionPathForParsed(parsed ParsedKey) (string, bool) {
	if len(parsed.Segments) == 0 {
		return "", false
	}
	switch parsed.Namespace {
	case "personal":
		if validAccountID(parsed.Segments[0]) {
			return "/personal/" + parsed.Segments[0], true
		}
	case "svc":
		return "/svc/" + parsed.Segments[0], true
	case "mail":
		if len(parsed.Segments) >= 2 &&
			(parsed.Segments[1] == "msg" || parsed.Segments[1] == "share") {
			return "/mail/" + parsed.Segments[0] + "/" + parsed.Segments[1], true
		}
	case "blob":
		if len(parsed.Segments) >= 2 && validAccountID(parsed.Segments[0]) {
			return "/blob/" + parsed.Segments[0] + "/" + parsed.Segments[1], true
		}
	}
	return "", false
}

func pathMetaDBKey(path string) []byte {
	out := make([]byte, 0, len(pathMetaKeyPrefix)+len(path))
	out = append(out, pathMetaKeyPrefix...)
	out = append(out, path...)
	return out
}

func clonePathMeta(meta *PathMeta) *PathMeta {
	if meta == nil {
		return nil
	}
	copyMeta := *meta
	return &copyMeta
}

func encodePathMeta(meta *PathMeta) ([]byte, error) {
	if meta == nil || meta.Version != pathMetaVersion || meta.Path == "" {
		return nil, ErrInvalidRecord
	}
	return json.Marshal(meta)
}

func decodePathMeta(data []byte) (*PathMeta, error) {
	var meta PathMeta
	if len(data) == 0 || json.Unmarshal(data, &meta) != nil ||
		meta.Version != pathMetaVersion || meta.Path == "" {
		return nil, ErrInvalidRecord
	}
	return &meta, nil
}

func recordExpiryTime(record *wire.DKVSRecord) uint64 {
	if record == nil || record.TTL == 0 || record.IssueTime == 0 ||
		record.IssueTime > math.MaxUint64-record.TTL {
		return 0
	}
	return record.IssueTime + record.TTL
}

func recordActiveForMeta(record *wire.DKVSRecord, height, now uint64) bool {
	return record != nil && !IsTombstone(record.Flags) && !IsExpired(record, height, now)
}

func pathMetaNeedsRefresh(meta *PathMeta, height, now uint64) bool {
	if meta == nil || meta.ExpiryDirty {
		return true
	}
	if meta.NextExpiryHeight != 0 && height != 0 && meta.NextExpiryHeight <= height {
		return true
	}
	return meta.NextExpiryTime != 0 && now != 0 && meta.NextExpiryTime <= now
}

func (i *Indexer) rebuildPathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	meta := &PathMeta{
		Version:       pathMetaVersion,
		Path:          path,
		UpdatedHeight: height,
		UpdatedAt:     now,
	}
	records, _, _, err := i.scanLocked(path, nil, 0, false, height, now)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if !recordActiveForMeta(record, height, now) {
			continue
		}
		meta.ActiveCount++
		meta.ActiveBytes += uint64(RecordSize(record))
		pathMetaIncludeExpiry(meta, record)
	}
	return meta, nil
}

func (i *Indexer) ensurePathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	data, err := i.db.Read(pathMetaDBKey(path))
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return i.rebuildPathMetaLocked(path, height, now)
	}
	if err != nil {
		return nil, err
	}
	meta, err := decodePathMeta(data)
	if err != nil || meta.Path != path || pathMetaNeedsRefresh(meta, height, now) {
		return i.rebuildPathMetaLocked(path, height, now)
	}
	return meta, nil
}

func pathMetaIncludeExpiry(meta *PathMeta, record *wire.DKVSRecord) {
	if meta == nil || record == nil {
		return
	}
	if record.ExpiryHeight != 0 &&
		(meta.NextExpiryHeight == 0 || record.ExpiryHeight < meta.NextExpiryHeight) {
		meta.NextExpiryHeight = record.ExpiryHeight
	}
	if expiryTime := recordExpiryTime(record); expiryTime != 0 &&
		(meta.NextExpiryTime == 0 || expiryTime < meta.NextExpiryTime) {
		meta.NextExpiryTime = expiryTime
	}
}

func pathMetaRemoveRecord(meta *PathMeta, record *wire.DKVSRecord, height, now uint64) {
	if meta == nil || !recordActiveForMeta(record, height, now) {
		return
	}
	if meta.ActiveCount > 0 {
		meta.ActiveCount--
	}
	size := uint64(RecordSize(record))
	if size >= meta.ActiveBytes {
		meta.ActiveBytes = 0
	} else {
		meta.ActiveBytes -= size
	}
	if record.ExpiryHeight != 0 && record.ExpiryHeight == meta.NextExpiryHeight {
		meta.ExpiryDirty = true
	}
	if expiryTime := recordExpiryTime(record); expiryTime != 0 && expiryTime == meta.NextExpiryTime {
		meta.ExpiryDirty = true
	}
}

func pathMetaAddRecord(meta *PathMeta, record *wire.DKVSRecord, height, now uint64) {
	if meta == nil || !recordActiveForMeta(record, height, now) {
		return
	}
	meta.ActiveCount++
	meta.ActiveBytes += uint64(RecordSize(record))
	pathMetaIncludeExpiry(meta, record)
}

func pathMetaTouch(meta *PathMeta, height, now uint64) {
	meta.Generation++
	meta.UpdatedHeight = height
	meta.UpdatedAt = now
}

func (i *Indexer) applyPathMetaMutationLocked(batch indexercommon.WriteBatch,
	parsed ParsedKey, existing, next *wire.DKVSRecord, height, now uint64) error {

	path, ok := collectionPathForParsed(parsed)
	if !ok {
		return nil
	}
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return err
	}
	pathMetaRemoveRecord(meta, existing, height, now)
	pathMetaAddRecord(meta, next, height, now)
	pathMetaTouch(meta, height, now)
	encoded, err := encodePathMeta(meta)
	if err != nil {
		return err
	}
	return batch.Put(pathMetaDBKey(path), encoded)
}

// GetPathMeta returns an exact collection summary. The metadata is lazily
// rebuilt if a record expiry boundary has passed.
func (i *Indexer) GetPathMeta(path string) (*PathMeta, error) {
	normalized, ok := normalizeCollectionPath(path)
	if !ok {
		return nil, ErrInvalidKey
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	meta, err := i.ensurePathMetaLocked(normalized, height, now)
	if err != nil {
		return nil, err
	}
	encoded, err := encodePathMeta(meta)
	if err != nil {
		return nil, err
	}
	if err := i.db.Write(pathMetaDBKey(normalized), encoded); err != nil {
		return nil, err
	}
	return clonePathMeta(meta), nil
}

func (i *Indexer) listPrefixWithMetaLocked(prefix string, start, limit int,
	height, now uint64) ([]*wire.DKVSRecord, int, error) {

	meta, err := i.ensurePathMetaLocked(prefix, height, now)
	if err != nil {
		return nil, 0, err
	}
	encoded, err := encodePathMeta(meta)
	if err != nil {
		return nil, 0, err
	}
	if err := i.db.Write(pathMetaDBKey(prefix), encoded); err != nil {
		return nil, 0, err
	}

	scanPrefix := recordDBKey(prefix)
	records := make([]*wire.DKVSRecord, 0, limit)
	activeIndex := 0
	err = i.db.BatchReadV2(scanPrefix, scanPrefix, false, func(_, value []byte) error {
		record, err := UnmarshalRecord(value)
		if err != nil {
			return err
		}
		if record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/") {
			return nil
		}
		if !recordActiveForMeta(record, height, now) {
			return nil
		}
		if activeIndex >= start {
			records = append(records, record)
			if len(records) >= limit {
				return errStopScan
			}
		}
		activeIndex++
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	total := int(meta.ActiveCount)
	if meta.ActiveCount > uint64(math.MaxInt) {
		total = math.MaxInt
	}
	return records, total, err
}
