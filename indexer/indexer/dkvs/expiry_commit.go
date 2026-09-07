package dkvs

import (
	"sort"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type expiryCommitResult struct {
	paths []string
}

// stageExpiredRecordsLocked physically removes expired records and updates the
// corresponding PathMeta in the same write batch. Canonical records retain a
// durable sequence floor for network convergence. Endpoint-local cache data
// does not retain a delete marker; only EndpointGeneration advances.
func (i *Indexer) stageExpiredRecordsLocked(batch indexercommon.WriteBatch,
	records []*wire.DKVSRecord, height, now uint64) (expiryCommitResult, error) {

	result := expiryCommitResult{}
	if batch == nil || len(records) == 0 {
		return result, nil
	}

	ordered := make([]*wire.DKVSRecord, 0, len(records))
	for _, record := range records {
		if record != nil {
			ordered = append(ordered, record)
		}
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].Key < ordered[b].Key })

	pathMetas := make(map[string]*PathMeta)
	relayPaths := make(map[string]struct{})

	for _, record := range ordered {
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return result, err
		}
		if err := batch.Delete(recordDBKey(record.Key)); err != nil {
			return result, err
		}
		if err := batch.Delete(hashDBKey(RecordHash(record))); err != nil {
			return result, err
		}

		path := collectionPath(parsed)
		localOnly := isEndpointCacheRecord(record) || pathMode(parsed) == PathLocalOnly || path == ""
		var pathGeneration uint64
		if path != "" && pathMode(parsed) != PathLocalOnly {
			meta := pathMetas[path]
			if meta == nil {
				current, metaErr := i.ensurePathMetaLocked(path, height, now)
				if metaErr != nil {
					return result, metaErr
				}
				meta = clonePathMeta(current)
				pathMetas[path] = meta
			}
			if meta.EndpointGeneration == ^uint64(0) {
				return result, ErrStaleGeneration
			}
			meta.EndpointGeneration++
			pathGeneration = meta.EndpointGeneration
			if !localOnly {
				if meta.Generation == ^uint64(0) {
					return result, ErrStaleGeneration
				}
				meta.Generation++
				pathGeneration = meta.Generation
				meta.ViewHeight = height
				relayPaths[path] = struct{}{}
			}
		}

		if localOnly {
			if err := deleteDeleteStateBatch(batch, record.Key); err != nil {
				return result, err
			}
		} else {
			state := &deleteState{
				FloorSeq:       record.Seq,
				PathGeneration: pathGeneration,
				PubKey:         append([]byte(nil), record.PubKey...),
			}
			state.EffectiveHash = deleteFloorEffectiveHash(
				record.Key, state.FloorSeq, state.PathGeneration, state.PubKey,
			)
			if err := putDeleteStateBatch(batch, record.Key, state); err != nil {
				return result, err
			}
			xorDeleteFloorRoot(&pathMetas[path].StateRoot, record.Key, state)
		}
	}

	paths := make([]string, 0, len(relayPaths))
	for path, meta := range pathMetas {
		normalizePathMetaAliases(meta)
		if err := putPathMetaBatch(batch, meta); err != nil {
			return result, err
		}
		if err := putPathStatusBatch(batch, &PathLocalStatus{Path: path, UpdatedAt: now}); err != nil {
			return result, err
		}
		if _, relay := relayPaths[path]; relay {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	result.paths = paths
	return result, nil
}

func (i *Indexer) notifyExpiryCommit(result expiryCommitResult) {
	for _, path := range result.paths {
		i.notifyPath(path)
	}
}
