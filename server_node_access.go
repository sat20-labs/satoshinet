package main

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"time"

	common "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

type accessTiming struct {
	ping, idlePing, expiry, selectionWait time.Duration
}

var accessTimers = nodeAccessTiming(accessTiming{
	ping: 20 * time.Second, idlePing: 2 * time.Minute,
	expiry: 60 * time.Second, selectionWait: 10 * time.Minute,
})

var accessCoreKey = []byte("preferred-core")

// Owned by peerHandler. Preference only controls ping metadata, not connections.
// The saved public key and observed address survive a restart.
type nodeAccessState struct {
	nodeID      uint64
	coreKey     string
	coreAddr    string
	selected    *serverPeer
	selectAfter time.Time
}

type accessPingReply struct {
	message  *wire.MsgPing
	interval time.Duration
}

type newAccessPingMsg struct {
	peer  *serverPeer
	nonce uint64
	reply chan accessPingReply
}

type recvAccessPingMsg struct {
	peer *serverPeer
	ping wire.MsgPing
}

type nodeStatsMsg struct {
	reply chan btcjson.GetNodeStatsResult
}

func (s *server) accessType(key string) int {
	if key == "" || s.assetIndexer == nil {
		return common.NODE_TYPE_NORMAL
	}
	seq := s.assetIndexer.GetSeqMgr()
	if seq == nil {
		return common.NODE_TYPE_NORMAL
	}
	return seq.GetNodeType(key)
}

func (s *server) autoAccess() bool {
	return s.miningPubKey == "" && !cfg.SimNet
}

func (s *server) initNodeAccess() error {
	id, err := wire.RandomUint64()
	if err != nil {
		return err
	}
	for id == 0 {
		id, err = wire.RandomUint64()
		if err != nil {
			return err
		}
	}
	s.access.nodeID = id
	if !s.autoAccess() {
		return nil
	}
	return s.db.View(func(tx database.Tx) error {
		data := tx.Metadata().Get(accessCoreKey)
		if len(data) == 0 {
			return nil
		}
		if len(data) <= 33 {
			return fmt.Errorf("invalid preferred CoreNode record")
		}
		addr := string(data[33:])
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("invalid preferred CoreNode address: %w", err)
		}
		s.access.coreKey = hex.EncodeToString(data[:33])
		s.access.coreAddr = addr
		s.access.selectAfter = time.Now().Add(accessTimers.selectionWait)
		return nil
	})
}

func (s *server) saveAccessCore(sp *serverPeer) error {
	key, addr := sp.ValidatorId(), sp.Addr()
	if key == s.access.coreKey && addr == s.access.coreAddr {
		return nil
	}
	pub, err := hex.DecodeString(key)
	if err != nil || len(pub) != 33 {
		return fmt.Errorf("invalid access CoreNode key %q", key)
	}
	data := append(pub, []byte(addr)...)
	if err := s.db.Update(func(tx database.Tx) error {
		return tx.Metadata().Put(accessCoreKey, data)
	}); err != nil {
		return err
	}
	s.access.coreKey, s.access.coreAddr = key, addr
	return nil
}

func (s *server) forgetAccessCore() error {
	if err := s.db.Update(func(tx database.Tx) error {
		return tx.Metadata().Delete(accessCoreKey)
	}); err != nil {
		return err
	}
	s.access.selected = nil
	s.access.coreKey, s.access.coreAddr = "", ""
	s.access.selectAfter = time.Time{}
	return nil
}

// Selection stays sticky. Ordinary nodes only persist outbound endpoints:
// an inbound TCP source port is not a CoreNode's listening port.
func (s *server) updateNodeAccess(state *peerState, now time.Time) {
	// Membership loss is definitive, unlike a maintenance outage. Drop the
	// preference without changing any peer connections.
	if selected := s.access.selected; selected != nil && s.autoAccess() &&
		s.accessType(selected.ValidatorId()) != common.NODE_TYPE_CORE {
		if err := s.forgetAccessCore(); err != nil {
			srvrLog.Errorf("Forget withdrawn CoreNode: %v", err)
			return
		}
		selected.RequestPing()
	}
	if selected := s.access.selected; selected != nil {
		if selected.Connected() && now.Sub(selected.accessSeen) < accessTimers.expiry {
			return
		}
		s.access.selected = nil
		selected.RequestPing()
		if s.autoAccess() {
			s.access.selectAfter = now.Add(accessTimers.selectionWait)
		}
	}

	parent := cfg.ServerPubKey
	localType := s.accessType(s.miningPubKey)
	if localType == common.NODE_TYPE_BOOTSTRAP {
		return
	}
	if localType == common.NODE_TYPE_CORE {
		parent = common.GetBootstrapPubKey()
	} else if s.miningPubKey != "" && parent == "" {
		if seq := s.assetIndexer.GetSeqMgr(); seq != nil {
			if info := seq.GetMiningInfo(s.miningPubKey); info != nil && info.Father != nil {
				parent = info.Father.PubKey
			}
		}
	}
	if !s.autoAccess() && parent == "" {
		return
	}
	var candidates []*serverPeer
	state.forAllPeers(func(sp *serverPeer) {
		// Idle backup connections ping less often. Wait for a fresh report
		// before selecting one, without disconnecting it or changing retries.
		if !sp.Connected() || sp.ValidatorId() == s.miningPubKey ||
			sp.accessSeen.IsZero() || now.Sub(sp.accessSeen) >= accessTimers.expiry {
			return
		}
		if parent != "" {
			if sp.ValidatorId() == parent {
				candidates = append(candidates, sp)
			}
			return
		}
		if sp.Inbound() || s.accessType(sp.ValidatorId()) != common.NODE_TYPE_CORE {
			return
		}
		if now.Before(s.access.selectAfter) && sp.ValidatorId() != s.access.coreKey {
			return
		}
		candidates = append(candidates, sp)
	})
	if len(candidates) == 0 {
		return
	}
	selected := candidates[rand.Intn(len(candidates))]
	if s.autoAccess() {
		if err := s.saveAccessCore(selected); err != nil {
			srvrLog.Errorf("Save preferred CoreNode: %v", err)
			return
		}
		s.access.selectAfter = time.Time{}
	}
	s.access.selected = selected
	selected.RequestPing()
	srvrLog.Infof("Selected access CoreNode %s at %s", selected.ValidatorId(), selected.Addr())
}

