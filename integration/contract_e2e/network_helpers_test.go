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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	utxos map[string]*indexercommon.AssetsInUtxo) *httptest.Server {

	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/testnet/v3/indexer/pubkey" {
			err := json.NewEncoder(w).Encode(indexerwire.IndexerPubKeyResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}, PubKey: indexerPubKey,
			})
			require.NoError(t, err)
			return
		}
		if r.URL.Path == "/testnet/kv/register" {
			err := json.NewEncoder(w).Encode(indexerwire.RegisterPubKeyResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
				PubKey:   indexerPubKey,
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
	r := newContractE2EHarness(t, &chaincfg.TestNetParams,
		btcdCfg, contractE2EExecutablePath(t), env, "127.0.0.1")
	r.MaxConnRetries = 30
	r.ConnectionRetryTimeout = 100 * time.Millisecond
	require.NoError(t, r.SetUp(false, 0))
	t.Logf("started %s node: pid=%d rpc=%s p2p=%s log=%s",
		role, r.NodePID(), r.RPCAddress(), r.P2PAddress(), r.LogFile())
	return r
}

// An explicit range opts into the controlled runner's port policy. The runner
// must approve and preflight it; there is deliberately no automatic range.
// These helpers are used only by serial E2E tests.
var contractE2EPorts struct {
	first, last, next int
}

func newContractE2EHarness(t *testing.T, activeNet *chaincfg.Params,
	extraArgs []string, executable string, env []string, p2pHost string) *rpctest.Harness {

	t.Helper()
	portRange, controlled := os.LookupEnv("SATOSHINET_CONTRACT_E2E_PORT_RANGE")
	var generator func() (string, string)
	if controlled {
		p2p, rpc := contractE2EListenAddresses(t, portRange, p2pHost)
		generator = func() (string, string) { return p2p, rpc }
	} else if p2pHost == "::1" {
		// Preserve the existing IPv6 scenario when no controlled range is set.
		previous := rpctest.ListenAddressGenerator
		generator = func() (string, string) {
			p2p, rpc := previous()
			_, port, err := net.SplitHostPort(p2p)
			require.NoError(t, err)
			return net.JoinHostPort("::1", port), rpc
		}
	}
	r, err := func() (*rpctest.Harness, error) {
		previous := rpctest.ListenAddressGenerator
		defer func() { rpctest.ListenAddressGenerator = previous }()
		if generator != nil {
			rpctest.ListenAddressGenerator = generator
		}
		return rpctest.NewWithEnv(activeNet, nil, extraArgs, executable, env)
	}()
	require.NoError(t, err)
	prepareContractE2ENode(t, r, controlled)
	return r
}

func contractE2EListenAddresses(t *testing.T, portRange, p2pHost string) (string, string) {
	t.Helper()
	require.True(t, p2pHost == "127.0.0.1" || p2pHost == "::1", "P2P must use loopback")
	parts := strings.Split(strings.TrimSpace(portRange), "-")
	require.Len(t, parts, 2, "SATOSHINET_CONTRACT_E2E_PORT_RANGE must be an approved start-end range")
	first, err := strconv.Atoi(parts[0])
	require.NoError(t, err)
	last, err := strconv.Atoi(parts[1])
	require.NoError(t, err)
	require.True(t, first >= 1024 && last <= 65535 && first <= last-2, "range must contain at least three unprivileged ports")
	for _, port := range []int{5189, 18443, 18453, 20011, 20012, 24225, 30043, 30143} {
		require.False(t, first <= port && port <= last, "range contains protected port %d", port)
	}
	require.False(t, first <= 30374 && last >= 30350, "range overlaps protected PWA ports")
	require.False(t, first <= 53170 && last >= 53168, "range overlaps reserved PWA ports")
	require.False(t, first <= 31464 && last >= 31450, "range overlaps reserved SDK test ports")
	if contractE2EPorts.next == 0 {
		contractE2EPorts.first, contractE2EPorts.last, contractE2EPorts.next = first, last, first
	}
	require.Equal(t, contractE2EPorts.first, first, "port range must not change during a run")
	require.Equal(t, contractE2EPorts.last, last, "port range must not change during a run")

	var lastBindErr error
	for contractE2EPorts.next+2 <= last {
		port := contractE2EPorts.next
		contractE2EPorts.next += 3 // Never reuse a stopped node's ports before Restart.
		p2p := net.JoinHostPort(p2pHost, strconv.Itoa(port))
		rpc := net.JoinHostPort("127.0.0.1", strconv.Itoa(port+1))
		indexer := net.JoinHostPort("127.0.0.1", strconv.Itoa(port+2))
		p2pNetwork := "tcp4"
		if p2pHost == "::1" {
			p2pNetwork = "tcp6"
		}
		var listeners []net.Listener
		available := true
		for _, endpoint := range []struct{ network, address string }{
			{p2pNetwork, p2p}, {"tcp4", rpc}, {"tcp4", indexer},
		} {
			listener, listenErr := net.Listen(endpoint.network, endpoint.address)
			if listenErr != nil {
				lastBindErr, available = listenErr, false
				break
			}
			listeners = append(listeners, listener)
		}
		var closeErr error
		for _, listener := range listeners {
			if err := listener.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		require.NoError(t, closeErr)
		if available {
			return p2p, rpc
		}
	}
	t.Fatalf("approved E2E port range %d-%d exhausted; last bind error: %v", first, last, lastBindErr)
	return "", ""
}

func prepareContractE2ENode(t *testing.T, node *rpctest.Harness, controlled bool) {
	t.Helper()
	// Register before SetUp, including all startup-failure paths.
	t.Cleanup(func() { require.NoError(t, node.TearDown()) })
	tmp := os.TempDir()
	if controlled {
		require.NotEmpty(t, os.Getenv("TMPDIR"), "controlled runner must provide a fresh private TMPDIR")
		require.True(t, filepath.IsAbs(tmp), "TMPDIR must be absolute")
		info, err := os.Lstat(tmp)
		require.NoError(t, err)
		require.True(t, info.IsDir() && info.Mode()&os.ModeSymlink == 0, "TMPDIR must be a real directory")
		require.Zero(t, info.Mode().Perm()&0077, "controlled TMPDIR must be private")
	}
	logFile := node.LogFile()
	relative, err := filepath.Rel(tmp, logFile)
	require.NoError(t, err)
	require.True(t, relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)), "node log must be inside TMPDIR")

	// MkdirTemp creates a private directory on the same filesystem. Do not use
	// t.TempDir: this stdout inode must survive testing cleanup and timeout.
	// Never walk, copy, or hardlink the node's database directory.
	archive, err := os.MkdirTemp(tmp, "contract-e2e-stdout-")
	require.NoError(t, err)
	stdout, err := os.OpenFile(logFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	require.NoError(t, stdout.Close())
	retained := filepath.Join(archive, filepath.Base(filepath.Dir(logFile))+".stdout.log")
	require.NoError(t, os.Link(logFile, retained))
	sourceInfo, err := os.Lstat(logFile)
	require.NoError(t, err)
	retainedInfo, err := os.Lstat(retained)
	require.NoError(t, err)
	require.True(t, sourceInfo.Mode().IsRegular() && retainedInfo.Mode().IsRegular() && os.SameFile(sourceInfo, retainedInfo), "stdout hardlink must retain the same regular-file inode")
	t.Logf("retained node stdout: %s", retained)
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
