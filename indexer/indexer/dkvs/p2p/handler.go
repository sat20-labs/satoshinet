package p2p

import (
	"errors"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type Store interface {
	PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error)
	GetDKVSRecordForRelay(key string) (*wire.DKVSRecord, error)
	GetDKVSRecordByHash(hash chainhash.Hash) (*wire.DKVSRecord, error)
	ApplyDKVSMirror(filters []dkvs.Subscription, records []*wire.DKVSRecord, root chainhash.Hash) (int, error)
	ApplyDKVSRecordSet(records []*wire.DKVSRecord) (int, error)
	SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error)
	GetDKVSCheckpoint() (*dkvs.Checkpoint, error)
	ListDKVSSubscriptions() []dkvs.Subscription
	IsDKVSSubscribed(key string) bool
}

type Handler struct {
	Store           Store
	Peer            *PeerState
	Node            *NodeState
	Net             wire.BitcoinNet
	ValidatorID     string
	LocalServices   wire.ServiceFlag
	RemoteServices  wire.ServiceFlag
	TrustedSource   bool
	MirrorAuthority bool
	Send            func(wire.Message)
	Broadcast       func(*wire.MsgDKVSNotify)
	Sign            func([]byte) ([]byte, error)
	Penalize        func(persistent, transient uint32, reason string)
	Debugf          func(string, ...interface{})
	Warnf           func(string, ...interface{})
}

func (h Handler) valid() bool {
	return h.Store != nil && h.Peer != nil && h.Node != nil
}

func (h Handler) localMiner() bool {
	return h.LocalServices&wire.SFNodeMiner != 0
}

func (h Handler) allowedIncrementalSource() bool {
	return h.localMiner() || h.TrustedSource
}

func (h Handler) shouldStoreKey(key string) bool {
	if h.localMiner() {
		return true
	}
	return h.Store.IsDKVSSubscribed(key)
}

func (h Handler) needsRecord(key string, hash chainhash.Hash) bool {
	if key == "" || !h.shouldStoreKey(key) {
		return false
	}
	if hash != (chainhash.Hash{}) {
		if _, err := h.Store.GetDKVSRecordByHash(hash); err == nil {
			return false
		}
	}
	return true
}

