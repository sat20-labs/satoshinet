package base

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	idxcommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestL2StakeMinerInfoSurvivesRestart(t *testing.T) {
	b, path, pubs := membershipIndexer(t)
	parent, child := pubs[0], pubs[2]
	a, err := hex.DecodeString(parent)
	require.NoError(t, err)
	c, err := hex.DecodeString(child)
	require.NoError(t, err)
	_, script, err := anchortx.GetP2WSHscript(a, c)
	require.NoError(t, err)
	name := idxcommon.NewAssetNameFromString(idxcommon.GetStakeAssetNameWithHeightL2(1))
	amount := idxcommon.NewDefaultDecimal(idxcommon.GetStakeAssetAmtWithHeightL2(1))
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{9}}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{Name: *idxcommon.NewAssetNameFromString("brc20:f:other"), Amount: *idxcommon.NewDefaultDecimal(7)}, {Name: *name, Amount: *amount}}, script))
	block := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: *b.chaincfgParam.GenesisHash}, Transactions: []*wire.MsgTx{tx}}
	converted := ConvertBlock(block, 1, b.chaincfgParam).Transactions[0]
	channel := converted.Outputs[0].Address.Addresses[0]
	b.channelMap[channel] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{Address: channel, PubA: a, PubB: c}, IsNew: true}
	invoice, err := common.CreateStakeInvoice(name, amount)
	require.NoError(t, err)
	b.handleStakeAssetV2(1, converted, invoice)
	require.Contains(t, b.coreNodeMap[parent].ChildMiners, child)
	var raw bytes.Buffer
	require.NoError(t, block.Serialize(&raw))
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if conn.ReadJSON(&req) != nil {
				return
			}
			var result interface{}
			switch req.Method {
			case "getblockcount":
				result = 1
			case "getblockhash":
				result = block.BlockHash().String()
			case "getblock":
				result = hex.EncodeToString(raw.Bytes())
			}
			if conn.WriteJSON(map[string]interface{}{"id": req.ID, "error": nil, "result": result}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	_, err = satsnet_rpc.InitSatsNetClient(host, port, "test", "test", "", false)
	require.NoError(t, err)
	defer satsnet_rpc.ShutdownSatsNetClient()
	check := func(current *BaseIndexer) {
		t.Helper()
		var info *common.MinerInfo
		require.NotPanics(t, func() { info = NewRpcIndexer(current).GetMinerInfo(child) })
		require.NotNil(t, info)
		require.Equal(t, 1, info.AscendHeight)
		require.Equal(t, tx.TxID()+":0", info.AscendUtxo)
		require.Equal(t, parent, info.ServerNode)
		require.Equal(t, channel, info.ChannelAddr)
		require.Equal(t, name.String(), info.AssetName)
		require.Equal(t, amount.String(), info.AssetAmt)
		require.Empty(t, info.AnchorTxId)
	}
	check(b)
	restored := restartMembership(t, b, path, 1)
	check(restored)
	restored.coreNodeMap[parent].ChildMiners[child].AscendUtxo = tx.TxID() + ":99"
	require.Nil(t, NewRpcIndexer(restored).GetMinerInfo(child))
}

func TestL2StakeCoreUsesActualAssetInMultiAssetOutput(t *testing.T) {
	b, path, pubs := membershipIndexer(t)
	root := idxcommon.GetBootstrapPubKey()
	a, err := hex.DecodeString(root)
	require.NoError(t, err)
	c, err := hex.DecodeString(pubs[2])
	require.NoError(t, err)
	_, script, err := anchortx.GetP2WSHscript(a, c)
	require.NoError(t, err)
	name := idxcommon.NewAssetNameFromString(idxcommon.GetStakeAssetNameWithHeightL2(1))
	amount := idxcommon.NewDefaultDecimal(idxcommon.GetStakeAssetAmtWithHeightL2(1))
	tx := wire.NewMsgTx(2)
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{Name: *idxcommon.NewAssetNameFromString("brc20:f:other"), Amount: *idxcommon.NewDefaultDecimal(7)}, {Name: *name, Amount: *amount}}, script))
	converted := ConvertBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}, 1, b.chaincfgParam).Transactions[0]
	channel := converted.Outputs[0].Address.Addresses[0]
	b.channelMap[channel] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{Address: channel, PubA: a, PubB: c}, IsNew: true}
	invoice, err := common.CreateStakeInvoice(name, amount)
	require.NoError(t, err)
	b.handleStakeAssetV2(1, converted, invoice)
	check := func(current *BaseIndexer) {
		core := current.coreNodeMap[pubs[2]]
		require.NotNil(t, core)
		require.Equal(t, name.String(), core.AssetName)
		require.Equal(t, amount.String(), core.AssetAmt)
		require.Equal(t, root, core.ServerNode)
		require.Equal(t, channel, core.ChannelAddr)
	}
	check(b)
	check(restartMembership(t, b, path, 1))
}
