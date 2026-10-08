package main

import (
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type routeMiningIndexer struct {
	shareindexer.Indexer
	seq *scommon.MiningSequenceMgr
}

func (i *routeMiningIndexer) GetSeqMgr() *scommon.MiningSequenceMgr { return i.seq }

func connectedMinerRoute(t *testing.T, validator string) *serverPeer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	verack := make(chan struct{}, 1)
	in := peer.NewInboundPeer(&peer.Config{ChainParams: &chaincfg.TestNetParams, AllowSelfConns: true,
		Listeners: peer.MessageListeners{OnVerAck: func(*peer.Peer, *wire.MsgVerAck) { verack <- struct{}{} }}})
	out, err := peer.NewOutboundPeer(&peer.Config{ChainParams: &chaincfg.TestNetParams, AllowSelfConns: true,
		ValidatorId: validator}, listener.Addr().String())
	require.NoError(t, err)
	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	in.AssociateConnection(<-accepted)
	out.AssociateConnection(conn)
	t.Cleanup(func() { in.Disconnect(); out.Disconnect(); in.WaitForDisconnect(); out.WaitForDisconnect() })
	select {
	case <-verack:
	case <-time.After(3 * time.Second):
		t.Fatal("peer handshake timed out")
	}
	require.Equal(t, validator, in.ValidatorId())
	require.True(t, in.Connected())
	return &serverPeer{Peer: in}
}

func TestMinerRouteKeepsSurvivingConnection(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes([]byte{73})
	validator := hex.EncodeToString(key.PubKey().SerializeCompressed())
	params := chaincfg.TestNetParams
	params.POSV2Height = 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
	seq := scommon.NewMiningSequenceMgr(&params)
	require.NoError(t, seq.Init(map[string]*scommon.CoreNodeInfo{validator: scommon.NewCoreNodeInfo(nil)}, 0, ""))
	previous := shareindexer.ShareIndexer
	shareindexer.ShareIndexer = &routeMiningIndexer{seq: seq}
	t.Cleanup(func() { shareindexer.ShareIndexer = previous })
	for _, first := range []string{"old-A", "new-B", "manual-old-A", "manual-new-B"} {
		t.Run(first, func(t *testing.T) {
			a, b := connectedMinerRoute(t, validator), connectedMinerRoute(t, validator)
			state := &peerState{inboundPeers: map[int32]*serverPeer{a.ID(): a, b.ID(): b},
				minerPeers: map[string]*serverPeer{validator: a}}
			state.minerPeers[validator] = b // Same replacement as handleAddPeerMsg.
			exit, remaining := a, b
			if first == "new-B" || first == "manual-new-B" {
				exit, remaining = b, a
			}
			s := &server{}
			if first == "manual-old-A" || first == "manual-new-B" {
				require.True(t, disconnectPeer(state, state.inboundPeers, func(p *serverPeer) bool { return p == exit }, nil))
			} else {
				exit.Disconnect()
			}
			s.handleDonePeerMsg(state, exit)
			require.Same(t, remaining, state.minerPeers[validator])
			for _, id := range []string{validator, ""} {
				reply := make(chan *peer.Peer, 1)
				s.handleQuery(state, getPeerByValidatorIdMsg{validatorId: id, reply: reply})
				require.Same(t, remaining.Peer, <-reply, "parent and random-Core lookup must keep the live route")
			}
			require.True(t, remaining.Connected())
			remaining.Disconnect()
			s.handleDonePeerMsg(state, remaining)
			require.NotContains(t, state.minerPeers, validator)
		})
	}
}
