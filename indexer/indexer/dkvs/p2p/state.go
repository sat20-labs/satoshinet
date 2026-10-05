package p2p

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	RequestTTL          = 2 * time.Minute
	MaxPendingChanges   = 10000
	MaxPendingByteHints = 32 * 1024 * 1024
	MaxMirrorRecords    = 1_000_000
	MaxMirrorBytes      = 512 * 1024 * 1024
	TrustedMirrorGap    = 6 * 24 * time.Hour
	SyncSessionTimeout  = 2 * time.Minute
)

var (
	ErrUnsolicitedSync     = errors.New("unsolicited DKVS sync response")
	ErrUnauthenticatedSync = errors.New("unauthenticated DKVS mirror response")
	ErrSyncRootChanged     = errors.New("DKVS sync root changed")
	ErrSyncCursor          = errors.New("non-progressing DKVS sync cursor")
	ErrSyncStagingLimit    = errors.New("DKVS sync staging limit exceeded")
	ErrSyncConflict        = errors.New("conflicting DKVS sync record")
)

type NodeState struct {
	trustedSeen     int64
	ready           uint32
	pendingMtx      sync.Mutex
	pending         []*wire.MsgDKVSNotify
	pendingBytes    int
	pendingOverflow bool
	// One node-wide queue; the empty path is scope discovery.
	pendingPathSync    []string
	pendingPathSyncSet map[string]struct{}
	syncPeer           *PeerState
	syncPath           string
	draining           bool
}

func (s *NodeState) Ready() bool { return atomic.LoadUint32(&s.ready) != 0 }
func (s *NodeState) SetReady(ready bool) {
	s.pendingMtx.Lock()
	defer s.pendingMtx.Unlock()
	if ready && (s.syncPeer != nil || len(s.pendingPathSyncSet) != 0 || len(s.pending) != 0 || s.pendingOverflow || s.draining) {
		ready = false
	}
	s.setReadyLocked(ready)
}
func (s *NodeState) setReadyLocked(ready bool) {
	var value uint32
	if ready {
		value = 1
	}
	atomic.StoreUint32(&s.ready, value)
}
func (s *NodeState) MarkTrusted(now time.Time) { atomic.StoreInt64(&s.trustedSeen, now.Unix()) }

// The readiness check and enqueue share the same lock as the final drain.
// Returning true also covers overflow: no incomplete realtime suffix is applied.
func (s *NodeState) bufferPendingNotify(msg *wire.MsgDKVSNotify) bool {
	s.pendingMtx.Lock()
	defer s.pendingMtx.Unlock()
	if s.Ready() {
		return false
	}
	byteHint := len(msg.Data) + 16
	if len(s.pending) >= MaxPendingChanges || s.pendingBytes+byteHint > MaxPendingByteHints {
		s.pendingOverflow = true
		return true
	}
	s.pending = append(s.pending, cloneNotify(msg))
	s.pendingBytes += byteHint
	return true
}
func (s *NodeState) ObserveTrusted(now time.Time, localBootstrap bool) bool {
	lastSeen := atomic.LoadInt64(&s.trustedSeen)
	forceMirror := !localBootstrap && s.Ready() && TrustedGapRequiresMirror(lastSeen, now)
	if forceMirror {
		s.SetReady(false)
	}
	s.MarkTrusted(now)
	return forceMirror
}
func TrustedGapRequiresMirror(lastSeen int64, now time.Time) bool {
	return lastSeen > 0 && !now.Before(time.Unix(lastSeen, 0).Add(TrustedMirrorGap))
}
func ShouldRequestSync(localServices, remoteServices wire.ServiceFlag, subscriptionCount int) bool {
	if remoteServices&wire.SFNodeMiner == 0 {
		return false
	}
	return localServices&wire.SFNodeMiner != 0 || subscriptionCount > 0
}
func ShouldRunAntiEntropy(localServices wire.ServiceFlag, subscriptionCount int) bool {
	return localServices&wire.SFNodeMiner != 0 || subscriptionCount > 0
}

