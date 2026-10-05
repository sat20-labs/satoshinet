package p2p

import (
	"errors"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const AntiEntropyInterval = 15 * time.Minute

type Store interface {
	AcceptDKVSCurrentRecord(record *wire.DKVSRecord) (bool, error)
	DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error)
	ApplyDKVSPathSnapshotFrom(snapshot *dkvs.PathSnapshot, baseline dkvs.ActiveMeta) (int, error)
	DKVSNetworkPaths() ([]string, error)
	GetDKVSRecordForRelay(key string) (*wire.DKVSRecord, error)
	GetDKVSRecordByHashForRelay(hash chainhash.Hash) (*wire.DKVSRecord, error)
	SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error)
	GetDKVSPathSnapshot(path string) (*dkvs.PathSnapshot, error)
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
	Store             Store
	Peer              *PeerState
	Node              *NodeState
	Send              func(wire.Message)
	Broadcast         func(*wire.MsgDKVSNotify)
	RequestPathRepair func(string)
	Penalize          func(persistent, transient uint32, reason string)
	LocalServices     wire.ServiceFlag
	RemoteServices    wire.ServiceFlag
	TrustedSource     bool
	MirrorAuthority   bool
	ValidatorID       string
	PeerValidatorID   string
	Net               wire.BitcoinNet
	Sign              SignFunc
	Debugf            func(format string, args ...interface{})
	Warnf             func(format string, args ...interface{})
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
func (h Handler) valid() bool                     { return h.Store != nil && h.Peer != nil && h.Node != nil }
func (h Handler) localMiner() bool                { return h.LocalServices&wire.SFNodeMiner != 0 }
func (h Handler) remoteMiner() bool               { return h.RemoteServices&wire.SFNodeMiner != 0 }
func (h Handler) allowedPathSnapshotSource() bool { return h.TrustedSource && h.validatorID() != "" }
func (h Handler) allowedIncrementalSource() bool  { return h.TrustedSource }
func (h Handler) shouldStoreKey(key string) bool {
	return dkvs.IsAccountMappingBindingKey(key) || h.localMiner() || h.Store.IsDKVSSubscribed(key)
}
func (h Handler) validatorID() string {
	if h.ValidatorID != "" {
		return h.ValidatorID
	}
	return h.PeerValidatorID
}
func (h Handler) send(msg wire.Message) {
	if msg == nil || h.Send == nil {
		return
	}
	if request, ok := msg.(*wire.MsgDKVSSyncRequest); ok && !h.prepareCurrentRequest(request) {
		return
	}
	h.Send(msg)
}
func (h Handler) broadcast(record *wire.DKVSRecord) {
	if record == nil || h.Broadcast == nil {
		return
	}
	// Full-view/discovery content is a hint, not a newly authored commit.
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
	if !h.valid() {
		return false
	}
	return h.allowedPathSnapshotSource()
}

