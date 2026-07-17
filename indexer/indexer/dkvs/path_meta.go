package dkvs

import (
	"encoding/binary"
	"errors"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const pathMetaVersion = uint32(1)

var pathMetaKeyPrefix = []byte("dkvs:path-meta:")

// PathMeta is a compact aggregate for a business collection path. It is stored
// outside the user-visible DKVS namespace and is updated atomically with the
// records it describes.
type PathMeta struct {
	Version       uint32 `json:"version"`
	Path          string `json:"path"`
	Generation    uint64 `json:"generation"`
	ActiveCount   uint64 `json:"active_count"`
	ActiveBytes   uint64 `json:"active_bytes"`
	UpdatedHeight uint64 `json:"updated_height"`
	UpdatedAt     uint64 `json:"updated_at"`
}

func collectionPathForParsed(parsed ParsedKey) (string, bool) {
	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) >= 1 && validAccountID(parsed.Segments[0]) {
			return "/personal/" + parsed.Segments[0], true
		}
	case "svc":
		if len(parsed.Segments) >= 1 {
			return "/svc/" + parsed.Segments[0], true
		}
	case "mail":
		if len(parsed.Segments) >= 2 && (parsed.Segments[1] == "msg" || parsed.Segments[1] == "share") {
			return "/mail/" + parsed.Segments[0] + "/" + parsed.Segments[1], true
		}
	case "blob":
		if len(parsed.Segments) >= 2 && validAccountID(parsed.Segments[0]) {
			return "/blob/" + parsed.Segments[0] + "/" + parsed.Segments[1], true
		}
	}
	return "", false
}

func normalizeCollectionPath(path string) (string, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	parsed, err := parseKeyParts(path)
	if err != nil {
		return "", err
	}
	candidate, ok := collectionPathForParsed(parsed)
	if !ok || candidate != path {
		return "", ErrInvalidKey
	}
	return candidate, nil
}

func pathMetaDBKey(path string) []byte {
	out := make([]byte, 0, len(pathMetaKeyPrefix)+len(path))
	out = append(out, pathMetaKeyPrefix...)
	out = append(out, path...)
	return out
}

func encodePathMeta(meta PathMeta) []byte {
	var encoded [44]byte
	binary.LittleEndian.PutUint32(encoded[0:4], pathMetaVersion)
	binary.LittleEndian.PutUint64(encoded[4:12], meta.Generation)
	binary.LittleEndian.PutUint64(encoded[12:20], meta.ActiveCount)
	binary.LittleEndian.PutUint64(encoded[20:28], meta.ActiveBytes)
	binary.LittleEndian.PutUint64(encoded[28:36], meta.UpdatedHeight)
	binary.LittleEndian.PutUint64(encoded[36:44], meta.UpdatedAt)
	return encoded[:]
}

func decodePathMeta(path string, encoded []byte) (*PathMeta, error) {
	if len(encoded) != 44 || binary.LittleEndian.Uint32(encoded[0:4]) != pathMetaVersion {
		return nil, ErrInvalidRecord
	}
	return &PathMeta{
		Version:       pathMetaVersion,
		Path:          path,
		Generation:    binary.LittleEndian.Uint64(encoded[4:12]),
		ActiveCount:   binary.LittleEndian.Uint64(encoded[12:20]),
		ActiveBytes:   binary.LittleEndian.Uint64(encoded[20:28]),
		UpdatedHeight: binary.LittleEndian.Uint64(encoded[28:36]),
		UpdatedAt:     binary.LittleEndian.Uint64(encoded[36:44]),
	}, nil
}

func (i *Indexer) readPathMetaLocked(path string) (*PathMeta, error) {
	encoded, err := i.db.Read(pathMetaDBKey(path))
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return decodePathMeta(path, encoded)
}

func (i *Indexer) computePathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
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
	for _, record := range records {
		meta.ActiveCount++
		meta.ActiveBytes += uint64(RecordSize(record))
	}
	return meta, nil
}

func (i *Indexer) ensurePathMetaLocked(path string, height, now uint64) (*PathMeta, error) {
	meta, err := i.readPathMetaLocked(path)
	if err == nil {
		return meta, nil
	}
	if !errors.Is(err, ErrRecordNotFound) {
		return nil, err
	}
	return i.computePathMetaLocked(path, height, now)
}

