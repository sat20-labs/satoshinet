package netsync

import (
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSyncCandidateServiceBoundaries(t *testing.T) {
	for _, params := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNetParams, &chaincfg.RegressionNetParams} {
		t.Run(params.Name, func(t *testing.T) {
			db, err := database.Create("ffldb", t.TempDir(), params.Net)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: params, TimeSource: blockchain.NewMedianTime()})
			require.NoError(t, err)
			active, err := chain.IsDeploymentActive(chaincfg.DeploymentSegwit)
			require.NoError(t, err)
			require.False(t, active, "these cases cover service requirements before witness activation")
			sm := &SyncManager{chain: chain, chainParams: params}
			for _, tc := range []struct {
				name, addr string
				services   wire.ServiceFlag
				height     int32
				want       bool
			}{
				{"full", "192.0.2.1:39526", wire.SFNodeNetwork, 0, true},
				{"no_blocks", "192.0.2.1:39526", 0, 0, false},
				{"witness_only", "192.0.2.1:39526", wire.SFNodeWitness, 0, false},
				{"pruned_next_available", "[::1]:39526", wire.SFNodeNetworkLimited, wire.NodeNetworkLimitedBlockThreshold, true},
				{"pruned_next_unavailable", "[::1]:39526", wire.SFNodeNetworkLimited, wire.NodeNetworkLimitedBlockThreshold + 1, false},
				{"full_and_pruned", "[::1]:39526", wire.SFNodeNetwork | wire.SFNodeNetworkLimited, wire.NodeNetworkLimitedBlockThreshold + 1, true},
				{"ipv4_tool", "127.0.0.1:39526", 0, 0, params == &chaincfg.RegressionNetParams},
				{"hostname_tool", "localhost:39526", 0, 0, params == &chaincfg.RegressionNetParams},
			} {
				t.Run(tc.name, func(t *testing.T) {
					p, err := peer.NewOutboundPeer(&peer.Config{ChainParams: params, Services: tc.services,
						HostToNetAddress: func(host string, port uint16, services wire.ServiceFlag) (*wire.NetAddressV2, error) {
							if host == "localhost" {
								host = "127.0.0.1"
							}
							return wire.NetAddressV2FromBytes(time.Now(), services, net.ParseIP(host), port), nil
						}}, tc.addr)
					require.NoError(t, err)
					p.UpdateLastBlockHeight(tc.height)
					require.Equal(t, tc.want, sm.isSyncCandidate(p))
				})
			}
		})
	}
}