type PeerState struct {
	syncLifecycle      peerSyncLifecycle
	requestMtx         sync.Mutex
	requested          map[chainhash.Hash]time.Time
	serveMtx           sync.Mutex
	serveSession       uint64
	serveFilters       []wire.DKVSSyncFilter
	serveActive        bool
	serveSnapshot      *dkvs.PathSnapshot
	notifyFilters      []wire.DKVSSyncFilter
	notifyFiltersKnown bool
	syncMtx            sync.Mutex
	syncSession        uint64
	syncCursor         []byte
	syncFilters        []wire.DKVSSyncFilter
	syncRoot           chainhash.Hash
	syncRootSet        bool
	syncActive         bool
	syncUpdated        time.Time
	syncRecords        map[string]*wire.DKVSRecord
	syncDeletes        map[string]*wire.DKVSRecord
	syncPaths          map[string]struct{}
	syncBytes          uint64
	// Only the last accepted response is retained, as a digest. The ordered
	// connection can deliver the second response to a retried page after the
	// first one advances the cursor. No record bodies or page history are kept.
	syncLastResponse *chainhash.Hash
	// Receiver-local installation fence. Never transmitted as a remote
	// generation or reused by a different sync session.
	syncBaseline *dkvs.ActiveMeta
	syncNode     *NodeState
}

func (s *PeerState) TrackRequest(msg *wire.MsgDKVSGet, now time.Time) {
	if msg == nil || s.Closed() {
		return
	}
	s.requestMtx.Lock()
	if s.requested == nil {
		s.requested = make(map[chainhash.Hash]time.Time)
	}
	for hash, at := range s.requested {
		if !now.Before(at.Add(RequestTTL)) {
			delete(s.requested, hash)
		}
	}
	for _, key := range msg.Keys {
		s.requested[KeyHash(key)] = now
	}
	for _, hash := range msg.RecordHashes {
		s.requested[hash] = now
	}
	s.requestMtx.Unlock()
}
func (s *PeerState) ConsumeRequest(record *wire.DKVSRecord, now time.Time) bool {
	if record == nil || s.Closed() {
		return false
	}
	s.requestMtx.Lock()
	defer s.requestMtx.Unlock()
	matched := false
	for _, hash := range []chainhash.Hash{KeyHash(record.Key), dkvs.RecordHash(record)} {
		at, ok := s.requested[hash]
		if !ok {
			continue
		}
		delete(s.requested, hash)
		if now.Before(at.Add(RequestTTL)) {
			matched = true
		}
	}
	return matched
}
func (s *PeerState) ConsumeNotFound(hashes []chainhash.Hash) {
	s.requestMtx.Lock()
	for _, hash := range hashes {
		delete(s.requested, hash)
	}
	s.requestMtx.Unlock()
}
func filtersEqual(a, b []wire.DKVSSyncFilter) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}
func filtersMatchKey(filters []wire.DKVSSyncFilter, key string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if dkvs.SubscriptionMatchesKey(dkvs.Subscription{Type: dkvs.SubscriptionType(filter.Type), Target: filter.Target}, key) {
			return true
		}
	}
	return false
}
func (s *PeerState) BeginServe(msg *wire.MsgDKVSSyncRequest) bool {
	if msg == nil || msg.SessionID == 0 || s.Closed() {
		return false
	}
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if len(msg.Cursor) == 0 {
		if s.serveActive && s.serveSession == msg.SessionID {
			return filtersEqual(s.serveFilters, msg.Filters)
		}
		s.cancelSync("serve")
		s.serveSession = msg.SessionID
		s.serveFilters = append(s.serveFilters[:0], msg.Filters...)
		s.serveActive = true
		s.serveSnapshot = nil
		return true
	}
	return s.serveActive && s.serveSession == msg.SessionID && filtersEqual(s.serveFilters, msg.Filters)
}
func (s *PeerState) CancelServe() {
	s.serveMtx.Lock()
	s.cancelSync("serve")
	s.serveActive, s.serveSnapshot = false, nil
	s.serveMtx.Unlock()
}
func cloneNotify(msg *wire.MsgDKVSNotify) *wire.MsgDKVSNotify {
	if msg == nil {
		return nil
	}
	copyMsg := *msg
	copyMsg.Data = append([]byte{}, msg.Data...)
	return &copyMsg
}
func (s *PeerState) WantsNotify(msg *wire.MsgDKVSNotify, remoteMiner bool) bool {
	if s.Closed() {
		return false
	}
	record, err := RecordFromNotify(msg)
	if err != nil {
		return false
	}
	// Binding changes are network routing state, not an application
	// subscription. Forward them to every connected node.
	if remoteMiner || dkvs.IsAccountMappingBindingKey(record.Key) {
		return true
	}
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if s.serveActive && filtersMatchKey(s.serveFilters, record.Key) {
		return true
	}
	return s.notifyFiltersKnown && filtersMatchKey(s.notifyFilters, record.Key)
}
func (s *PeerState) FinishServe(session uint64) {
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if !s.serveActive || s.serveSession != session {
		return
	}
	s.cancelSync("serve")
	// A directory snapshot has a temporary serving scope. Only a completed
	// discovery declaration replaces this connection's ongoing subscriptions.
	if _, pathSnapshot := pathSyncFilter(s.serveFilters); !pathSnapshot {
		s.notifyFilters = append(s.notifyFilters[:0], s.serveFilters...)
		s.notifyFiltersKnown = true
	}
	s.serveActive, s.serveSnapshot = false, nil
}
func syncSessionExpired(updated, now time.Time) bool {
	return updated.IsZero() || !now.Before(updated.Add(SyncSessionTimeout))
}

