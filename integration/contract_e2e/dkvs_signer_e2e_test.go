package contract_e2e

import (
	"encoding/hex"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Each process advertises the same core. Only its actual wallet can sign for it.
func TestNetworkDKVSSignerIdentity(t *testing.T) {
	pub := hex.EncodeToString(keyFromMnemonic(t, coreMnemonic, 0).PubKey().SerializeCompressed())
	parent := hex.EncodeToString(keyFromMnemonic(t, bootstrapMnemonic, 0).PubKey().SerializeCompressed())
	l1 := startFakeL1Indexer(t, parent, nil)
	exe := contractE2EExecutablePath(t)
	if ports, ok := os.LookupEnv("SATOSHINET_CONTRACT_E2E_PORT_RANGE"); ok {
		previous := rpctest.ListenAddressGenerator
		t.Cleanup(func() { rpctest.ListenAddressGenerator = previous })
		rpctest.ListenAddressGenerator = func() (string, string) {
			return contractE2EListenAddresses(t, ports, "127.0.0.1")
		}
	}
	for _, tc := range []struct {
		name, mnemonic string
		identityArgs   []string
		wantReject     bool
	}{
		{"mismatched", bootstrapMnemonic, []string{"--miningpubkey=" + pub, "--serverpubkey=" + parent}, true},
		{"matching", coreMnemonic, []string{"--miningpubkey=" + pub, "--serverpubkey=" + parent}, false},
		{"derived", coreMnemonic, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{
				"--notls", "--nocheckpoints", "--nodnsseed", "--connect=127.0.0.1:1", "--generate",
				"--indexerscheme=http", "--indexerhost=" + strings.TrimPrefix(l1.URL, "http://"), "--indexerproxy=testnet",
			}
			node, err := rpctest.NewWithEnv(&chaincfg.TestNetParams, nil, append(args, tc.identityArgs...),
				exe, []string{"SATOSHINET_RPCTEST_NODE_ROLE=core", "SATOSHINET_RPCTEST_STP_MNEMONIC=" + tc.mnemonic,
					"SATOSHINET_RPCTEST_STP_PASSWORD=rpctest"})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, node.TearDown()) })
			require.NoError(t, node.SetUp(false, 0))

			request := &wire.MsgDKVSSyncRequest{SessionID: 1, Limit: 1}
			reply := make(chan wire.Message, 1)
			p, err := peer.NewOutboundPeer(&peer.Config{ChainParams: &chaincfg.TestNetParams,
				Listeners: peer.MessageListeners{
					OnVerAck:           func(p *peer.Peer, _ *wire.MsgVerAck) { p.QueueMessage(request, nil) },
					OnReject:           func(_ *peer.Peer, m *wire.MsgReject) { reply <- m },
					OnDKVSSyncResponse: func(_ *peer.Peer, m *wire.MsgDKVSSyncResponse) { reply <- m },
				}}, node.P2PAddress())
			require.NoError(t, err)
			conn, err := net.DialTimeout("tcp", node.P2PAddress(), 5*time.Second)
			require.NoError(t, err)
			p.AssociateConnection(conn)
			t.Cleanup(func() { p.Disconnect(); p.WaitForDisconnect() })
			select {
			case msg := <-reply:
				require.Equal(t, pub, p.ValidatorId())
				if tc.wantReject {
					require.IsType(t, &wire.MsgReject{}, msg)
					reject := msg.(*wire.MsgReject)
					require.Equal(t, wire.CmdDKVSSyncRequest, reject.Cmd)
					require.Equal(t, wire.RejectDKVSNoSigner, reject.Code)
					return
				}
				require.IsType(t, &wire.MsgDKVSSyncResponse{}, msg)
				response := msg.(*wire.MsgDKVSSyncResponse)
				require.Equal(t, request.SessionID, response.SessionID)
				require.True(t, dkvsp2p.VerifySyncSignature(chaincfg.TestNetParams.Net, pub, nil, nil, response))
			case <-time.After(10 * time.Second):
				t.Fatal("no DKVS reply from the node")
			}
		})
	}
}
