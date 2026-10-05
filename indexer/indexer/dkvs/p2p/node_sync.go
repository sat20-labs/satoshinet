package p2p

import (
	"time"
)

func (s *NodeState) enqueuePathSyncLocked(path string) {
	if s.pendingPathSyncSet == nil {
		s.pendingPathSyncSet = make(map[string]struct{})
	}
	if _, exists := s.pendingPathSyncSet[path]; exists {
		return
	}
	if len(s.pendingPathSync) >= MaxPendingChanges {
		// Rediscover the full set rather than losing an unqueued repair hint.
		s.pendingOverflow = true
		s.setReadyLocked(false)
		return
	}
	s.pendingPathSync = append(s.pendingPathSync, path)
	s.pendingPathSyncSet[path] = struct{}{}
	s.setReadyLocked(false)
}

// A directory remains outstanding until its complete snapshot is installed.
// Failed transfers and closed sources put it back in the same node-wide queue.
func (s *NodeState) finishSync(peer *PeerState, success bool) bool {
	s.pendingMtx.Lock()
	defer s.pendingMtx.Unlock()
	if s.syncPeer != peer {
		return false
	}
	if success {
		delete(s.pendingPathSyncSet, s.syncPath)
	} else {
		s.pendingPathSync = append(s.pendingPathSync, s.syncPath)
	}
	s.syncPeer, s.syncPath = nil, ""
	return true
}

func (h Handler) queueNextNodeSync(skip string, skipFailed bool) bool {
	if !h.valid() || !h.allowedPathSnapshotSource() || h.Peer.Closed() {
		return false
	}
	s := h.Node
	s.pendingMtx.Lock()
	if s.syncPeer != nil || s.draining {
		s.pendingMtx.Unlock()
		return false
	}
	index := -1
	for n, path := range s.pendingPathSync {
		if !skipFailed || path != skip {
			index = n
			break
		}
	}
	if index < 0 {
		s.pendingMtx.Unlock()
		return false
	}
	path := s.pendingPathSync[index]
	s.pendingPathSync = append(s.pendingPathSync[:index], s.pendingPathSync[index+1:]...)
	s.syncPeer, s.syncPath = h.Peer, path
	s.setReadyLocked(false)
	h.Peer.syncMtx.Lock()
	h.Peer.syncNode = s
	h.Peer.syncMtx.Unlock()
	var start SyncStart
	var err error
	if path == "" {
		start, err = h.Peer.StartSync(nil, h.localMiner(), FiltersFromSubscriptions(h.Store.ListDKVSSubscriptions()), time.Now())
	} else {
		start, err = h.Peer.StartPathSync(path, time.Now())
	}
	if err != nil || start.Request == nil {
		s.pendingPathSync = append(s.pendingPathSync, path)
		s.syncPeer, s.syncPath = nil, ""
		s.pendingMtx.Unlock()
		return false
	}
	s.pendingMtx.Unlock()
	h.sendPathSyncRequest(start.Request)
	return true
}

// Keep READY false throughout replay. New notifications join the same queue;
// the empty-queue check and transition to realtime use the ingress mutex.
func (h Handler) markReadyAndDrain() {
	if !h.valid() {
		return
	}
	s := h.Node
	s.pendingMtx.Lock()
	if s.syncPeer != nil || len(s.pendingPathSyncSet) != 0 || s.draining {
		s.pendingMtx.Unlock()
		return
	}
	s.draining = true
	s.setReadyLocked(false)
	s.pendingMtx.Unlock()
	for {
		s.pendingMtx.Lock()
		if s.syncPeer != nil || len(s.pendingPathSyncSet) != 0 {
			s.draining = false
			s.pendingMtx.Unlock()
			h.queueNextPathSync()
			return
		}
		if s.pendingOverflow {
			s.pending, s.pendingBytes, s.pendingOverflow = nil, 0, false
			s.enqueuePathSyncLocked("")
			s.draining = false
			s.pendingMtx.Unlock()
			h.warnf("DKVS notify cache overflowed; synchronize all directories again")
			h.queueNextPathSync()
			return
		}
		if len(s.pending) == 0 {
			s.draining = false
			s.MarkTrusted(time.Now())
			s.setReadyLocked(true)
			s.pendingMtx.Unlock()
			return
		}
		message := s.pending[0]
		s.pendingMtx.Unlock()
		record, err := RecordFromNotify(message)
		if err == nil {
			h.applyNotifyRecord(record)
		}
		s.pendingMtx.Lock()
		s.pending[0] = nil
		s.pending = s.pending[1:]
		s.pendingBytes -= len(message.Data) + 16
		s.pendingMtx.Unlock()
	}
}

func (s *NodeState) ownsSync(peer *PeerState) bool {
	s.pendingMtx.Lock()
	defer s.pendingMtx.Unlock()
	return s.syncPeer == peer
}