func recordIsActive(record *wire.DKVSRecord, height, now uint64) bool {
	return record != nil && !IsTombstone(record.Flags) && !IsExpired(record, height, now)
}

func (i *Indexer) updatePathMetaBatchLocked(batch indexercommon.WriteBatch, parsed ParsedKey,
	existing, replacement *wire.DKVSRecord, height, now uint64) error {

	path, ok := collectionPathForParsed(parsed)
	if !ok {
		return nil
	}
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return err
	}

	oldActive := recordIsActive(existing, height, now)
	if oldActive && i.activeError(existing, height, now) != nil {
		oldActive = false
	}
	newActive := recordIsActive(replacement, height, now)

	changed := false
	if oldActive {
		oldSize := uint64(RecordSize(existing))
		if meta.ActiveCount > 0 {
			meta.ActiveCount--
		}
		if meta.ActiveBytes >= oldSize {
			meta.ActiveBytes -= oldSize
		} else {
			meta.ActiveBytes = 0
		}
		changed = true
	}
	if newActive {
		meta.ActiveCount++
		meta.ActiveBytes += uint64(RecordSize(replacement))
		changed = true
	}
	if !changed {
		return nil
	}
	meta.Generation++
	meta.UpdatedHeight = height
	meta.UpdatedAt = now
	return batch.Put(pathMetaDBKey(path), encodePathMeta(*meta))
}

func (i *Indexer) PathMeta(path string) (*PathMeta, error) {
	normalized, err := normalizeCollectionPath(path)
	if err != nil {
		return nil, err
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.ensurePathMetaLocked(normalized, height, now)
}

func (i *Indexer) usageV2(prefix string) (*Usage, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, err
	}
	normalized := strings.TrimSuffix(prefix, "/")
	if path, err := normalizeCollectionPath(normalized); err == nil {
		meta, err := i.PathMeta(path)
		if err != nil {
			return nil, err
		}
		return &Usage{
			Prefix:          path,
			ActiveRecords:   meta.ActiveCount,
			ActiveTotalSize: meta.ActiveBytes,
		}, nil
	}

	records, _, _, err := i.scan(normalized, nil, 0, true)
	if err != nil {
		return nil, err
	}
	usage := &Usage{Prefix: normalized}
	for _, record := range records {
		usage.ActiveRecords++
		usage.ActiveTotalSize += uint64(RecordSize(record))
	}
	return usage, nil
}

func (i *Indexer) listPrefixV2(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, 0, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, 0, err
	}
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = 100
	}
	normalized := strings.TrimSuffix(prefix, "/")
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	if path, err := normalizeCollectionPath(normalized); err == nil {
		meta, err := i.ensurePathMetaLocked(path, height, now)
		if err != nil {
			return nil, 0, err
		}
		records, err := i.listPrefixPageLocked(path, start, limit, height, now)
		if err != nil {
			return nil, 0, err
		}
		return records, uint64ToInt(meta.ActiveCount), nil
	}
	return i.listPrefixLocked(normalized, start, limit, height, now)
}

func (i *Indexer) listPrefixPageLocked(prefix string, start, limit int, height, now uint64) ([]*wire.DKVSRecord, error) {
	scanPrefix := recordDBKey(prefix)
	records := make([]*wire.DKVSRecord, 0, limit)
	activeOffset := 0
	err := i.db.BatchReadV2(scanPrefix, scanPrefix, false, func(_, value []byte) error {
		record, err := UnmarshalRecord(value)
		if err != nil {
			return err
		}
		if record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/") {
			return nil
		}
		if i.activeError(record, height, now) != nil {
			return nil
		}
		if activeOffset < start {
			activeOffset++
			return nil
		}
		records = append(records, record)
		if len(records) >= limit {
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	return records, err
}

func uint64ToInt(value uint64) int {
	maxInt := uint64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func (i *Indexer) zeroServicePathMetaBatchLocked(batch indexercommon.WriteBatch, serviceName string, height, now uint64) error {
	path := "/svc/" + serviceName
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return err
	}
	if meta.ActiveCount == 0 && meta.ActiveBytes == 0 {
		return nil
	}
	meta.ActiveCount = 0
	meta.ActiveBytes = 0
	meta.Generation++
	meta.UpdatedHeight = height
	meta.UpdatedAt = now
	return batch.Put(pathMetaDBKey(path), encodePathMeta(*meta))
}
