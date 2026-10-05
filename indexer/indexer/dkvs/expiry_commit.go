package dkvs

import (
	"sort"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type expiryCommitResult struct { paths []string }

// Expiry changes the current visible set, not a per-key history. Both paid
// expiring data and endpoint-local leases are physically removed. The prefix
// metadata and current generation index change in the same database batch.
func (i *Indexer) stageExpiredRecordsLocked(batch indexercommon.WriteBatch,
	records []*wire.DKVSRecord, height, now uint64) (expiryCommitResult, error) {
	result := expiryCommitResult{}
	if batch == nil || len(records) == 0 { return result, nil }
	paths := make(map[string]bool)
	for _, record := range records {
		if record == nil { continue }
		parsed, err := ParseKey(record.Key)
		if err != nil { return result, err }
		if err := batch.Delete(recordDBKey(record.Key)); err != nil { return result, err }
		if err := batch.Delete(hashDBKey(RecordHash(record))); err != nil { return result, err }
		path := collectionPath(parsed)
		if path == "" || pathMode(parsed) == PathLocalOnly { continue }
		if err := i.clearChangedRecordBatch(batch, path, record.Key); err != nil { return result, err }
		paths[path] = paths[path] || !isEndpointCacheRecord(record)
	}
	for path, canonical := range paths {
		// This read view already excludes records expired at height. It does
		// not write metadata separately from the caller's atomic deletion.
		meta, err := i.computePathMetaViewLocked(path, height, now)
		if err != nil { return result, err }
		if meta.EndpointGeneration == ^uint64(0) { return result, ErrStaleGeneration }
		meta.EndpointGeneration++
		if canonical {
			if meta.Generation == ^uint64(0) { return result, ErrStaleGeneration }
			meta.Generation++
		}
		normalizePathMetaAliases(meta)
		if err := putPathMetaBatch(batch, meta); err != nil { return result, err }
		if err := putPathStatusBatch(batch, &PathLocalStatus{Path: path, UpdatedAt: now}); err != nil { return result, err }
		result.paths = append(result.paths, path)
	}
	sort.Strings(result.paths)
	return result, nil
}

func (i *Indexer) notifyExpiryCommit(result expiryCommitResult) {
	// FREE_LOCAL expiry must also wake an already connected wallet.
	for _, path := range result.paths { i.notifyPath(path) }
}
