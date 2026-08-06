package p2p

import (
	"errors"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const AntiEntropyInterval = 15 * time.Minute

// Store is the complete DKVS v1 P2P surface. Full path snapshot support is a
// required part of the v1 protocol rather than an optional compatibility path.
type Store interface {
	PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error)
	GetDKVSRecordForRelay(key string) (*wire.DKVSRecord, error)
	GetDKVSRecordByHashForRelay(hash chainhash.Hash) (*wire.DKVSRecord, error)
	SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error)
	ApplyDKVSMirror(filters []dkvs.Subscription, records []*wire.DKVSRecord, root chainhash.Hash) (int, error)
	GetDKVSPathSnapshot(path string) (*dkvs.PathSnapshot, error)
	ApplyDKVSPathSnapshot(snapshot *dkvs.PathSnapshot) (int, error)
	ListDKVSSubscriptions() []dkvs.Subscription
	IsDKVSSubscribed(key string) bool
}

type Peer interface {
	QueueMessage(msg wire.Message, doneChan chan<- struct{})
	Services() wire.ServiceFlag
	ValidatorId() string
}

type SignFunc func(payload []byte) ([]byte, error)

type Handler struct {
	Store           Store
	Peer            *PeerState
	Node            *NodeState
	Send            func(wire.Message)
	Broadcast       func(*wire.MsgDKVSNotify)
	Penalize        func(persistent, transient uint32, reason string)
	LocalServices   wire.ServiceFlag
	RemoteServices  wire.ServiceFlag
	TrustedSource   bool
	MirrorAuthority bool
	ValidatorID     string
	// PeerValidatorID remains only as a source-compatible test fixture field.
	// New callers must set ValidatorID.
	PeerValidatorID string
	Net             wire.BitcoinNet
	Sign            SignFunc
	Debugf          func(format string, args ...interface{})
	Warnf           func(format string, args ...interface{})
}

func NewHandler(store Store, state *PeerState, node *NodeState, send func(wire.Message)) Handler {
	if state == nil {
		state = &PeerState{}
	}
	if node == nil {
		node = &NodeState{}
	}
	return Handler{Store: store, Peer: state, Node: node, Send: send}
}

func (h Handler) valid() bool {
	return h.Store != nil && h.Peer != nil && h.Node != nil
}

func (h Handler) localMiner() bool {
	return h.LocalServices&wire.SFNodeMiner != 0
}

func (h Handler) remoteMiner() bool {
	return h.RemoteServices&wire.SFNodeMiner != 0
}

func (h Handler) allowedPathSnapshotSource() bool {
	// A path repair is requested from the exact peer that announced the
	// divergence. Its response is bound to the negotiated validator identity
	// and still passes full record, permission, fee-proof and StateRoot checks.
	// Generic mirror synchronization remains restricted to TrustedSource.
	return h.validatorID() != ""
}

func (h Handler) allowedIncrementalSource() bool {
	return h.localMiner() || h.TrustedSource
}

func (h Handler) shouldStoreKey(key string) bool {
	return h.localMiner() || h.Store.IsDKVSSubscribed(key)
}

func (h Handler) validatorID() string {
	if h.ValidatorID != "" {
		return h.ValidatorID
	}
	return h.PeerValidatorID
}

func (h Handler) send(msg wire.Message) {
	if msg != nil && h.Send != nil {
		h.Send(msg)
	}
}

func (h Handler) broadcast(record *wire.DKVSRecord) {
	if record == nil || h.Broadcast == nil {
		return
	}
	if msg := NotifyForRecord(record); msg != nil {
		h.Broadcast(msg)
	}
}

func (h Handler) penalize(persistent, transient uint32, reason string) {
	if h.Penalize != nil {
		h.Penalize(persistent, transient, reason)
	}
}

func (h Handler) debugf(format string, args ...interface{}) {
	if h.Debugf != nil {
		h.Debugf(format, args...)
	}
}

func (h Handler) warnf(format string, args ...interface{}) {
	if h.Warnf != nil {
		h.Warnf(format, args...)
	}
}

func (h Handler) ShouldRequestSync() bool {
	if !h.valid() || !h.remoteMiner() {
		return false
	}
	if !h.localMiner() && !h.TrustedSource {
		return false
	}
	return ShouldRequestSync(h.LocalServices, h.RemoteServices, len(h.Store.ListDKVSSubscriptions()))
}