func (s *server) accessChildCount(state *peerState, now time.Time) uint32 {
	localType := s.accessType(s.miningPubKey)
	if localType != common.NODE_TYPE_CORE && localType != common.NODE_TYPE_BOOTSTRAP {
		return 0
	}
	ids := make(map[uint64]struct{})
	state.forAllPeers(func(sp *serverPeer) {
		if !sp.Connected() || !sp.accessUse || sp.accessNodeID == 0 || now.Sub(sp.accessSeen) >= accessTimers.expiry {
			return
		}
		typ := s.accessType(sp.ValidatorId())
		if typ != common.NODE_TYPE_CORE && typ != common.NODE_TYPE_BOOTSTRAP {
			ids[sp.accessNodeID] = struct{}{}
		}
	})
	return uint32(len(ids))
}

func (sp *serverPeer) newAccessPing(nonce uint64) (*wire.MsgPing, time.Duration) {
	reply := make(chan accessPingReply, 1)
	select {
	case sp.server.query <- newAccessPingMsg{peer: sp, nonce: nonce, reply: reply}:
	case <-sp.server.quit:
		return wire.NewMsgPing(nonce), accessTimers.ping
	}
	select {
	case result := <-reply:
		return result.message, result.interval
	case <-sp.server.quit:
		return wire.NewMsgPing(nonce), accessTimers.ping
	}
}

func (s *server) handleAccessPing(msg recvAccessPingMsg) {
	sp := msg.peer
	changed := sp.accessUse != msg.ping.UseAsCore
	sp.accessNodeID = msg.ping.NodeID
	sp.accessUse = msg.ping.UseAsCore
	sp.accessCount = msg.ping.ChildCount
	sp.accessSeen = time.Now()
	if changed {
		sp.RequestPing()
		if s.access.selected != nil && s.access.selected != sp {
			s.access.selected.RequestPing()
		}
	}
}

func (s *server) accessSnapshot(state *peerState) btcjson.GetNodeStatsResult {
	now := time.Now()
	result := btcjson.GetNodeStatsResult{
		NodeID:        fmt.Sprintf("%016x", s.access.nodeID),
		PreferredCore: s.access.coreKey, PreferredAddr: s.access.coreAddr,
		ChildCount: s.accessChildCount(state, now), WindowSeconds: int64(accessTimers.expiry / time.Second),
		IsBootstrap: s.accessType(s.miningPubKey) == common.NODE_TYPE_BOOTSTRAP,
	}
	if sp := s.access.selected; sp != nil && sp.Connected() {
		result.AccessCore = sp.ValidatorId()
	}
	if !result.IsBootstrap {
		return result
	}
	cores := make(map[string]*serverPeer)
	state.forAllPeers(func(sp *serverPeer) {
		if !sp.Connected() || sp.accessNodeID == 0 || now.Sub(sp.accessSeen) >= accessTimers.expiry ||
			s.accessType(sp.ValidatorId()) != common.NODE_TYPE_CORE {
			return
		}
		previous := cores[sp.ValidatorId()]
		if previous == nil || sp.accessSeen.After(previous.accessSeen) {
			cores[sp.ValidatorId()] = sp
		}
	})
	result.CoreCount = uint32(len(cores))
	result.ObservedNodes = 1 + uint64(result.ChildCount) + uint64(result.CoreCount)
	for _, sp := range cores {
		result.ObservedNodes += uint64(sp.accessCount)
	}
	return result
}

func (s *server) NodeStats() btcjson.GetNodeStatsResult {
	reply := make(chan btcjson.GetNodeStatsResult, 1)
	select {
	case s.query <- nodeStatsMsg{reply: reply}:
	case <-s.quit:
		return btcjson.GetNodeStatsResult{}
	}
	select {
	case result := <-reply:
		return result
	case <-s.quit:
		return btcjson.GetNodeStatsResult{}
	}
}
