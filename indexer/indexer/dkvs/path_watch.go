package dkvs

import (
	"context"
	"errors"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type pathWatchSignal struct {
	ch chan struct{}
	refs int
}

func (i *Indexer) subscribePath(path string) *pathWatchSignal {
	i.watchMutex.Lock()
	defer i.watchMutex.Unlock()
	if i.pathSignals == nil { i.pathSignals = make(map[string]*pathWatchSignal) }
	signal := i.pathSignals[path]
	if signal == nil { signal = &pathWatchSignal{ch: make(chan struct{})}; i.pathSignals[path] = signal }
	signal.refs++
	return signal
}

func (i *Indexer) unsubscribePath(path string, signal *pathWatchSignal) {
	i.watchMutex.Lock()
	defer i.watchMutex.Unlock()
	signal.refs--
	if signal.refs == 0 { delete(i.pathSignals, path) }
}

func (i *Indexer) pathSignal(signal *pathWatchSignal) <-chan struct{} {
	i.watchMutex.Lock()
	defer i.watchMutex.Unlock()
	return signal.ch
}

func (i *Indexer) notifyPath(path string) {
	if path == "" { return }
	i.watchMutex.Lock()
	defer i.watchMutex.Unlock()
	if signal := i.pathSignals[path]; signal != nil { close(signal.ch); signal.ch = make(chan struct{}) }
}

func (i *Indexer) notifyPathMutation(record *wire.DKVSRecord) {
	if record == nil { return }
	parsed, err := ParseKey(record.Key)
	if err == nil { i.notifyPath(collectionPath(parsed)) }
}

func (i *Indexer) notifyPathMutations(records []*wire.DKVSRecord) {
	paths := make(map[string]struct{})
	for _, record := range records {
		if record == nil { continue }
		if parsed, err := ParseKey(record.Key); err == nil { paths[collectionPath(parsed)] = struct{}{} }
	}
	for path := range paths { i.notifyPath(path) }
}

// computePathMetaViewLocked rebuilds only an in-memory current-state view.
// Watching or reading never mutates the database or constructs deleted leaves.
func (i *Indexer) computePathMetaViewLocked(path string, height, now uint64) (*PathMeta, error) {
	records, _, _, err := i.scanLocked(path, nil, 0, true, height, now)
	if err != nil { return nil, err }
	meta := &PathMeta{Version: pathMetaVersion, Path: path, ViewHeight: height}
	if previous, readErr := i.readPathMetaLocked(path); readErr == nil {
		meta.Generation, meta.EndpointGeneration = previous.Generation, previous.EndpointGeneration
	} else if !errors.Is(readErr, ErrRecordNotFound) { return nil, readErr }
	for _, record := range records {
		if !i.networkPathRecordVisible(record) { continue }
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(RecordSize(record))
		xorPathMetaRoot(&meta.StateRoot, record)
		updateMinExpiry(meta, record)
	}
	normalizePathMetaAliases(meta)
	return meta, nil
}

func (i *Indexer) getPathMetaReadOnly(path string) (*PathMeta, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if !isCanonicalCollectionPath(path) { return nil, ErrInvalidKey }
	parsed, err := ParsePrefix(path)
	if err != nil { return nil, err }
	if pathMode(parsed) == PathLocalOnly { return nil, ErrFreeLocalNotRelayable }
	height, now := i.currentHeight(), currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	meta, err := i.readPathMetaLocked(path)
	if err == nil {
		status, statusErr := i.readPathStatusLocked(path)
		if statusErr != nil && !errors.Is(statusErr, ErrRecordNotFound) { return nil, statusErr }
		if statusErr == nil && !pathMetaNeedsRebuild(meta, status, height) { return clonePathMeta(meta), nil }
	} else if !errors.Is(err, ErrRecordNotFound) { return nil, err }
	return i.computePathMetaViewLocked(path, height, now)
}

func pathWatchChanged(meta *PathMeta, generation uint64, root chainhash.Hash, viewHeight uint64) bool {
	return meta == nil || meta.Generation != generation || meta.StateRoot != root || (viewHeight != 0 && meta.ViewHeight != viewHeight)
}

// WaitPath observes canonical current-state changes without a per-request
// ticker. Mutations and expiry processing notify only after successful commit.
func (i *Indexer) WaitPath(ctx context.Context, path string, generation uint64, root chainhash.Hash, viewHeight uint64) (*PathMeta, bool, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if !isCanonicalCollectionPath(path) { return nil, false, ErrInvalidKey }
	watch := i.subscribePath(path)
	defer i.unsubscribePath(path, watch)
	for {
		// Subscribe/capture before reading so a concurrent commit cannot fall
		// into a read-then-sleep gap.
		signal := i.pathSignal(watch)
		meta, err := i.getPathMetaReadOnly(path)
		if err != nil || pathWatchChanged(meta, generation, root, viewHeight) { return meta, err == nil, err }
		select {
		case <-signal:
			continue
		case <-ctx.Done():
			latest, err := i.getPathMetaReadOnly(path)
			if err != nil { return nil, false, err }
			if pathWatchChanged(latest, generation, root, viewHeight) { return latest, true, nil }
			return latest, false, ctx.Err()
		}
	}
}