func (h Handler) OnNotify(msg *wire.MsgDKVSNotify) {
	if !h.valid() || msg == nil {
		return
	}
	if h.Peer.BufferNotify(msg) {
		return
	}
	if !h.allowedIncrementalSource() {
		h.warnf("reject unsolicited DKVS notify from untrusted source")
		return
	}
	record, err := RecordFromNotify(msg)
	if err != nil {
		h.penalize(0, 10, "invalid DKVS notify")
		h.warnf("invalid DKVS notify: %v", err)
		return
	}
	if !h.shouldStoreKey(record.Key) || !h.Peer.AcceptNotify(record, time.Now()) {
		return
	}
	updated, err := h.Store.PutRemoteDKVSRecord(record)
	if err != nil {
		if h.queuePathRepair(record, err) {
			h.warnf("DKVS path %s requires full synchronization: %v", record.Key, err)
			return
		}
		h.warnf("apply DKVS notify failed: %v", err)
		return
	}
	if updated {
		h.debugf("applied DKVS notify %s", record.Key)
		h.broadcast(record)
	}
}

func (h Handler) OnInv(msg *wire.MsgDKVSInv) {
	if !h.valid() || msg == nil || !h.allowedIncrementalSource() {
		return
	}
	request := &wire.MsgDKVSGet{}
	for _, item := range msg.Items {
		if !h.shouldStoreKey(item.Key) {
			continue
		}
		local, err := h.Store.GetDKVSRecordForRelay(item.Key)
		if err != nil || local == nil || dkvs.RecordHash(local) != item.RecordHash {
			request.RecordHashes = append(request.RecordHashes, item.RecordHash)
		}
		if len(request.RecordHashes) >= wire.MaxDKVSItemsPerMsg {
			break
		}
	}
	if len(request.RecordHashes) == 0 {
		return
	}
	h.Peer.TrackRequest(request, time.Now())
	h.send(request)
}

