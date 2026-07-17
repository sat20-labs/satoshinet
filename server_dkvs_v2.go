package main

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func (sp *serverPeer) needsDKVSRecordV2(key string, hash chainhash.Hash) bool {
	if key == "" || !sp.shouldFetchDKVSKey(key) {
		return false
	}
	if hash != (chainhash.Hash{}) {
		if _, err := sp.server.assetIndexer.GetDKVSRecordByHashForSync(hash); err == nil {
			return false
		}
	}
	return true
}

func (sp *serverPeer) onDKVSGetV2(msg *wire.MsgDKVSGet) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	records := make([]*wire.DKVSRecord, 0)
	notFound := make([]chainhash.Hash, 0)
	seen := make(map[chainhash.Hash]struct{})
	for _, key := range msg.Keys {
		record, err := sp.server.assetIndexer.GetDKVSRecordForSync(key)
		if err != nil {
			notFound = append(notFound, dkvsKeyHash(key))
			continue
		}
		hash := dkvsindexer.RecordHash(record)
		if _, ok := seen[hash]; !ok {
			seen[hash] = struct{}{}
			records = append(records, record)
		}
	}
	for _, hash := range msg.RecordHashes {
		record, err := sp.server.assetIndexer.GetDKVSRecordByHashForSync(hash)
		if err != nil {
			notFound = append(notFound, hash)
			continue
		}
		recordHash := dkvsindexer.RecordHash(record)
		if _, ok := seen[recordHash]; !ok {
			seen[recordHash] = struct{}{}
			records = append(records, record)
		}
	}
	sp.queueDKVSData(records, notFound)
}

func (sp *serverPeer) onDKVSDataV2(msg *wire.MsgDKVSData) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	for _, record := range msg.Records {
		if record == nil {
			continue
		}
		sp.markDKVSMirrorKey(record.Key)
		if !sp.shouldStoreDKVSKey(record.Key) {
			continue
		}
		updated, err := sp.server.assetIndexer.PutRemoteDKVSRecord(record)
		if err != nil {
			peerLog.Warnf("reject remote dkvs record %s from %s: %v", record.Key, sp, err)
			continue
		}
		if updated {
			sp.relayDKVSRecord(record)
		}
	}
}

func (sp *serverPeer) onDKVSSyncRequestV2(msg *wire.MsgDKVSSyncRequest) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	if len(msg.Filters) == 0 && (sp.Services()&wire.SFNodeMiner == 0 || sp.ValidatorId() == "") {
		sp.addBanScore(0, 10, "unfiltered DKVS sync from non-miner")
		return
	}

	filters := dkvsSubscriptionsFromWireFilters(msg.Filters)
	tracked := len(filters) <= 1
	var sub dkvsindexer.Subscription
	if len(filters) == 1 {
		sub = filters[0]
	}
	token := sp.dkvsServeSyncToken(msg.SessionID)
	if tracked && len(msg.Cursor) == 0 {
		if err := sp.server.assetIndexer.BeginDKVSPathSync(token, sub); err != nil {
			peerLog.Debugf("begin dkvs path sync from %s failed: %v", sp, err)
			return
		}
	}

	records, next, done, root, err := sp.server.assetIndexer.SyncFilteredDKVSRecords(
		msg.Cursor, msg.Limit, filters)
	if err != nil {
		if tracked {
			sp.server.assetIndexer.CancelDKVSPathSync(token)
		}
		peerLog.Debugf("dkvs sync request from %s failed: %v", sp, err)
		return
	}
	if done && tracked {
		pending, overflow := sp.server.assetIndexer.EndDKVSPathSync(token)
		if overflow {
			peerLog.Warnf("dkvs path sync pending buffer overflow for %s session=%d", sp, msg.SessionID)
			return
		}
		// Queue the coalesced concurrent changes before the final response. The
		// receiver therefore applies them before authoritative mirror cleanup.
		if len(pending) != 0 {
			sp.queueDKVSData(pending, nil)
		}
	}
	sp.QueueMessage(&wire.MsgDKVSSyncResponse{
		SessionID:      msg.SessionID,
		Records:        records,
		NextCursor:     next,
		Done:           done,
		CheckpointRoot: root,
	}, nil)
}