func (h Handler) send(msg wire.Message) {
	if msg != nil && h.Send != nil {
		h.Send(msg)
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

func (h Handler) OnNotify(msg *wire.MsgDKVSNotify) {
	if !h.valid() || msg == nil || !h.Node.Ready() || !h.allowedIncrementalSource() ||
		!h.needsRecord(msg.Key, msg.RecordHash) {
		return
	}
	get := &wire.MsgDKVSGet{}
	if dkvs.IsTombstone(msg.Flags) && msg.Key != "" {
		get.Keys = []string{msg.Key}
	} else if h.localMiner() && msg.RecordHash != (chainhash.Hash{}) {
		get.RecordHashes = []chainhash.Hash{msg.RecordHash}
	} else if msg.Key != "" {
		get.Keys = []string{msg.Key}
	}
	if len(get.Keys) != 0 || len(get.RecordHashes) != 0 {
		h.Peer.TrackRequest(get, time.Now())
		h.send(get)
	}
}

func (h Handler) OnInv(msg *wire.MsgDKVSInv) {
	if !h.valid() || msg == nil || !h.Node.Ready() || !h.allowedIncrementalSource() {
		return
	}
	get := &wire.MsgDKVSGet{}
	for _, item := range msg.Items {
		if !h.needsRecord(item.Key, item.RecordHash) {
			continue
		}
		if item.Key != "" {
			get.Keys = append(get.Keys, item.Key)
		} else if item.RecordHash != (chainhash.Hash{}) {
			get.RecordHashes = append(get.RecordHashes, item.RecordHash)
		}
	}
	if len(get.Keys) != 0 || len(get.RecordHashes) != 0 {
		h.Peer.TrackRequest(get, time.Now())
		h.send(get)
	}
}

func (h Handler) OnGet(msg *wire.MsgDKVSGet) {
	if !h.valid() || msg == nil || !h.Node.Ready() {
		return
	}
	records := make([]*wire.DKVSRecord, 0)
	notFound := make([]chainhash.Hash, 0)
	seen := make(map[chainhash.Hash]struct{})
	for _, key := range msg.Keys {
		record, err := h.Store.GetDKVSRecordForRelay(key)
		if err != nil {
			notFound = append(notFound, KeyHash(key))
			continue
		}
		hash := dkvs.RecordHash(record)
		if _, ok := seen[hash]; !ok {
			seen[hash] = struct{}{}
			records = append(records, record)
		}
	}
	for _, hash := range msg.RecordHashes {
		record, err := h.Store.GetDKVSRecordByHash(hash)
		if err != nil {
			notFound = append(notFound, hash)
			continue
		}
		recordHash := dkvs.RecordHash(record)
		if _, ok := seen[recordHash]; !ok {
			seen[recordHash] = struct{}{}
			records = append(records, record)
		}
	}
	for _, response := range DataMessages(records, notFound) {
		h.send(response)
	}
}

func (h Handler) OnData(msg *wire.MsgDKVSData) {
	if !h.valid() || msg == nil {
		return
	}
	for _, record := range OrderRecords(msg.Records) {
		if record == nil || !h.Peer.ConsumeRequest(record, time.Now()) {
			h.penalize(0, 1, "unsolicited DKVS data")
			continue
		}
		if !h.shouldStoreKey(record.Key) {
			continue
		}
		updated, err := h.Store.PutRemoteDKVSRecord(record)
		if err != nil {
			h.warnf("reject remote dkvs record %s: %v", record.Key, err)
			continue
		}
		if updated && h.Broadcast != nil {
			h.Broadcast(NotifyForRecord(record))
		}
	}
	h.Peer.ConsumeNotFound(msg.NotFound)
}

func (h Handler) OnSyncRequest(msg *wire.MsgDKVSSyncRequest) {
	if !h.valid() || msg == nil || !h.Node.Ready() {
		return
	}
	if len(msg.Filters) != 0 && !h.MirrorAuthority {
		return
	}
	if len(msg.Filters) == 0 && (h.RemoteServices&wire.SFNodeMiner == 0 || h.ValidatorID == "") {
		h.penalize(0, 10, "unfiltered DKVS sync from non-miner")
		return
	}
	if !h.Peer.BeginServe(msg) {
		h.penalize(0, 5, "invalid DKVS sync session")
		return
	}
	records, next, done, root, err := h.Store.SyncFilteredDKVSRecords(
		msg.Cursor, msg.Limit, SubscriptionsFromFilters(msg.Filters))
	if err != nil {
		h.Peer.CancelServe()
		h.debugf("dkvs sync request failed: %v", err)
		return
	}
	response := &wire.MsgDKVSSyncResponse{
		SessionID: msg.SessionID, Records: records, NextCursor: next, Done: done, CheckpointRoot: root,
	}
	if h.Sign == nil {
		h.Peer.CancelServe()
		return
	}
	response.SourceSignature, err = h.Sign(SyncAuthPayload(h.Net, msg.Cursor, msg.Filters, response))
	if err != nil {
		h.Peer.CancelServe()
		h.debugf("sign dkvs sync response failed: %v", err)
		return
	}
	h.send(response)
	if done {
		for _, pending := range h.Peer.FinishServe(msg.SessionID) {
			h.send(pending)
		}
	}
}

func (h Handler) applyMerge(records []*wire.DKVSRecord) {
	for _, record := range OrderRecords(records) {
		if record == nil || !h.shouldStoreKey(record.Key) {
			continue
		}
		if _, err := h.Store.PutRemoteDKVSRecord(record); err != nil {
			h.warnf("reject synced dkvs record %s: %v", record.Key, err)
		}
	}
}

func (h Handler) OnSyncResponse(msg *wire.MsgDKVSSyncResponse) {
	if !h.valid() || msg == nil {
		return
	}
	action, err := h.Peer.AcceptSyncResponse(msg, func(cursor []byte, filters []wire.DKVSSyncFilter, response *wire.MsgDKVSSyncResponse) bool {
		return h.TrustedSource && VerifySyncSignature(h.Net, h.ValidatorID, cursor, filters, response)
	}, h.shouldStoreKey, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, ErrSyncRootChanged):
			h.QueueSync(nil)
		case errors.Is(err, ErrUnauthenticatedSync):
			h.penalize(20, 0, err.Error())
		case errors.Is(err, ErrSyncStagingLimit), errors.Is(err, ErrSyncConflict):
			h.penalize(0, 20, err.Error())
		case errors.Is(err, ErrSyncCursor):
			h.penalize(0, 10, err.Error())
		default:
			h.penalize(0, 5, err.Error())
		}
		return
	}
	h.applyMerge(action.MergeRecords)
	if !action.Done {
		h.QueueSync(action.NextCursor)
		return
	}
	if action.Mirror {
		if _, err := h.Store.ApplyDKVSMirror(
			SubscriptionsFromFilters(action.Filters), action.MirrorRecords, action.Root,
		); err != nil {
			h.warnf("apply DKVS mirror failed: %v", err)
			return
		}
		h.applyMerge(action.MirrorDeletes)
		h.Node.MarkTrusted(time.Now())
		h.Node.SetReady(true)
		return
	}
	if len(action.BlobRecords) != 0 {
		if _, err := h.Store.ApplyDKVSRecordSet(action.BlobRecords); err != nil {
			h.warnf("apply atomic DKVS blob sync failed: %v", err)
		}
	}
	if h.localMiner() && msg.CheckpointRoot != (chainhash.Hash{}) {
		checkpoint, err := h.Store.GetDKVSCheckpoint()
		if err != nil {
			h.debugf("dkvs checkpoint after sync failed: %v", err)
			return
		}
		if CheckpointRootMismatch(checkpoint.ActiveRecordRoot, msg.CheckpointRoot) {
			h.debugf("dkvs sync checkpoint root mismatch: local=%s remote=%s", checkpoint.ActiveRecordRoot, msg.CheckpointRoot.String())
		}
	}
}

func (h Handler) QueueSync(cursor []byte) {
	if !h.valid() {
		return
	}
	starting := len(cursor) == 0
	localMiner := h.localMiner()
	var filters []wire.DKVSSyncFilter
	if starting && !localMiner {
		filters = FiltersFromSubscriptions(h.Store.ListDKVSSubscriptions())
		if len(filters) == 0 {
			return
		}
	}
	start, err := h.Peer.StartSync(cursor, !localMiner || !h.Node.Ready(), localMiner, filters, time.Now())
	if err != nil || start.Request == nil {
		return
	}
	if start.Started && start.Mirror {
		h.Node.SetReady(false)
	}
	h.send(start.Request)
}

func (h Handler) ShouldRequestSync() bool {
	if !h.valid() {
		return false
	}
	needsMirror := !h.localMiner() || !h.Node.Ready()
	if needsMirror && !h.TrustedSource {
		return false
	}
	return ShouldRequestSync(h.LocalServices, h.RemoteServices, len(h.Store.ListDKVSSubscriptions()))
}
