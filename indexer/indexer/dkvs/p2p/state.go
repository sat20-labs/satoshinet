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
	trustedSeen int64
	ready       uint32
}

func (s *NodeState) Ready() bool { return atomic.LoadUint32(&s.ready) != 0 }

func (s *NodeState) SetReady(ready bool) {
	var value uint32
	if ready {
		value = 1
	}
	atomic.StoreUint32(&s.ready, value)
}

func (s *NodeState) MarkTrusted(now time.Time) {
	atomic.StoreInt64(&s.trustedSeen, now.Unix())
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
	if lastSeen <= 0 {
		return false
	}
	return !now.Before(time.Unix(lastSeen, 0).Add(TrustedMirrorGap))
}

func ShouldRequestSync(localServices, remoteServices wire.ServiceFlag, subscriptionCount int) bool {
	if remoteServices&wire.SFNodeMiner == 0 {
		return false
	}
	if localServices&wire.SFNodeMiner != 0 {
		return true
	}
	return subscriptionCount > 0
}

func ShouldRunAntiEntropy(localServices wire.ServiceFlag, subscriptionCount int) bool {
	return localServices&wire.SFNodeMiner != 0 || subscriptionCount > 0
}

type PeerState struct {
	requestMtx sync.Mutex
	requested  map[chainhash.Hash]time.Time

	serveMtx           sync.Mutex
	serveSession       uint64
	serveFilters       []wire.DKVSSyncFilter
	servePending       map[string]*wire.MsgDKVSNotify
	servePendingBytes  int
	serveActive        bool
	notifyFilters      []wire.DKVSSyncFilter
	notifyFiltersKnown bool

	syncMtx     sync.Mutex
	syncSession uint64
	syncCursor  []byte
	syncFilters []wire.DKVSSyncFilter
	syncRoot    chainhash.Hash
	syncRootSet bool
	syncActive  bool
	syncUpdated time.Time
	syncMirror  bool
	syncRecords map[string]*wire.DKVSRecord
	syncDeletes map[string]*wire.DKVSRecord
	syncBytes   uint64

	pendingPathSync    []string
	pendingPathSyncSet map[string]struct{}
}