// Use the existing POS recovery fixture's sequence/readiness implementation to
// activate the real BIP9 state machine. These synthetic unit-test blocks are
// separate from the nonempty, real-node historical-sync acceptance test.
func TestRegtestSyncCandidateWitnessAfterActivation(t *testing.T) {
	params := chaincfg.RegressionNetParams
	params.POSV2Height = 1
	key, _ := btcec.PrivKeyFromBytes([]byte{1})
	seq := scommon.NewMiningSequenceMgr(&params)
	pub := hex.EncodeToString(key.PubKey().SerializeCompressed())
	require.NoError(t, seq.Init(map[string]*scommon.CoreNodeInfo{pub: scommon.NewCoreNodeInfo(nil)}, 0, ""))
	ready := &recoveryReady{hash: *params.GenesisHash, seq: seq}
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params,
		TimeSource: blockchain.NewMedianTime(), AssetIndexReadiness: ready})
	require.NoError(t, err)
	for height := 1; height <= 2; height++ {
		best := chain.BestSnapshot()
		sig := ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(height, 0))).Serialize()
		script, err := txscript.NewScriptBuilder().AddInt64(int64(height)).AddInt64(0).AddData(sig).Script()
		require.NoError(t, err)
		addr, err := btcutil.DecodeAddress(seq.GetCurrentMiningAddr(), &params)
		require.NoError(t, err)
		reward, err := txscript.PayToAddrScript(addr)
		require.NoError(t, err)
		coinbase := wire.NewMsgTx(2)
		coinbase.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, script, wire.TxWitness{make([]byte, 32)}))
		coinbase.AddTxOut(wire.NewTxOut(0, nil, reward))
		root := blockchain.CalcMerkleRoot([]*btcutil.Tx{btcutil.NewTx(coinbase)}, true)
		var preimage [64]byte
		copy(preimage[:32], root[:])
		coinbase.AddTxOut(wire.NewTxOut(0, nil, append(append([]byte(nil), blockchain.WitnessMagicBytes...), chainhash.DoubleHashB(preimage[:])...)))
		version := int32(0x20000000 | (1 << params.Deployments[chaincfg.DeploymentSegwit].BitNumber))
		msg := &wire.MsgBlock{Header: wire.BlockHeader{Version: version, PrevBlock: best.Hash,
			Timestamp: best.MedianTime.Add(time.Second), Bits: params.PowLimitBits}, Transactions: []*wire.MsgTx{coinbase}}
		msg.Header.MerkleRoot = blockchain.CalcMerkleRoot([]*btcutil.Tx{btcutil.NewTx(coinbase)}, false)
		hash := msg.BlockHash()
		for blockchain.HashToBig(&hash).Cmp(blockchain.CompactToBig(msg.Header.Bits)) > 0 {
			msg.Header.Nonce++
			hash = msg.BlockHash()
		}
		coinbase.TxIn[0].Witness = append(coinbase.TxIn[0].Witness,
			ecdsa.Sign(key, chainhash.HashB(scommon.POSApprovalMessage(params.Net, int32(height), msg.BlockHash()))).Serialize())
		mainChain, orphan, err := chain.ProcessBlock(btcutil.NewBlock(msg), blockchain.BFNone)
		require.NoError(t, err)
		require.True(t, mainChain)
		require.False(t, orphan)
		require.NoError(t, seq.MoveMiningAddr(height, seq.GetCurrentMiningAddr()))
		ready.height, ready.hash = height, chain.BestSnapshot().Hash
	}
	active, err := chain.IsDeploymentActive(chaincfg.DeploymentSegwit)
	require.NoError(t, err)
	require.True(t, active)
	sm := &SyncManager{chain: chain, chainParams: &chaincfg.RegressionNetParams}
	for _, witness := range []bool{false, true} {
		name := "without_witness"
		services := wire.SFNodeNetwork
		if witness {
			name, services = "with_witness", services|wire.SFNodeWitness
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			conn, err := net.Dial("tcp4", listener.Addr().String())
			require.NoError(t, err)
			accepted, err := listener.Accept()
			require.NoError(t, err)
			verack := make(chan struct{})
			out, err := peer.NewOutboundPeer(&peer.Config{ChainParams: &params, AllowSelfConns: true,
				Services:  wire.SFNodeNetwork | wire.SFNodeWitness,
				Listeners: peer.MessageListeners{OnVerAck: func(*peer.Peer, *wire.MsgVerAck) { close(verack) }},
			}, "192.0.2.1:39526") // Address-policy unit input; TCP only connects to the local listener above.
			require.NoError(t, err)
			in := peer.NewInboundPeer(&peer.Config{ChainParams: &params, AllowSelfConns: true, Services: services})
			t.Cleanup(func() { out.Disconnect(); in.Disconnect(); out.WaitForDisconnect(); in.WaitForDisconnect() })
			in.AssociateConnection(accepted)
			out.AssociateConnection(conn)
			select {
			case <-verack:
			case <-time.After(3 * time.Second):
				t.Fatal("local witness capability handshake did not complete")
			}
			require.Equal(t, witness, out.IsWitnessEnabled())
			require.Equal(t, witness, sm.isSyncCandidate(out))
		})
	}
	tool, err := peer.NewOutboundPeer(&peer.Config{ChainParams: &params}, "127.0.0.1:39526")
	require.NoError(t, err)
	require.True(t, sm.isSyncCandidate(tool), "the existing local-tool candidate exception stays intact")
}