type SyncStart struct {
	Request *wire.MsgDKVSSyncRequest
	Started bool
}

func (s *PeerState) StartSync(cursor []byte, localMiner bool, filters []wire.DKVSSyncFilter, now time.Time) (SyncStart, error) {
	if s.Closed() {
		return SyncStart{}, nil
	}
	starting := len(cursor) == 0
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	if starting {
		if s.syncActive && !syncSessionExpired(s.syncUpdated, now) {
			return SyncStart{}, nil
		}
		session, err := wire.RandomUint64()
		if err != nil || session == 0 {
			return SyncStart{}, err
		}
		if localMiner {
			filters = nil
		}
		s.syncSession = session
		s.syncFilters = append(s.syncFilters[:0], filters...)
		s.syncRecords, s.syncDeletes, s.syncPaths = nil, nil, nil
		s.syncLastResponse = nil
		s.syncBytes, s.syncRoot, s.syncRootSet = 0, chainhash.Hash{}, false
		s.syncActive, s.syncUpdated, s.syncBaseline = true, now, nil
	} else if !s.syncActive {
		return SyncStart{}, nil
	}
	s.syncCursor = append(s.syncCursor[:0], cursor...)
	request := &wire.MsgDKVSSyncRequest{SessionID: s.syncSession, Cursor: cursor, Limit: wire.MaxDKVSRecordsPerMsg}
	if !localMiner {
		request.Filters = append(request.Filters, s.syncFilters...)
	}
	return SyncStart{Request: request, Started: starting}, nil
}

type SyncAction struct {
	Duplicate                    bool
	Retry                        bool
	NextCursor                   []byte
	Done                         bool
	Filters                      []wire.DKVSSyncFilter
	Root                         chainhash.Hash
	MirrorRecords, MirrorDeletes []*wire.DKVSRecord
	Baseline                     *dkvs.ActiveMeta
	DiscoveredPaths              []string
}

type MirrorVerifier func([]byte, []wire.DKVSSyncFilter, *wire.MsgDKVSSyncResponse) bool
type KeyFilter func(string) bool