func (sp *serverPeer) onDKVSSyncResponseV2(msg *wire.MsgDKVSSyncResponse) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	sp.dkvsSyncMtx.Lock()
	if !sp.dkvsSyncActive || msg.SessionID == 0 || msg.SessionID != sp.dkvsSyncSession {
		sp.dkvsSyncMtx.Unlock()
		sp.addBanScore(0, 5, "unsolicited DKVS sync response")
		return
	}
	if !sp.dkvsSyncRootSet {
		sp.dkvsSyncRoot = msg.CheckpointRoot
		sp.dkvsSyncRootSet = true
	} else if sp.isLocalMiner() && sp.dkvsSyncRoot != msg.CheckpointRoot {
		sp.dkvsSyncActive = false
		sp.dkvsSyncMtx.Unlock()
		sp.abortDKVSMirrorSync()
		sp.queueDKVSSyncRequest(nil)
		return
	}
	if !msg.Done && (len(msg.NextCursor) == 0 || bytes.Equal(msg.NextCursor, sp.dkvsSyncCursor)) {
		sp.dkvsSyncActive = false
		sp.dkvsSyncMtx.Unlock()
		sp.abortDKVSMirrorSync()
		sp.addBanScore(0, 10, "non-progressing DKVS sync cursor")
		return
	}
	if msg.Done {
		sp.dkvsSyncActive = false
	} else {
		sp.dkvsSyncUpdated = time.Now()
	}
	sp.dkvsSyncMtx.Unlock()

	for _, record := range msg.Records {
		if record == nil {
			continue
		}
		sp.markDKVSMirrorKey(record.Key)
		if !sp.shouldStoreDKVSKey(record.Key) {
			continue
		}
		if _, err := sp.server.assetIndexer.PutRemoteDKVSRecord(record); err != nil {
			peerLog.Warnf("reject synced dkvs record %s from %s: %v", record.Key, sp, err)
		}
	}
	if !msg.Done {
		sp.queueDKVSSyncRequest(msg.NextCursor)
		return
	}

	sp.finishDKVSMirrorSync()
	if sp.advanceDKVSSyncPath() {
		sp.queueDKVSSyncRequest(nil)
		return
	}
	if sp.isLocalMiner() && msg.CheckpointRoot != (chainhash.Hash{}) {
		checkpoint, err := sp.server.assetIndexer.GetDKVSCheckpoint()
		if err != nil {
			peerLog.Debugf("dkvs checkpoint after sync from %s failed: %v", sp, err)
			return
		}
		if dkvsCheckpointRootMismatch(checkpoint.ActiveRecordRoot, msg.CheckpointRoot) {
			peerLog.Debugf("dkvs sync checkpoint root mismatch from %s: local=%s remote=%s", sp, checkpoint.ActiveRecordRoot, msg.CheckpointRoot.String())
		}
	}
}

func (sp *serverPeer) queueDKVSSyncRequestV2(cursor []byte) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil {
		return
	}
	var (
		prepareMirror bool
		session       uint64
		filter        wire.DKVSSyncFilter
		msg           *wire.MsgDKVSSyncRequest
	)

	sp.dkvsSyncMtx.Lock()
	if len(cursor) == 0 {
		if sp.dkvsSyncActive && !dkvsSyncSessionExpired(sp.dkvsSyncUpdated, time.Now(), 2*time.Minute) {
			sp.dkvsSyncMtx.Unlock()
			return
		}
		if !sp.isLocalMiner() {
			if len(sp.dkvsSyncFilters) == 0 || sp.dkvsSyncFilterIndex >= len(sp.dkvsSyncFilters) {
				sp.dkvsSyncFilters = dkvsWireFiltersFromSubscriptions(sp.server.assetIndexer.ListDKVSSubscriptions())
				sp.dkvsSyncFilterIndex = 0
			}
			if len(sp.dkvsSyncFilters) == 0 {
				sp.dkvsSyncMtx.Unlock()
				return
			}
			sp.dkvsSyncCurrentFilter = sp.dkvsSyncFilters[sp.dkvsSyncFilterIndex]
		} else {
			sp.dkvsSyncCurrentFilter = wire.DKVSSyncFilter{}
		}
		newSession, err := wire.RandomUint64()
		if err != nil || newSession == 0 {
			sp.dkvsSyncMtx.Unlock()
			return
		}
		sp.dkvsSyncSession = newSession
		sp.dkvsSyncRoot = chainhash.Hash{}
		sp.dkvsSyncRootSet = false
		sp.dkvsSyncActive = true
		sp.dkvsSyncUpdated = time.Now()
		sp.dkvsSyncMirrorKeys = nil
		prepareMirror = !sp.isLocalMiner()
	} else if !sp.dkvsSyncActive {
		sp.dkvsSyncMtx.Unlock()
		return
	}
	sp.dkvsSyncCursor = append(sp.dkvsSyncCursor[:0], cursor...)
	msg = sp.dkvsSyncRequestV2(cursor)
	msg.SessionID = sp.dkvsSyncSession
	session = sp.dkvsSyncSession
	filter = sp.dkvsSyncCurrentFilter
	sp.dkvsSyncMtx.Unlock()

	if prepareMirror {
		if err := sp.prepareDKVSMirrorSync(session, filter); err != nil {
			peerLog.Debugf("prepare dkvs mirror sync from %s failed: %v", sp, err)
			sp.dkvsSyncMtx.Lock()
			if sp.dkvsSyncSession == session {
				sp.dkvsSyncActive = false
				sp.dkvsSyncMirrorKeys = nil
			}
			sp.dkvsSyncMtx.Unlock()
			return
		}
	}
	sp.QueueMessage(msg, nil)
}

