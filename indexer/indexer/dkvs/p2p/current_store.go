package p2p

import (
	"errors"
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
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	if !h.Node.finishDiscovery(h.Peer, ordered) {
		return
	}
	if !h.ResumeSync() {
		h.markReadyAndDrain()
	}
}

func (h Handler) applyCurrentPathSnapshot(snapshot *dkvs.PathSnapshot, action SyncAction) error {
	if action.Baseline == nil {
		return dkvs.ErrInvalidSnapshot
	}
	_, err := h.Store.ApplyDKVSPathSnapshotFrom(snapshot, *action.Baseline)
	if errors.Is(err, dkvs.ErrLocalBinding) {
		// This binding owns a singleton path. Let the mirror learn its
		// signed value and release the sync slot so notifications can drain.
		record, readErr := h.Store.GetDKVSRecordForRelay(snapshot.Path)
		if readErr != nil {
			return readErr
		}
		h.send(NotifyForRecord(record))
		return nil
	}
	return err
}