func (h Handler) OnNotify(msg *wire.MsgDKVSNotify) {
	if !h.valid() || msg == nil {
		return
	}
	if msg.Target != "" {
		if msg.EventType != wire.DKVSNotifyEventMessage {
			h.penalize(0, 10, "invalid directed DKVS notify event")
			h.warnf("reject directed DKVS notify with event type %d", msg.EventType)
			return
		}
		if !h.TrustedSource {
			h.warnf("reject directed DKVS notify from untrusted source")
			return
		}
		if handled, err := handleDirectedNotify(h.Store, msg); handled && err != nil {
			h.warnf("directed DKVS notify failed: %v", err)
		}
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
	if !h.shouldStoreKey(record.Key) {
		return
	}
	if !h.allowedPathSnapshotSource() {
		h.warnf("reject DKVS notify without an authenticated source identity")
		return
	}
	if h.Node.bufferPendingNotify(msg) {
		return
	}
	h.applyNotifyRecord(record)
}

func (h Handler) applyNotifyRecord(record *wire.DKVSRecord) {
	updated, err := h.applyCurrentNotification(record)
	if err != nil {
		if h.queuePathRepair(record, err) {
			h.warnf("DKVS path %s requires current-state reconciliation: %v", record.Key, err)
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
		updated, err := h.applyCurrentRecord(record)
		if err != nil {
			if h.queuePathRepair(record, err) {
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
	// Network DKVS contains only globally relayable current state. Any peer may
	// bootstrap that public view, but only a Core/Bootstrap mirror authority
	// may serve the signed sync session.
	if !h.MirrorAuthority {
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
	response := &wire.MsgDKVSSyncResponse{SessionID: msg.SessionID, Records: records, NextCursor: next, Done: done, CheckpointRoot: root}
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
		h.Peer.FinishServe(msg.SessionID)
	}
}

func (h Handler) QueueSync(cursor []byte) {
	if !h.ShouldRequestSync() || h.Peer.Closed() {
		return
	}
	if len(cursor) != 0 {
		if !h.Node.ownsSync(h.Peer) {
			return
		}
		start, err := h.Peer.StartSync(cursor, h.localMiner(), nil, time.Now())
		if err == nil && start.Request != nil {
			h.sendPathSyncRequest(start.Request)
		}
		return
	}
	h.Node.pendingMtx.Lock()
	// Other connections join this synchronization round. Do not enqueue a
	// duplicate discovery behind its directory snapshots.
	if h.Node.syncPeer == nil && len(h.Node.pendingPathSyncSet) == 0 {
		h.Node.enqueuePathSyncLocked("")
	}
	h.Node.pendingMtx.Unlock()
	h.queueNextPathSync()
}

func (h Handler) OnSyncResponse(msg *wire.MsgDKVSSyncResponse) {
	if !h.valid() || msg == nil || !h.Node.ownsSync(h.Peer) {
		return
	}
	activePath, pathSync := h.Peer.ActivePathSync()
	verify := func(cursor []byte, filters []wire.DKVSSyncFilter, response *wire.MsgDKVSSyncResponse) bool {
		if pathSync {
			return h.allowedPathSnapshotSource() && VerifySyncSignature(h.Net, h.validatorID(), cursor, filters, response)
		}
		return h.TrustedSource && VerifySyncSignature(h.Net, h.validatorID(), cursor, filters, response)
	}
	shouldStore := h.shouldStoreKey
	if pathSync {
		shouldStore = func(string) bool { return true }
	}
	action, err := h.Peer.AcceptSyncResponse(msg, verify, shouldStore, time.Now(), !pathSync)
	if action.Duplicate {
		return
	}
	if !errors.Is(err, ErrUnsolicitedSync) {
		h.Peer.cancelSync("request")
	}
	if err != nil {
		h.warnf("reject DKVS sync response: %v", err)
		if errors.Is(err, ErrUnauthenticatedSync) {
			h.penalize(0, 20, "unauthenticated DKVS sync response")
		}
		if pathSync && !errors.Is(err, ErrUnsolicitedSync) {
			h.finishFailedPathSync(activePath, !errors.Is(err, ErrUnauthenticatedSync))
			return
		}
		if !errors.Is(err, ErrUnsolicitedSync) {
			h.finishFailedPathSync("", !errors.Is(err, ErrUnauthenticatedSync))
		}
		return
	}
	if !action.Done {
		if pathSync {
			h.queuePathSyncContinuation(action.NextCursor)
		} else {
			h.QueueSync(action.NextCursor)
		}
		return
	}
	if snapshot, isPath, snapshotErr := pathActionSnapshot(action); isPath {
		if snapshotErr != nil {
			h.warnf("decode DKVS path snapshot failed: %v", snapshotErr)
			h.finishFailedPathSync(activePath, true)
			return
		}
		applyErr := h.applyCurrentPathSnapshot(snapshot, action)
		if applyErr != nil {
			h.warnf("apply DKVS path snapshot failed: %v", applyErr)
			h.finishFailedPathSync(snapshot.Path, !errors.Is(applyErr, dkvs.ErrPermissionDenied))
			return
		}
		for _, record := range snapshot.Records {
			if record != nil && !dkvs.IsTombstone(record.Flags) {
				h.broadcast(record)
			}
		}
		h.Node.finishSync(h.Peer, true)
		if !h.queueNextPathSync() {
			h.markReadyAndDrain()
		}
		return
	}
	h.reconcileDiscoveredPaths(action)
}

func (h Handler) OnTrustedConnected() {
	if !h.valid() {
		return
	}
	h.Node.ObserveTrusted(time.Now(), h.localMiner())
	h.QueueSync(nil)
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