func (sp *serverPeer) dkvsSyncRequestV2(cursor []byte) *wire.MsgDKVSSyncRequest {
	msg := &wire.MsgDKVSSyncRequest{
		Cursor: cursor,
		Limit:  wire.MaxDKVSRecordsPerMsg,
	}
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || sp.isLocalMiner() {
		return msg
	}
	if sp.dkvsSyncCurrentFilter.Type != "" && sp.dkvsSyncCurrentFilter.Target != "" {
		msg.Filters = []wire.DKVSSyncFilter{sp.dkvsSyncCurrentFilter}
	}
	return msg
}

func (sp *serverPeer) dkvsServeSyncToken(sessionID uint64) string {
	return fmt.Sprintf("%p:%d", sp, sessionID)
}

func (sp *serverPeer) prepareDKVSMirrorSync(session uint64, filter wire.DKVSSyncFilter) error {
	if sp.isLocalMiner() || sp.Services()&wire.SFNodeMiner != wire.SFNodeMiner {
		return nil
	}
	sub := dkvsindexer.Subscription{
		Type:   dkvsindexer.SubscriptionType(filter.Type),
		Target: filter.Target,
	}
	keys, err := sp.server.assetIndexer.ListDKVSKeysForSync(sub)
	if err != nil {
		return err
	}
	remaining := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		remaining[key] = struct{}{}
	}
	sp.dkvsSyncMtx.Lock()
	if sp.dkvsSyncSession == session && sp.dkvsSyncActive {
		sp.dkvsSyncMirrorKeys = remaining
	}
	sp.dkvsSyncMtx.Unlock()
	return nil
}

func (sp *serverPeer) markDKVSMirrorKey(key string) {
	sp.dkvsSyncMtx.Lock()
	defer sp.dkvsSyncMtx.Unlock()
	if sp.dkvsSyncMirrorKeys == nil || !dkvsSyncFilterMatchesKey(sp.dkvsSyncCurrentFilter, key) {
		return
	}
	delete(sp.dkvsSyncMirrorKeys, key)
}

func (sp *serverPeer) finishDKVSMirrorSync() {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || sp.isLocalMiner() {
		return
	}
	sp.dkvsSyncMtx.Lock()
	filter := sp.dkvsSyncCurrentFilter
	keys := make([]string, 0, len(sp.dkvsSyncMirrorKeys))
	for key := range sp.dkvsSyncMirrorKeys {
		keys = append(keys, key)
	}
	sp.dkvsSyncMirrorKeys = nil
	sp.dkvsSyncMtx.Unlock()
	if len(keys) == 0 || filter.Type == "" {
		return
	}
	sub := dkvsindexer.Subscription{
		Type:   dkvsindexer.SubscriptionType(filter.Type),
		Target: filter.Target,
	}
	if _, err := sp.server.assetIndexer.DeleteDKVSKeysForMirror(sub, keys); err != nil {
		peerLog.Warnf("delete missing dkvs mirror keys from %s failed: %v", sp, err)
	}
}

func (sp *serverPeer) abortDKVSMirrorSync() {
	sp.dkvsSyncMtx.Lock()
	sp.dkvsSyncMirrorKeys = nil
	sp.dkvsSyncMtx.Unlock()
}

func (sp *serverPeer) advanceDKVSSyncPath() bool {
	sp.dkvsSyncMtx.Lock()
	defer sp.dkvsSyncMtx.Unlock()
	if sp.isLocalMiner() {
		sp.dkvsSyncFilters = nil
		sp.dkvsSyncFilterIndex = 0
		sp.dkvsSyncCurrentFilter = wire.DKVSSyncFilter{}
		return false
	}
	sp.dkvsSyncFilterIndex++
	if sp.dkvsSyncFilterIndex < len(sp.dkvsSyncFilters) {
		sp.dkvsSyncCurrentFilter = sp.dkvsSyncFilters[sp.dkvsSyncFilterIndex]
		return true
	}
	sp.dkvsSyncFilters = nil
	sp.dkvsSyncFilterIndex = 0
	sp.dkvsSyncCurrentFilter = wire.DKVSSyncFilter{}
	return false
}

func dkvsSyncFilterMatchesKey(filter wire.DKVSSyncFilter, key string) bool {
	target := strings.TrimSuffix(strings.TrimSpace(filter.Target), "/")
	switch dkvsindexer.SubscriptionType(strings.ToLower(strings.TrimSpace(filter.Type))) {
	case dkvsindexer.SubscriptionKey:
		return key == target
	case dkvsindexer.SubscriptionPrefix, dkvsindexer.SubscriptionMailbox, dkvsindexer.SubscriptionService:
		return key == target || strings.HasPrefix(key, target+"/")
	default:
		return false
	}
}
