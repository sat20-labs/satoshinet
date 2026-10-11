package contract_e2e

import (
	"encoding/hex"
	"encoding/json"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	bootstrapMnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
	coreMnemonic      = "uniform bulb body vital later special era tourist build chief devote annual"
)

func generateOrWaitBlockAtLeast(t *testing.T, node *rpctest.Harness, nodes []*rpctest.Harness, minHeight int32) []*chainhash.Hash {
	t.Helper()
	bestHash, bestHeight, err := node.Client.GetBestBlock()
	require.NoError(t, err)
	if bestHeight >= minHeight {
		require.NoError(t, rpctest.JoinNodes(nodes, rpctest.Blocks))
		return []*chainhash.Hash{bestHash}
	}

	blockHashes, err := node.Client.Generate(1)
	if err == nil {
		require.Len(t, blockHashes, 1)
		require.NoError(t, rpctest.JoinNodes(nodes, rpctest.Blocks))
		return blockHashes
	}
	if !waitForPOSBlockError(err) {
		require.NoError(t, err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		bestHash, bestHeight, bestErr := node.Client.GetBestBlock()
		if bestErr == nil && bestHeight >= minHeight {
			require.NoError(t, rpctest.JoinNodes(nodes, rpctest.Blocks))
			return []*chainhash.Hash{bestHash}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, err)
	return nil
}

func waitForPOSBlockError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "no any new tx") ||
		strings.Contains(message, "already in POS mining")
}

func startFakeL1Indexer(t *testing.T, indexerPubKey string,
	utxos map[string]*indexercommon.AssetsInUtxo, parentByPub ...map[string]string) *httptest.Server {

	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/testnet/kv/register" {
			pub := indexerPubKey
			if len(parentByPub) != 0 {
				var req indexerwire.RegisterPubKeyReq
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				if parent, ok := parentByPub[0][req.PubKey]; ok {
					pub = parent
				}
			}
			err := json.NewEncoder(w).Encode(indexerwire.RegisterPubKeyResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
				PubKey:   pub,
			})
			require.NoError(t, err)
			return
		}
		const prefix = "/testnet/v3/utxo/info/"
		require.True(t, strings.HasPrefix(r.URL.Path, prefix), r.URL.Path)
		utxo := strings.TrimPrefix(r.URL.Path, prefix)
		data, ok := utxos[utxo]
		if !ok {
			err := json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
				BaseResp: indexerwire.BaseResp{Code: 404, Msg: "missing utxo"},
			})
			require.NoError(t, err)
			return
		}
		err := json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
			BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
			Data:     data,
		})
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	return server
}

func startSatoshiNetNetwork(t *testing.T, fakeL1 *httptest.Server) (*rpctest.Harness, *rpctest.Harness) {
	t.Helper()
	bootstrapNode := startSatoshiNetNode(t, fakeL1, "bootstrap", bootstrapMnemonic)
	coreNode := startSatoshiNetNode(t, fakeL1, "core", coreMnemonic)
	require.NoError(t, rpctest.ConnectNode(coreNode, bootstrapNode))
	require.NoError(t, rpctest.JoinNodes([]*rpctest.Harness{bootstrapNode, coreNode}, rpctest.Blocks))
	return bootstrapNode, coreNode
}

func startSatoshiNetNode(t *testing.T, fakeL1 *httptest.Server, role, mnemonic string, extraArgs ...string) *rpctest.Harness {
	t.Helper()
	nodeKey := keyFromMnemonic(t, mnemonic, 0)
	nodePubKey := hex.EncodeToString(nodeKey.PubKey().SerializeCompressed())
	btcdCfg := []string{
		"--notls",
		"--nocheckpoints",
		"--nodnsseed",
		"--indexerscheme=http",
		"--indexerhost=" + strings.TrimPrefix(fakeL1.URL, "http://"),
		"--indexerproxy=testnet",
		"--generate",
		"--miningpubkey=" + nodePubKey,
	}
	btcdCfg = append(btcdCfg, extraArgs...)
	env := []string{
		"SATOSHINET_RPCTEST_NODE_ROLE=" + role,
		"SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic,
		"SATOSHINET_RPCTEST_STP_PASSWORD=rpctest",
	}
	for _, name := range []string{
		"SATOSHINET_POS_MINER_INTERVAL",
		"SATOSHINET_POS_PREWARNING_INTERVAL",
		"SATOSHINET_POS_CHECKING_INTERVAL",
		"SATOSHINET_RPCTEST_POS_V2_HEIGHT",
	} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	r, err := rpctest.NewWithEnv(
		&chaincfg.TestNetParams, nil, btcdCfg, contractE2EExecutablePath(t), env,
	)
	require.NoError(t, err)
	r.MaxConnRetries = 30
	r.ConnectionRetryTimeout = 100 * time.Millisecond
	require.NoError(t, r.SetUp(false, 0))
	t.Logf("started %s node: pid=%d rpc=%s p2p=%s log=%s",
		role, r.NodePID(), r.RPCAddress(), r.P2PAddress(), r.LogFile())
	t.Cleanup(func() {
		if err := r.TearDown(); err != nil {
			if t.Failed() {
				t.Logf("node teardown after test failure: %v", err)
				return
			}
			require.NoError(t, err)
		}
	})
	return r
}