func (h Handler) OnGet(msg *wire.MsgDKVSGet) {
	if !h.valid() || msg == nil {
		return
	}
	records := make([]*wire.DKVSRecord, 0, len(msg.Keys)+len(msg.RecordHashes))
	notFound := make([]chainhash.Hash, 0)
	seen := make(map[chainhash.Hash]struct{})
	for _, key := range msg.Keys {
		record, err := h.Store.GetDKVSRecordForRelay(key)
		if err != nil || record == nil {
			notFound = append(notFound, KeyHash(key))
			continue
		}
		hash := dkvs.RecordHash(record)
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		records = append(records, record)
	}
	for _, hash := range msg.RecordHashes {
		record, err := h.Store.GetDKVSRecordByHashForRelay(hash)
		if err != nil || record == nil {
			notFound = append(notFound, hash)
			continue
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		records = append(records, record)
	}
	for _, response := range DataMessages(OrderRecords(records), notFound) {
		h.send(response)
	}
}

func (h Handler) OnData(msg *wire.MsgDKVSData) {
	if !h.valid() || msg == nil || !h.allowedIncrementalSource() {
		return
	}
	now := time.Now()
	for _, record := range msg.Records {
		if record == nil || !h.Peer.ConsumeRequest(record, now) || !h.shouldStoreKey(record.Key) {
			continue
		}
		updated, err := h.Store.PutRemoteDKVSRecord(record)
		if err != nil {
			if h.queuePathRepair(record, err) {
				h.warnf("DKVS path %s requires full synchronization: %v", record.Key, err)
				continue
			}
			h.warnf("apply DKVS data failed: %v", err)
			continue
		}
		if updated {
			h.broadcast(record)
		}
	}
	h.Peer.ConsumeNotFound(msg.NotFound)
}

func (h Handler) OnSyncRequest(msg *wire.MsgDKVSSyncRequest) {
	if !h.valid() || msg == nil || msg.SessionID == 0 {
		return
	}
	if len(msg.Filters) == 0 {
		if !h.localMiner() || !h.remoteMiner() {
			return
		}
	} else if !h.MirrorAuthority {
		return
	}
	if !h.Peer.BeginServe(msg) {
		return
	}
	if h.servePathSync(msg) {
		return
	}
	filters := SubscriptionsFromFilters(msg.Filters)
	records, next, done, root, err := h.Store.SyncFilteredDKVSRecords(msg.Cursor, msg.Limit, filters)
	if err != nil {
		h.Peer.CancelServe()
		h.warnf("serve DKVS sync failed: %v", err)
		return
	}
	response := &wire.MsgDKVSSyncResponse{
		SessionID: msg.SessionID, Records: records, NextCursor: next,
		Done: done, CheckpointRoot: root,
	}
	if h.MirrorAuthority {
		if h.Sign == nil {
			h.Peer.CancelServe()
			return
		}
		response.SourceSignature, err = h.Sign(SyncAuthPayload(h.Net, msg.Cursor, msg.Filters, response))
		if err != nil {
			h.Peer.CancelServe()
			h.warnf("sign DKVS sync response failed: %v", err)
			return
		}
	}
	h.send(response)
	if done {
		for _, pending := range h.Peer.FinishServe(msg.SessionID) {
			h.send(pending)
		}
	}
}

func (h Handler) QueueSync(cursor []byte) {
	if !h.ShouldRequestSync() {
		return
	}
	mirror := !h.localMiner() || !h.Node.Ready()
	start, err := h.Peer.StartSync(cursor, mirror, h.localMiner(),
		FiltersFromSubscriptions(h.Store.ListDKVSSubscriptions()), time.Now())
	if err != nil || start.Request == nil {
		return
	}
	if start.Started && start.Mirror {
		h.Node.SetReady(false)
	}
	h.send(start.Request)
}

func (h Handler) OnSyncResponse(msg *wire.MsgDKVSSyncResponse) {
	if !h.valid() || msg == nil {
		return
	}
	activePath, pathSync := h.Peer.ActivePathSync()
	verify := func(cursor []byte, filters []wire.DKVSSyncFilter, response *wire.MsgDKVSSyncResponse) bool {
		if pathSync {
			return h.allowedPathSnapshotSource() &&
				VerifySyncSignature(h.Net, h.validatorID(), cursor, filters, response)
		}
		return h.TrustedSource && VerifySyncSignature(h.Net, h.validatorID(), cursor, filters, response)
	}
	shouldStore := h.shouldStoreKey
	if pathSync {
		shouldStore = func(string) bool { return true }
	}
	action, err := h.Peer.AcceptSyncResponse(msg, verify, shouldStore, time.Now())
	if err != nil {
		h.warnf("reject DKVS sync response: %v", err)
		if errors.Is(err, ErrUnauthenticatedSync) {
			h.penalize(0, 20, "unauthenticated DKVS sync response")
		}
		if pathSync && !errors.Is(err, ErrUnsolicitedSync) {
			retry := !errors.Is(err, ErrUnauthenticatedSync)
			h.finishFailedPathSync(activePath, retry)
			return
		}
		if errors.Is(err, ErrSyncRootChanged) {
			h.QueueSync(nil)
		}
		return
	}
	for _, record := range action.MergeRecords {
		updated, applyErr := h.Store.PutRemoteDKVSRecord(record)
		if applyErr != nil {
			if h.queuePathRepair(record, applyErr) {
				continue
			}
			h.warnf("merge DKVS sync record failed: %v", applyErr)
			continue
		}
		if updated {
			h.broadcast(record)
		}
	}
	if !action.Done {
		if pathSync {
			h.queuePathSyncContinuation(action.NextCursor)
		} else {
			h.QueueSync(action.NextCursor)
		}
		return
	}
	if !action.Mirror {
		return
	}
	if snapshot, isPath, snapshotErr := pathActionSnapshot(action); isPath {
		if snapshotErr != nil {
			h.warnf("decode DKVS path snapshot failed: %v", snapshotErr)
			h.finishFailedPathSync(activePath, true)
			return
		}
		if _, applyErr := h.Store.ApplyDKVSPathSnapshot(snapshot); applyErr != nil {
			h.warnf("apply DKVS path snapshot failed: %v", applyErr)
			h.finishFailedPathSync(snapshot.Path, true)
			return
		}
		// The incremental notify that triggered this repair was not committed.
		// Re-announce the authenticated active records after the atomic snapshot
		// commit so downstream subscribed peers can repair the same path.
		for _, record := range snapshot.Records {
			if record != nil && !dkvs.IsTombstone(record.Flags) {
				h.broadcast(record)
			}
		}
		h.Node.MarkTrusted(time.Now())
		h.Node.SetReady(true)
		h.queueNextPathSync()
		return
	}
	filters := SubscriptionsFromFilters(action.Filters)
	records := append(action.MirrorRecords, action.MirrorDeletes...)
	if _, err := h.Store.ApplyDKVSMirror(filters, records, action.Root); err != nil {
		h.warnf("apply DKVS mirror failed: %v", err)
		h.QueueSync(nil)
		return
	}
	h.Node.MarkTrusted(time.Now())
	h.Node.SetReady(true)
}

func (h Handler) OnTrustedConnected() {
	if !h.valid() {
		return
	}
	forceMirror := h.Node.ObserveTrusted(time.Now(), h.localMiner())
	if forceMirror || !h.Node.Ready() || !h.localMiner() {
		h.QueueSync(nil)
	}
}

func (h Handler) RunAntiEntropy(stop <-chan struct{}) {
	if !h.valid() || !ShouldRunAntiEntropy(h.LocalServices, len(h.Store.ListDKVSSubscriptions())) {
		return
	}
	ticker := time.NewTicker(AntiEntropyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.QueueSync(nil)
		case <-stop:
			return
		}
	}
}
