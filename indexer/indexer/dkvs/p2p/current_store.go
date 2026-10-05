package p2p

import (
	"sort"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// Requested Data and generic inventory responses contain content, not a
// current commit position. Even a previously requested, correctly signed body
// can have become stale during transit; it only prompts reconciliation.
func (h Handler) applyCurrentRecord(record *wire.DKVSRecord) (bool, error) {
	if !h.TrustedSource || h.validatorID() == "" {
		return false, dkvs.ErrPermissionDenied
	}
	if err := dkvs.VerifyRecordForClient(record, dkvs.RecordVerificationOptions{}); err != nil {
		return false, err
	}
	return false, dkvs.ErrPathDiverged
}

func (h Handler) prepareCurrentRequest(request *wire.MsgDKVSSyncRequest) bool {
	if len(request.Cursor) != 0 {
		return true
	}
	path, pathRequest := pathSyncFilter(request.Filters)
	if !pathRequest {
		return true
	}
	baseline, err := h.Store.DKVSNetworkSyncBaseline(path)
	if err != nil {
		h.warnf("cannot capture DKVS sync installation baseline: %v", err)
		return false
	}
	return h.Peer.SetSyncBaseline(request.SessionID, baseline)
}

// Generic anti-entropy discovers scopes. Signed complete snapshots include
// empty collections and are installed only against the captured local baseline.
func (h Handler) reconcileDiscoveredPaths(action SyncAction) {
	paths := make(map[string]struct{})
	local, err := h.Store.DKVSNetworkPaths()
	if err != nil {
		h.warnf("enumerate DKVS current paths: %v", err)
		h.finishFailedPathSync("", true)
		return
	}
	for _, path := range local {
		if h.localMiner() || filtersMatchKey(action.Filters, path) || h.Store.IsDKVSSubscribed(path) {
			paths[path] = struct{}{}
		}
	}
	for _, path := range action.DiscoveredPaths {
		paths[path] = struct{}{}
	}
	if len(paths) > MaxPendingChanges {
		h.warnf("too many DKVS current paths in one discovery")
		h.finishFailedPathSync("", false)
		return
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		h.Node.pendingMtx.Lock()
		h.Node.enqueuePathSyncLocked(path)
		h.Node.pendingMtx.Unlock()
	}
	h.Node.finishSync(h.Peer, true)
	if !h.queueNextPathSync() {
		h.markReadyAndDrain()
	}
}

func (h Handler) applyCurrentPathSnapshot(snapshot *dkvs.PathSnapshot, action SyncAction) error {
	if action.Baseline == nil {
		return dkvs.ErrInvalidSnapshot
	}
	_, err := h.Store.ApplyDKVSPathSnapshotFrom(snapshot, *action.Baseline)
	return err
}
