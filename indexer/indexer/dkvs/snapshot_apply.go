package dkvs

import (
	"bytes"
	"sort"
	"strconv"

	"github.com/sat20-labs/satoshinet/wire"
)

// prevalidateSnapshot performs all record-local and cross-record checks that
// can be completed without mutating the destination. PutRemote still performs
// the authoritative state check immediately before each commit, but malformed
// records no longer leave a partially applied snapshot behind.
func (i *Indexer) prevalidateSnapshot(snapshot *Snapshot) ([]*wire.DKVSRecord, error) {
	if snapshot == nil {
		return nil, ErrInvalidSnapshot
	}
	validators := i.snapshotValidators()
	height := i.currentHeight()
	now := currentUnixMilli()
	seen := make(map[string]*wire.DKVSRecord, len(snapshot.Records))
	ordered := make([]*wire.DKVSRecord, 0, len(snapshot.Records))

	for _, record := range snapshot.Records {
		if record == nil {
			return nil, ErrInvalidSnapshot
		}
		if _, ok := seen[record.Key]; ok {
			return nil, ErrInvalidSnapshot
		}
		parsed, err := validateParsedCoreWithVerifier(record, height, now, false, false, nil)
		if err != nil || IsTombstone(record.Flags) {
			return nil, ErrInvalidSnapshot
		}
		if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
			return nil, err
		}
		state, err := i.readWriteStateSnapshot(record.Key, parsed, validators)
		if err != nil {
			return nil, err
		}
		if _, err := validateWritePermissionWith(
			parsed, record, state.existing, state.requiresResolve, validators,
		); err != nil {
			return nil, err
		}
		seen[record.Key] = record
		ordered = append(ordered, record)
	}
	if err := validateSnapshotBlobRecords(seen, i.blob); err != nil {
		return nil, err
	}

	// Blob chunks require their manifest to be present in the destination.
	// Snapshot export is key-sorted, where "chunk" precedes "manifest", so
	// preserve order within each class while committing manifests first.
	sort.SliceStable(ordered, func(a, b int) bool {
		return snapshotRecordPriority(ordered[a]) < snapshotRecordPriority(ordered[b])
	})
	return ordered, nil
}

func snapshotRecordPriority(record *wire.DKVSRecord) int {
	if record == nil {
		return 1
	}
	parsed, err := ParseKey(record.Key)
	if err != nil || parsed.Namespace != "blob" || len(parsed.Segments) < 3 {
		return 1
	}
	switch parsed.Segments[2] {
	case "manifest":
		return 0
	case "chunk":
		return 2
	default:
		return 1
	}
}

func validateSnapshotBlobRecords(records map[string]*wire.DKVSRecord, policy BlobPolicy) error {
	for _, record := range records {
		parsed, err := ParseKey(record.Key)
		if err != nil || parsed.Namespace != "blob" {
			continue
		}
		if len(parsed.Segments) < 3 {
			return ErrInvalidSnapshot
		}
		if parsed.Segments[2] == "manifest" {
			manifest, err := parseBlobManifest(record.Value, policy)
			if err != nil || manifest.TTL != record.TTL || manifest.ExpiryHeight != record.ExpiryHeight {
				return ErrBlobManifestInvalid
			}
			continue
		}
		if parsed.Segments[2] != "chunk" || len(parsed.Segments) != 4 {
			return ErrInvalidSnapshot
		}
		manifestKey := "/blob/" + parsed.Segments[0] + "/" + parsed.Segments[1] + "/manifest"
		manifestRecord := records[manifestKey]
		if manifestRecord == nil {
			return ErrBlobManifestInvalid
		}
		manifest, err := parseBlobManifest(manifestRecord.Value, policy)
		if err != nil {
			return err
		}
		if !bytes.Equal(record.PubKey, manifestRecord.PubKey) || record.Seq != manifestRecord.Seq ||
			record.IssueTime != manifestRecord.IssueTime || record.TTL != manifestRecord.TTL ||
			record.ExpiryHeight != manifestRecord.ExpiryHeight {
			return ErrBlobChunkInvalid
		}
		index, err := parseCanonicalChunkIndex(parsed.Segments[3])
		if err != nil {
			return err
		}
		if err := validateBlobChunkHash(manifest, index, record.Value); err != nil {
			return err
		}
	}
	return nil
}

func parseCanonicalChunkIndex(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, ErrInvalidKey
	}
	return uint32(parsed), nil
}