func buildCounterContractTxs(t *testing.T, anchorTx *wire.MsgTx,
	callerKeys []*btcec.PrivateKey, spendScript []byte, spendAddress string,
	redeemScript, controlBlock []byte) (*wire.MsgTx, []*wire.MsgTx, evm.ContractAddress) {

	t.Helper()
	require.Len(t, callerKeys, 3)
	caller := evmAddressFromAddressString(spendAddress)
	deployTx, contract, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: evm.TestnetContractPrefix,
		Deployer:       caller.String(),
		GasLimit:       evm.DefaultGasConfig().DeployBaseGas,
		DeployNonce:    1,
		ContractContent: testEVMInitCode(
			testCounterRuntimeCode(),
		),
		Funding: wire.TxOut{
			Value: 1000,
			Assets: wire.TxAssets{{
				Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
				Amount: *indexercommon.NewDefaultDecimal(300000),
			}},
		},
		Inputs: []wire.OutPoint{{
			Hash:  anchorTx.TxHash(),
			Index: 0,
		}},
		ExtraOutputs: []*wire.TxOut{
			wire.NewTxOut(1000, testWireAsset(evm.DefaultGasConfig().GasAssetName, 200000), spendScript),
			wire.NewTxOut(1000, testWireAsset(evm.DefaultGasConfig().GasAssetName, 200000), spendScript),
			wire.NewTxOut(1000, testWireAsset(evm.DefaultGasConfig().GasAssetName, 200000), spendScript),
		},
	})
	require.NoError(t, err)
	signTemplateTaprootInputs(t, deployTx, callerKeys[0], redeemScript, controlBlock)

	invokeTxs := make([]*wire.MsgTx, 0, len(callerKeys))
	for i, key := range callerKeys {
		tx, err := evm.BuildInvokeTx(evm.InvokeTxBuildRequest{
			Contract:  contract,
			GasLimit:  evm.DefaultGasConfig().InvokeBaseGas,
			CallNonce: uint64(i + 1),
			Action:    contractcommon.ContractInvokeAPICall,
			Param:     networkSoliditySelector("inc()"),
			Funding: wire.TxOut{
				Assets: wire.TxAssets{{
					Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
					Amount: *indexercommon.NewDefaultDecimal(100000),
				}},
			},
			Inputs: []wire.OutPoint{{
				Hash:  deployTx.TxHash(),
				Index: uint32(2 + i),
			}},
			ExtraOutputs: []*wire.TxOut{
				wire.NewTxOut(1000, testWireAsset(evm.DefaultGasConfig().GasAssetName,
					100000-networkGasFeeAmount(t, evm.DefaultGasConfig().InvokeBaseGas)), spendScript),
			},
		})
		require.NoError(t, err)
		signTemplateTaprootInputs(t, tx, key, redeemScript, controlBlock)
		invokeTxs = append(invokeTxs, tx)
	}
	return deployTx, invokeTxs, contract
}

func testCallerSpendScript(t *testing.T) []byte {
	t.Helper()
	script, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_DROP).
		AddOp(txscript.OP_TRUE).
		Script()
	require.NoError(t, err)
	return script
}

func publicKeyPushScript(t *testing.T, key *btcec.PrivateKey) []byte {
	t.Helper()
	script, err := txscript.NewScriptBuilder().
		AddData(key.PubKey().SerializeCompressed()).
		Script()
	require.NoError(t, err)
	return script
}

func evmAddressFromAddressString(address string) evm.EVMAddress {
	var out evm.EVMAddress
	hash := btcutil.Hash160([]byte(address))
	copy(out[:], hash)
	return out
}

func testWireAsset(name string, amount int64) wire.TxAssets {
	assetName := wire.NewAssetNameFromString(name)
	return wire.TxAssets{{
		Name:   *assetName,
		Amount: *indexercommon.NewDefaultDecimal(amount),
	}}
}

func testDisplayAsset(name string, amount string) *indexercommon.DisplayAsset {
	assetName := indexercommon.NewAssetNameFromString(name)
	return &indexercommon.DisplayAsset{
		AssetName: *assetName,
		Amount:    amount,
		Precision: 0,
	}
}

func testEVMInitCode(runtime []byte) []byte {
	init := []byte{
		0x60, byte(len(runtime)),
		0x60, 0x0c,
		0x60, 0x00,
		0x39,
		0x60, byte(len(runtime)),
		0x60, 0x00,
		0xf3,
	}
	return append(init, runtime...)
}

func testCounterRuntimeCode() []byte {
	return []byte{
		0x60, 0x00,
		0x54,
		0x60, 0x01,
		0x01,
		0x80,
		0x60, 0x00,
		0x55,
		0x60, 0x00,
		0x52,
		0x60, 0x20,
		0x60, 0x00,
		0xf3,
	}
}

func signedAnchorScript(t *testing.T, utxo string, witnessScript []byte,
	value int64, assets wire.TxAssets, key *btcec.PrivateKey) []byte {

	t.Helper()
	invoice, err := anchortx.StandardAnchorScript(utxo, witnessScript, value, assets)
	require.NoError(t, err)
	sig := ecdsa.Sign(key, chainhash.HashB(invoice))
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	require.NoError(t, err)
	script, err := txscript.NewScriptBuilder().
		AddData([]byte(utxo)).
		AddData(witnessScript).
		AddInt64(value).
		AddData(assetsBuf).
		AddData(sig.Serialize()).
		Script()
	require.NoError(t, err)
	return script
}

func keyFromMnemonic(t *testing.T, mnemonic string, index uint32) *btcec.PrivateKey {
	t.Helper()
	require.True(t, bip39.IsMnemonicValid(mnemonic))
	seed := bip39.NewSeed(mnemonic, "")
	key, err := hdkeychain.NewMaster(seed, &chaincfg.TestNetParams)
	require.NoError(t, err)
	for _, child := range []uint32{
		hdkeychain.HardenedKeyStart + 86,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
		0,
		index,
	} {
		key, err = key.Derive(child)
		require.NoError(t, err)
	}
	privKey, err := key.ECPrivKey()
	require.NoError(t, err)
	return privKey
}