func (s *PeerState) TrackRequest(msg *wire.MsgDKVSGet, now time.Time) {
	if msg == nil {
		return
	}
	s.requestMtx.Lock()
	if s.requested == nil {
		s.requested = make(map[chainhash.Hash]time.Time)
	}
	for hash, requestedAt := range s.requested {
		if !now.Before(requestedAt.Add(RequestTTL)) {
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
	if record == nil {
		return false
	}
	s.requestMtx.Lock()
	defer s.requestMtx.Unlock()
	matched := false
	for _, hash := range []chainhash.Hash{KeyHash(record.Key), dkvs.RecordHash(record)} {
		requestedAt, ok := s.requested[hash]
		if !ok {
			continue
		}
		delete(s.requested, hash)
		if now.Before(requestedAt.Add(RequestTTL)) {
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
		if dkvs.SubscriptionMatchesKey(dkvs.Subscription{
			Type: dkvs.SubscriptionType(filter.Type), Target: filter.Target,
		}, key) {
			return true
		}
	}
	return false
}

func (s *PeerState) BeginServe(msg *wire.MsgDKVSSyncRequest) bool {
	if msg == nil || msg.SessionID == 0 {
		return false
	}
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if len(msg.Cursor) == 0 {
		s.serveSession = msg.SessionID
		s.serveFilters = append(s.serveFilters[:0], msg.Filters...)
		s.servePending = make(map[string]*wire.MsgDKVSNotify)
		s.servePendingBytes = 0
		s.serveActive = true
		return true
	}
	return s.serveActive && s.serveSession == msg.SessionID && filtersEqual(s.serveFilters, msg.Filters)
}

func (s *PeerState) CancelServe() {
	s.serveMtx.Lock()
	s.serveActive = false
	s.servePending = nil
	s.servePendingBytes = 0
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

func (s *PeerState) BufferNotify(msg *wire.MsgDKVSNotify) bool {
	record, err := RecordFromNotify(msg)
	if err != nil {
		return false
	}
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if !s.serveActive || !filtersMatchKey(s.serveFilters, record.Key) {
		return false
	}
	if previous := s.servePending[record.Key]; previous != nil {
		nextBytes := s.servePendingBytes + len(msg.Data) - len(previous.Data)
		if nextBytes > MaxPendingByteHints {
			return false
		}
		s.servePendingBytes = nextBytes
		s.servePending[record.Key] = cloneNotify(msg)
		return true
	}
	byteHint := len(msg.Data) + 16
	if len(s.servePending) >= MaxPendingChanges || s.servePendingBytes+byteHint > MaxPendingByteHints {
		return false
	}
	s.servePending[record.Key] = cloneNotify(msg)
	s.servePendingBytes += byteHint
	return true
}

func (s *PeerState) WantsNotify(msg *wire.MsgDKVSNotify, remoteMiner bool) bool {
	if remoteMiner {
		return true
	}
	record, err := RecordFromNotify(msg)
	if err != nil {
		return false
	}
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if s.serveActive && filtersMatchKey(s.serveFilters, record.Key) {
		return true
	}
	return s.notifyFiltersKnown && filtersMatchKey(s.notifyFilters, record.Key)
}

func (s *PeerState) FinishServe(session uint64) []*wire.MsgDKVSNotify {
	s.serveMtx.Lock()
	defer s.serveMtx.Unlock()
	if !s.serveActive || s.serveSession != session {
		return nil
	}
	keys := make([]string, 0, len(s.servePending))
	for key := range s.servePending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pending := make([]*wire.MsgDKVSNotify, 0, len(keys))
	for _, key := range keys {
		pending = append(pending, s.servePending[key])
	}
	s.notifyFilters = append(s.notifyFilters[:0], s.serveFilters...)
	s.notifyFiltersKnown = true
	s.serveActive = false
	s.servePending = nil
	s.servePendingBytes = 0
	return pending
}

func syncSessionExpired(updated, now time.Time) bool {
	return updated.IsZero() || !now.Before(updated.Add(SyncSessionTimeout))
}

type SyncStart struct {
	Request *wire.MsgDKVSSyncRequest
	Started bool
	Mirror  bool
}

func (s *PeerState) StartSync(cursor []byte, mirror, localMiner bool, filters []wire.DKVSSyncFilter, now time.Time) (SyncStart, error) {
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
		s.syncMirror = mirror
		if mirror {
			s.syncRecords = make(map[string]*wire.DKVSRecord)
			s.syncDeletes = make(map[string]*wire.DKVSRecord)
		} else {
			s.syncRecords = nil
			s.syncDeletes = nil
		}
		s.syncBytes = 0
		s.syncRoot = chainhash.Hash{}
		s.syncRootSet = false
		s.syncActive = true
		s.syncUpdated = now
	} else if !s.syncActive {
		return SyncStart{}, nil
	}
	s.syncCursor = append(s.syncCursor[:0], cursor...)
	request := &wire.MsgDKVSSyncRequest{
		SessionID: s.syncSession,
		Cursor:    cursor,
		Limit:     wire.MaxDKVSRecordsPerMsg,
	}
	if !localMiner {
		request.Filters = append(request.Filters, s.syncFilters...)
	}
	return SyncStart{Request: request, Started: starting, Mirror: s.syncMirror}, nil
}

type SyncAction struct {
	Retry         bool
	NextCursor    []byte
	Done          bool
	Mirror        bool
	Filters       []wire.DKVSSyncFilter
	Root          chainhash.Hash
	MergeRecords  []*wire.DKVSRecord
	MirrorRecords []*wire.DKVSRecord
	MirrorDeletes []*wire.DKVSRecord
}

type MirrorVerifier func(requestCursor []byte, filters []wire.DKVSSyncFilter, msg *wire.MsgDKVSSyncResponse) bool
type KeyFilter func(key string) bool

func (s *PeerState) resetSyncLocked() {
	s.syncActive = false
	s.syncRecords = nil
	s.syncDeletes = nil
	s.syncBytes = 0
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

func (s *PeerState) AcceptSyncResponse(msg *wire.MsgDKVSSyncResponse, verify MirrorVerifier, shouldStore KeyFilter, now time.Time) (SyncAction, error) {
	s.syncMtx.Lock()
	defer s.syncMtx.Unlock()
	action := SyncAction{}
	if !s.syncActive || msg == nil || msg.SessionID == 0 || msg.SessionID != s.syncSession {
		return action, ErrUnsolicitedSync
	}
	if s.syncMirror && (verify == nil || !verify(s.syncCursor, s.syncFilters, msg)) {
		s.resetSyncLocked()
		return action, ErrUnauthenticatedSync
	}
	if !s.syncRootSet {
		s.syncRoot = msg.CheckpointRoot
		s.syncRootSet = true
	} else if s.syncRoot != msg.CheckpointRoot {
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
	if s.syncMirror {
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
	} else {
		for _, record := range msg.Records {
			if record != nil && shouldStore(record.Key) {
				action.MergeRecords = append(action.MergeRecords, record)
			}
		}
	}
	action.Mirror = s.syncMirror
	action.Filters = append(action.Filters, s.syncFilters...)
	action.Root = s.syncRoot
	action.Done = msg.Done
	if !msg.Done {
		s.syncUpdated = now
		action.NextCursor = append(action.NextCursor, msg.NextCursor...)
		return action, nil
	}
	if s.syncMirror {
		action.MirrorRecords = sortedRecordMap(s.syncRecords)
		action.MirrorDeletes = sortedRecordMap(s.syncDeletes)
	}
	s.resetSyncLocked()
	return action, nil
}