func (s *PeerState) resetSyncLocked() {
	s.syncActive, s.syncRecords, s.syncDeletes, s.syncBytes, s.syncBaseline = false, nil, nil, 0, nil
	s.syncPaths = nil
	s.syncLastResponse = nil
}
func sortedRecordMap(records map[string]*wire.DKVSRecord) []*wire.DKVSRecord {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make([]*wire.DKVSRecord, 0, len(keys))
	for _, key := range keys {
		ordered = append(ordered, records[key])
	}
	return ordered
}
func (s *PeerState) SetSyncBaseline(session uint64, baseline dkvs.ActiveMeta) bool {
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	if !s.syncActive || s.syncSession != session {
		return false
	}
	copyBaseline := baseline
	copyBaseline.Scope.Keys = append([]string(nil), baseline.Scope.Keys...)
	s.syncBaseline = &copyBaseline
	return true
}
func (s *PeerState) AcceptSyncResponse(msg *wire.MsgDKVSSyncResponse, verify MirrorVerifier, shouldStore KeyFilter, now time.Time, discovery bool) (SyncAction, error) {
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	action := SyncAction{}
	if !s.syncActive || msg == nil || msg.SessionID == 0 || msg.SessionID != s.syncSession {
		return action, ErrUnsolicitedSync
	}
	// Include the signature as well as every signed response field. A message
	// with a changed body or signature must still go through authentication.
	responseHash := chainhash.HashH(appendAuthBytes(SyncAuthPayload(0, nil, nil, msg), msg.SourceSignature))
	if s.syncLastResponse != nil && *s.syncLastResponse == responseHash {
		action.Duplicate = true
		return action, nil
	}
	if verify == nil || !verify(s.syncCursor, s.syncFilters, msg) {
		s.resetSyncLocked()
		return action, ErrUnauthenticatedSync
	}
	if !s.syncRootSet {
		s.syncRoot, s.syncRootSet = msg.CheckpointRoot, true
	} else if !discovery && s.syncRoot != msg.CheckpointRoot {
		s.resetSyncLocked()
		action.Retry = true
		return action, ErrSyncRootChanged
	}
	if !msg.Done && (len(msg.NextCursor) == 0 || bytes.Equal(msg.NextCursor, s.syncCursor)) {
		s.resetSyncLocked()
		return action, ErrSyncCursor
	}
	if shouldStore == nil {
		shouldStore = func(string) bool { return true }
	}
	if discovery {
		// Authenticated inventory only supplies bounded directory hints. Its
		// records are never installed, retained, or used as deletion evidence.
		if s.syncPaths == nil {
			s.syncPaths = make(map[string]struct{})
		}
		for _, record := range msg.Records {
			if record == nil || !shouldStore(record.Key) || dkvs.IsTombstone(record.Flags) {
				continue
			}
			path, err := dkvs.CollectionPathForKey(record.Key)
			if err != nil {
				continue
			}
			if _, err := dkvs.NormalizeActiveScope(dkvs.ActiveScope{Prefix: path, Network: true}); err != nil {
				continue
			}
			if _, exists := s.syncPaths[path]; !exists && len(s.syncPaths) >= MaxPendingChanges {
				s.resetSyncLocked()
				return action, ErrSyncStagingLimit
			}
			s.syncPaths[path] = struct{}{}
		}
	} else {
		if s.syncRecords == nil {
			s.syncRecords, s.syncDeletes = make(map[string]*wire.DKVSRecord), make(map[string]*wire.DKVSRecord)
		}
		for _, record := range msg.Records {
			if record == nil || !shouldStore(record.Key) {
				continue
			}
			recordSize := uint64(wire.DKVSRecordSerializeSize(record))
			if len(s.syncRecords)+len(s.syncDeletes) >= MaxMirrorRecords || s.syncBytes+recordSize > MaxMirrorBytes {
				s.resetSyncLocked()
				return action, ErrSyncStagingLimit
			}
			target := s.syncRecords
			if dkvs.IsTombstone(record.Flags) {
				target = s.syncDeletes
			}
			if previous := target[record.Key]; previous != nil {
				if dkvs.RecordHash(previous) != dkvs.RecordHash(record) {
					s.resetSyncLocked()
					return action, ErrSyncConflict
				}
				continue
			}
			target[record.Key] = record
			s.syncBytes += recordSize
		}
	}
	action.Root, action.Done = s.syncRoot, msg.Done
	action.Filters = append(action.Filters, s.syncFilters...)
	if !msg.Done {
		s.syncLastResponse = &responseHash
		s.syncUpdated = now
		s.syncCursor = append(s.syncCursor[:0], msg.NextCursor...)
		action.NextCursor = append(action.NextCursor, msg.NextCursor...)
		return action, nil
	}
	if discovery {
		for path := range s.syncPaths {
			action.DiscoveredPaths = append(action.DiscoveredPaths, path)
		}
		sort.Strings(action.DiscoveredPaths)
	} else {
		action.MirrorRecords, action.MirrorDeletes = sortedRecordMap(s.syncRecords), sortedRecordMap(s.syncDeletes)
	}
	if s.syncBaseline != nil {
		copyBaseline := *s.syncBaseline
		action.Baseline = &copyBaseline
	}
	s.resetSyncLocked()
	return action, nil
}
