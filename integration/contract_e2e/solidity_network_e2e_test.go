//go:build rpctest
// +build rpctest

package contract_e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	gethcommon "github.com/ethereum/go-ethereum/common"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestNetworkSolidityContractsDeployInvokeAndAssetSettlement(t *testing.T) {
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	counter := compileNetworkSolidityContract(t, solidityNetworkCounterSource, "Counter")
	erc20 := compileNetworkSolidityContract(t, solidityNetworkMiniERC20Source, "MiniERC20")
	vault := compileNetworkSolidityContract(t, solidityNetworkVaultSource, "SatoshiNetTimelockVault")

	const (
		gasLockedUtxo   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:0"
		assetLockedUtxo = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc:0"
		lockedValue     = int64(100000)
		vaultAsset      = "ordx:ft:usd"
	)
	gasAsset := evm.DefaultGasConfig().GasAssetName
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	callerKeys := []*btcec.PrivateKey{
		keyFromMnemonic(t, bootstrapMnemonic, 1),
		keyFromMnemonic(t, bootstrapMnemonic, 2),
		keyFromMnemonic(t, bootstrapMnemonic, 3),
	}
	witnessScript, lockedPkScript, err := anchortx.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	fakeL1 := startFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()),
		map[string]*indexercommon.AssetsInUtxo{
			gasLockedUtxo: {
				OutPoint: gasLockedUtxo,
				Value:    lockedValue,
				PkScript: lockedPkScript,
				Assets: []*indexercommon.DisplayAsset{
					testDisplayAsset(gasAsset, "100000000"),
				},
			},
			assetLockedUtxo: {
				OutPoint: assetLockedUtxo,
				Value:    lockedValue,
				PkScript: lockedPkScript,
				Assets: []*indexercommon.DisplayAsset{
					testDisplayAssetWithPrecision(vaultAsset, "10.00", 2),
				},
			},
		})
	bootstrapNode, coreNode := startSatoshiNetNetwork(t, fakeL1)
	nodes := []*rpctest.Harness{bootstrapNode, coreNode}
	caller0Script, caller0Address, _, _ := testCallerTaprootScript(t, callerKeys[0])
	caller1Script, caller1Address, _, _ := testCallerTaprootScript(t, callerKeys[1])
	caller2Script, caller2Address, _, _ := testCallerTaprootScript(t, callerKeys[2])
	recipient := p2trAddressFromKey(t, callerKeys[2])

	gasAnchor := buildNetworkAnchorTx(t, gasLockedUtxo, lockedValue,
		testWireAsset(gasAsset, 100000000), gasAsset+"-100000000-0-1",
		witnessScript, bootstrapKey, caller0Script)
	assetAnchor := buildNetworkAnchorTx(t, assetLockedUtxo, lockedValue,
		testWireDecimalAsset(t, vaultAsset, "10.00", 2), vaultAsset+"-10.00-2-1",
		witnessScript, bootstrapKey, caller0Script)
	sendTx(t, bootstrapNode, gasAnchor)
	sendTx(t, bootstrapNode, assetAnchor)
	generateOrWaitBlockAtLeast(t, bootstrapNode, nodes, 1)

	counterDeploy, counterContract, counterChanges := buildSolidityDeployTx(t, callerKeys[0], 1,
		solidityDeployCode(t, counter, nil),
		[]wire.OutPoint{{Hash: gasAnchor.TxHash(), Index: 0}},
		wire.TxOut{Assets: wire.TxAssets{networkGasFunding(t, gasAsset, 3000000)}},
		[]*wire.TxOut{
			testSpendAssetOutput(gasAsset, 5000000, caller0Script),
			testSpendAssetOutput(gasAsset, 49900000, caller0Script),
			testSpendAssetOutput(gasAsset, 5000000, caller1Script),
			testSpendAssetOutput(gasAsset, 5000000, caller0Script),
			testSpendAssetOutput(gasAsset, 5000000, caller1Script),
			testSpendAssetOutput(gasAsset, 5000000, caller2Script),
			testSpendAssetOutput(gasAsset, 22000000, caller0Script),
		})
	sendAndMineTx(t, bootstrapNode, nodes, counterDeploy, 2)

	erc20DeployCode := solidityDeployCode(t, erc20,
		[]interface{}{"SatoshiNet Test Token", "SNT", uint8(6), new(big.Int).Mul(big.NewInt(1000), big.NewInt(1_000_000))})
	erc20Deploy, erc20Contract, _ := buildSolidityDeployTx(t, callerKeys[0], 2,
		erc20DeployCode,
		[]wire.OutPoint{counterChanges[0]},
		wire.TxOut{Assets: wire.TxAssets{networkGasFunding(t, gasAsset,
			5000000-networkGasFeeAmount(t, evm.DefaultGasConfig().DeployBaseGas))}},
		nil)
	sendAndMineTx(t, bootstrapNode, nodes, erc20Deploy, 3)

	_, bestHeight, err := bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	releaseHeight := uint64(bestHeight + 3)
	vaultDeployCode := solidityDeployCode(t, vault,
		[]interface{}{vaultAsset, recipient, "1.25", new(big.Int).SetUint64(releaseHeight)})
	vaultDeploy, vaultContract, _ := buildSolidityDeployTx(t, callerKeys[0], 3,
		vaultDeployCode,
		[]wire.OutPoint{counterChanges[1]},
		wire.TxOut{Assets: wire.TxAssets{
			networkGasFunding(t, gasAsset, 10000000),
		}},
		[]*wire.TxOut{testSpendAssetOutput(gasAsset, 39800000, caller0Script)})
	sendAndMineTx(t, bootstrapNode, nodes, vaultDeploy, 4)

	vaultDeposit := buildContractAssetDepositTx(t, callerKeys[0],
		wire.OutPoint{Hash: assetAnchor.TxHash(), Index: 0},
		vaultContract, testWireDecimalAsset(t, vaultAsset, "10.00", 2))
	sendAndMineTx(t, bootstrapNode, nodes, vaultDeposit, int32(releaseHeight)-1)
	requireAssetSummaryAmount(t, bootstrapNode, vaultContract.MustEncode(), vaultAsset, "10")
	requirePositiveAssetSummary(t, bootstrapNode, caller0Address, gasAsset)
	releaseTick := buildPassthroughAssetTx(t, callerKeys[0], counterChanges[6], gasAsset, 20000000, caller0Script)
	sendAndMineTx(t, bootstrapNode, nodes, releaseTick, int32(releaseHeight))
	vaultRelease := buildSolidityInvokeTx(t, callerKeys[0], vaultContract, 2,
		packNetworkSolidityMethod(t, vault.ABI, "release"),
		wire.OutPoint{Hash: releaseTick.TxHash(), Index: 0}, gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, vaultRelease, int32(releaseHeight)+1)
	requireAssetSummaryAmount(t, bootstrapNode, recipient, vaultAsset, "1.25")
	requireAssetSummaryAmount(t, bootstrapNode, vaultContract.MustEncode(), vaultAsset, "8.75")

	counterInvoke := buildSolidityInvokeTx(t, callerKeys[1], counterContract, 1,
		packNetworkSolidityMethod(t, counter.ABI, "incrementBy", big.NewInt(7)),
		counterChanges[2], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, counterInvoke, int32(releaseHeight)+2)

	erc20Transfer := buildSolidityInvokeTx(t, callerKeys[0], erc20Contract, 1,
		packNetworkSolidityMethod(t, erc20.ABI, "transfer",
			gethcommon.Address(evm.GethAddress(evmAddressFromAddressString(caller1Address))),
			big.NewInt(125_000_000)),
		counterChanges[3], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, erc20Transfer, int32(releaseHeight)+3)

	erc20Approve := buildSolidityInvokeTx(t, callerKeys[1], erc20Contract, 2,
		packNetworkSolidityMethod(t, erc20.ABI, "approve",
			gethcommon.Address(evm.GethAddress(evmAddressFromAddressString(caller2Address))),
			big.NewInt(20_000_000)),
		counterChanges[4], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, erc20Approve, int32(releaseHeight)+4)

	erc20TransferFrom := buildSolidityInvokeTx(t, callerKeys[2], erc20Contract, 3,
		packNetworkSolidityMethod(t, erc20.ABI, "transferFrom",
			gethcommon.Address(evm.GethAddress(evmAddressFromAddressString(caller1Address))),
			gethcommon.Address(evm.GethAddress(evmAddressFromAddressString(caller2Address))),
			big.NewInt(12_500_000)),
		counterChanges[5], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, erc20TransferFrom, int32(releaseHeight)+5)

	requireAssetSummaryAmount(t, bootstrapNode, recipient, vaultAsset, "1.25")
	requireAssetSummaryAmount(t, bootstrapNode, vaultContract.MustEncode(), vaultAsset, "8.75")
	requirePositiveAssetSummary(t, bootstrapNode, caller0Address, gasAsset)
	waitForEVMContractQueries(t, bootstrapNode,
		[]string{counterContract.MustEncode(), erc20Contract.MustEncode(), vaultContract.MustEncode()},
		counterContract.MustEncode(),
	)

	bestHash, bestHeight, err := bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	coreBestHash, coreBestHeight, err := coreNode.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, bestHeight, coreBestHeight)
	require.Equal(t, bestHash, coreBestHash)
}

func waitForEVMContractQueries(t *testing.T, node *rpctest.Harness, contracts []string, historyContract string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := checkEVMContractQueries(node, contracts, historyContract); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.NoError(t, lastErr)
}

func checkEVMContractQueries(node *rpctest.Harness, contracts []string, historyContract string) error {
	baseURL, err := node.IndexerURL("testnet")
	if err != nil {
		return err
	}
	var list localwire.ContractListResp
	if err := getIndexerJSON(baseURL+"/v3/contracts", &list); err != nil {
		return err
	}
	if list.Code != 0 {
		return fmt.Errorf("contract list code %d: %s", list.Code, list.Msg)
	}
	found := make(map[string]bool, len(contracts))
	for _, summary := range list.Data {
		for _, contract := range contracts {
			if summary.Address == contract && summary.ContractTypeID == evm.ContractTypeEVM {
				found[contract] = true
			}
		}
	}
	for _, contract := range contracts {
		if !found[contract] {
			return fmt.Errorf("evm contract %s not found in contract list", contract)
		}
	}

	var history localwire.ContractHistoryResp
	if err := getIndexerJSON(baseURL+"/v3/contracts/"+historyContract+"/history", &history); err != nil {
		return err
	}
	if history.Code != 0 {
		return fmt.Errorf("contract history code %d: %s", history.Code, history.Msg)
	}
	var deploys, invokes int
	for _, record := range history.Data {
		if record.Kind == "deploy" {
			deploys++
		}
		if record.Kind == "invoke" {
			invokes++
		}
	}
	if deploys == 0 || invokes == 0 {
		return fmt.Errorf("incomplete evm history: deploys=%d invokes=%d total=%d", deploys, invokes, history.Total)
	}
	return nil
}

type networkCompiledSolidityContract struct {
	ABI      gethabi.ABI
	Bytecode []byte
}

const networkSolidityCompileTimeout = 30 * time.Second

func compileNetworkSolidityContract(t *testing.T, source, contractName string) networkCompiledSolidityContract {
	t.Helper()
	solc := os.Getenv("SATOSHINET_SOLC")
	if solc == "" {
		var err error
		solc, err = exec.LookPath("solc")
		if err != nil {
			t.Fatal("solc not found; install solc or set SATOSHINET_SOLC to run Solidity E2E tests")
		}
	}
	input := map[string]interface{}{
		"language": "Solidity",
		"sources": map[string]interface{}{
			"Contract.sol": map[string]string{"content": source},
		},
		"settings": map[string]interface{}{
			"evmVersion": "paris",
			"optimizer":  map[string]interface{}{"enabled": true, "runs": 200},
			"outputSelection": map[string]interface{}{
				"*": map[string][]string{"*": []string{"abi", "evm.bytecode.object"}},
			},
		},
	}
	encoded, err := json.Marshal(input)
	require.NoError(t, err)
	out := runNetworkSolcStandardJSON(t, solc, encoded)

	var decoded struct {
		Errors []struct {
			Severity         string `json:"severity"`
			FormattedMessage string `json:"formattedMessage"`
		} `json:"errors"`
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
			EVM struct {
				Bytecode struct {
					Object string `json:"object"`
				} `json:"bytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	}
	require.NoError(t, json.Unmarshal(out, &decoded), string(out))
	for _, item := range decoded.Errors {
		if item.Severity == "error" {
			t.Fatalf("solc error: %s", item.FormattedMessage)
		}
	}
	compiled, ok := decoded.Contracts["Contract.sol"][contractName]
	require.Truef(t, ok, "compiled contract %s not found", contractName)
	abiValue, err := gethabi.JSON(bytes.NewReader(compiled.ABI))
	require.NoError(t, err)
	bytecode := gethcommon.FromHex(compiled.EVM.Bytecode.Object)
	require.NotEmpty(t, bytecode)
	return networkCompiledSolidityContract{ABI: abiValue, Bytecode: bytecode}
}

func solidityDeployCode(t *testing.T, compiled networkCompiledSolidityContract, constructorArgs []interface{}) []byte {
	t.Helper()
	args := constructorArgs
	if args == nil {
		args = []interface{}{}
	}
	packed, err := compiled.ABI.Pack("", args...)
	require.NoError(t, err)
	initCode := make([]byte, 0, len(compiled.Bytecode)+len(packed))
	initCode = append(initCode, compiled.Bytecode...)
	return append(initCode, packed...)
}

func packNetworkSolidityMethod(t *testing.T, abiValue gethabi.ABI, method string, args ...interface{}) []byte {
	t.Helper()
	input, err := abiValue.Pack(method, args...)
	require.NoError(t, err)
	return input
}

func buildNetworkAnchorTx(t *testing.T, lockedUtxo string, lockedValue int64, assets wire.TxAssets,
	ascending string, witnessScript []byte, signer *btcec.PrivateKey, spendScript []byte) *wire.MsgTx {

	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex},
		SignatureScript:  signedAnchorScript(t, lockedUtxo, witnessScript, lockedValue, assets, signer),
	})
	tx.AddTxOut(wire.NewTxOut(lockedValue, assets, spendScript))
	ascendingScript, err := sindexercommon.NullDataScript(
		sindexercommon.CONTENT_TYPE_ASCENDING,
		[]byte(ascending),
	)
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, ascendingScript))
	return tx
}

func buildSolidityDeployTx(t *testing.T, signer *btcec.PrivateKey, nonce uint64,
	initCode []byte, inputs []wire.OutPoint, funding wire.TxOut,
	changeOutputs []*wire.TxOut) (*wire.MsgTx, evm.ContractAddress, []wire.OutPoint) {

	t.Helper()
	_, callerAddress, redeemScript, controlBlock := testCallerTaprootScript(t, signer)
	caller := evmAddressFromAddressString(callerAddress)
	tx, contract, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: evm.TestnetContractPrefix,
		Caller:         caller,
		GasLimit:       networkEVMDeployGasLimit(),
		DeployNonce:    nonce,
		InitCode:       initCode,
		Funding:        funding,
		Inputs:         inputs,
		ChangeOutputs:  changeOutputs,
	})
	require.NoError(t, err)
	signTemplateTaprootInputs(t, tx, signer, redeemScript, controlBlock)
	change := collectSpendableOutPoints(t, tx, changeOutputs)
	return tx, contract, change
}

func buildSolidityInvokeTx(t *testing.T, signer *btcec.PrivateKey, contract evm.ContractAddress,
	nonce uint64, calldata []byte, input wire.OutPoint, gasAsset string, gasAmount int64) *wire.MsgTx {

	t.Helper()
	return buildSolidityInvokeTxWithFunding(t, signer, contract, nonce, calldata,
		[]wire.OutPoint{input},
		wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString(gasAsset),
			Amount: *indexercommon.NewDefaultDecimal(gasAmount - networkGasFeeAmount(t, evm.DefaultGasConfig().InvokeBaseGas)),
		}}},
		nil)
}

func buildSolidityInvokeTxWithFunding(t *testing.T, signer *btcec.PrivateKey, contract evm.ContractAddress,
	nonce uint64, calldata []byte, inputs []wire.OutPoint, funding wire.TxOut,
	changeOutputs []*wire.TxOut) *wire.MsgTx {

	t.Helper()
	tx, err := evm.BuildInvokeTx(evm.InvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      networkEVMInvokeGasLimit(),
		CallNonce:     nonce,
		Calldata:      calldata,
		Funding:       funding,
		Inputs:        inputs,
		ChangeOutputs: changeOutputs,
	})
	require.NoError(t, err)
	_, _, redeemScript, controlBlock := testCallerTaprootScript(t, signer)
	signTemplateTaprootInputs(t, tx, signer, redeemScript, controlBlock)
	return tx
}

func buildPassthroughAssetTx(t *testing.T, signer *btcec.PrivateKey, input wire.OutPoint,
	assetName string, amount int64, pkScript []byte) *wire.MsgTx {

	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: input,
	})
	tx.AddTxOut(wire.NewTxOut(0, testWireAsset(assetName, amount), pkScript))
	_, _, redeemScript, controlBlock := testCallerTaprootScript(t, signer)
	signTemplateTaprootInputs(t, tx, signer, redeemScript, controlBlock)
	return tx
}

func buildContractAssetDepositTx(t *testing.T, signer *btcec.PrivateKey, input wire.OutPoint,
	contract evm.ContractAddress, assets wire.TxAssets) *wire.MsgTx {

	t.Helper()
	contractOut, err := evm.NewContractTxOut(0, assets, contract)
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: input,
	})
	tx.AddTxOut(contractOut)
	_, _, redeemScript, controlBlock := testCallerTaprootScript(t, signer)
	signTemplateTaprootInputs(t, tx, signer, redeemScript, controlBlock)
	return tx
}

func collectSpendableOutPoints(t *testing.T, tx *wire.MsgTx, outputs []*wire.TxOut) []wire.OutPoint {
	t.Helper()
	if len(outputs) == 0 {
		return nil
	}
	txHash := tx.TxHash()
	result := make([]wire.OutPoint, 0, len(outputs))
	for vout, txOut := range tx.TxOut {
		for _, want := range outputs {
			if want == nil {
				continue
			}
			if bytes.Equal(txOut.PkScript, want.PkScript) && txOut.Value == want.Value &&
				(&txOut.Assets).Equal(want.Assets) {
				result = append(result, wire.OutPoint{Hash: txHash, Index: uint32(vout)})
				break
			}
		}
	}
	require.Len(t, result, len(outputs))
	return result
}

func sendTx(t *testing.T, node *rpctest.Harness, tx *wire.MsgTx) *chainhash.Hash {
	t.Helper()
	txHash, err := node.Client.SendRawTransaction(tx, true)
	require.NoError(t, err)
	require.Equal(t, tx.TxHash(), *txHash)
	return txHash
}

func sendAndMineTx(t *testing.T, node *rpctest.Harness, nodes []*rpctest.Harness, tx *wire.MsgTx, minHeight int32) {
	t.Helper()
	txHash := sendTx(t, node, tx)
	generateOrWaitBlockAtLeast(t, node, nodes, minHeight)
	verbose, err := node.Client.GetRawTransactionVerbose(txHash)
	require.NoError(t, err)
	require.GreaterOrEqual(t, verbose.Confirmations, uint64(1))
}

func requireAssetSummaryAmount(t *testing.T, node *rpctest.Harness, address, assetName, amount string) {
	t.Helper()
	var (
		lastSummary map[string]string
		lastErr     error
	)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := fetchAssetSummary(node, address)
		lastSummary, lastErr = summary, err
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if summary[assetName] == amount {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.Equal(t, amount, lastSummary[assetName], "address=%s asset=%s summary=%v", address, assetName, lastSummary)
}

func requirePositiveAssetSummary(t *testing.T, node *rpctest.Harness, address, assetName string) {
	t.Helper()
	var (
		lastSummary map[string]string
		lastErr     error
	)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := fetchAssetSummary(node, address)
		lastSummary, lastErr = summary, err
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		value, ok := summary[assetName]
		if ok && value != "" && value != "0" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.NotEmpty(t, lastSummary[assetName], "address=%s asset=%s summary=%v", address, assetName, lastSummary)
}

func fetchAssetSummary(node *rpctest.Harness, address string) (map[string]string, error) {
	baseURL, err := node.IndexerURL("testnet")
	if err != nil {
		return nil, err
	}
	resp, err := http.Get(baseURL + "/v3/address/summary/" + address)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected indexer status %d", resp.StatusCode)
	}

	var out indexerwire.AssetSummaryRespV3
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("indexer response code %d: %s", out.Code, out.Msg)
	}
	result := make(map[string]string)
	for _, asset := range out.Data {
		if asset == nil {
			continue
		}
		result[asset.AssetName.String()] = asset.Amount
	}
	return result, nil
}

func networkGasFunding(t *testing.T, gasAsset string, amount int64) wire.AssetInfo {
	t.Helper()
	return wire.AssetInfo{Name: *wire.NewAssetNameFromString(gasAsset), Amount: *indexercommon.NewDefaultDecimal(amount)}
}

func networkDecimalFunding(t *testing.T, assetName, amount string, precision int) wire.AssetInfo {
	t.Helper()
	decimal, err := indexercommon.NewDecimalFromString(amount, precision)
	require.NoError(t, err)
	return wire.AssetInfo{Name: *wire.NewAssetNameFromString(assetName), Amount: *decimal}
}

func testSpendAssetOutput(assetName string, amount int64, pkScript []byte) *wire.TxOut {
	return wire.NewTxOut(1000, testWireAsset(assetName, amount), pkScript)
}

func testWireDecimalAsset(t *testing.T, name, amount string, precision int) wire.TxAssets {
	t.Helper()
	assetName := wire.NewAssetNameFromString(name)
	decimal, err := indexercommon.NewDecimalFromString(amount, precision)
	require.NoError(t, err)
	return wire.TxAssets{{Name: *assetName, Amount: *decimal}}
}

func testDisplayAssetWithPrecision(name, amount string, precision int) *indexercommon.DisplayAsset {
	assetName := indexercommon.NewAssetNameFromString(name)
	return &indexercommon.DisplayAsset{
		AssetName: *assetName,
		Amount:    amount,
		Precision: precision,
	}
}

func runNetworkSolcStandardJSON(t *testing.T, solc string, encoded []byte) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), networkSolidityCompileTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, solc, "--standard-json")
	cmd.Stdin = bytes.NewReader(encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second

	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("solc timed out after %s: %s", networkSolidityCompileTimeout, string(out))
	}
	require.NoErrorf(t, err, "solc failed: %s", string(out))
	return out
}

func testWitnessAddress(t *testing.T, key *btcec.PrivateKey) string {
	t.Helper()
	hash160 := btcutil.Hash160(key.PubKey().SerializeCompressed())
	addr, err := btcutil.NewAddressWitnessPubKeyHash(hash160, &chaincfg.TestNetParams)
	require.NoError(t, err)
	return addr.EncodeAddress()
}

func mustNetworkEVMAddressFromKey(t *testing.T, key *btcec.PrivateKey) evm.EVMAddress {
	t.Helper()
	addr, err := evm.EVMAddressFromPublicKey(key.PubKey().SerializeCompressed())
	require.NoError(t, err)
	return addr
}

const solidityNetworkCounterSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract Counter {
    uint256 public value;
    address public lastCaller;

    event Incremented(address indexed caller, uint256 delta, uint256 value);

    function incrementBy(uint256 delta) public returns (uint256) {
        require(delta > 0, "delta is zero");
        value += delta;
        lastCaller = msg.sender;
        emit Incremented(msg.sender, delta, value);
        return value;
    }
}
`

const solidityNetworkMiniERC20Source = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

contract MiniERC20 {
    string public name;
    string public symbol;
    uint8 public decimals;
    uint256 public totalSupply;

    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);

    constructor(string memory name_, string memory symbol_, uint8 decimals_, uint256 initialSupply) {
        name = name_;
        symbol = symbol_;
        decimals = decimals_;
        _mint(msg.sender, initialSupply);
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        _transfer(msg.sender, to, amount);
        return true;
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        uint256 allowed = allowance[from][msg.sender];
        require(allowed >= amount, "allowance");
        allowance[from][msg.sender] = allowed - amount;
        _transfer(from, to, amount);
        return true;
    }

    function _transfer(address from, address to, uint256 amount) internal {
        require(to != address(0), "zero to");
        uint256 bal = balanceOf[from];
        require(bal >= amount, "balance");
        balanceOf[from] = bal - amount;
        balanceOf[to] += amount;
        emit Transfer(from, to, amount);
    }

    function _mint(address to, uint256 amount) internal {
        require(to != address(0), "zero mint");
        totalSupply += amount;
        balanceOf[to] += amount;
        emit Transfer(address(0), to, amount);
    }
}
`

const solidityNetworkVaultSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

interface ISatoshiNetAsset {
    function transferAsset(string calldata assetName, string calldata to, string calldata amount, bytes calldata extraData) external returns (bool);
}

contract SatoshiNetTimelockVault {
    ISatoshiNetAsset constant ASSET = ISatoshiNetAsset(0x0000000000000000000000000000000000534E01);

    string public assetName;
    string public recipient;
    string public amount;
    uint256 public releaseHeight;
    bool public released;

    event Released(string assetName, string recipient, string amount);

    constructor(string memory assetName_, string memory recipient_, string memory amount_, uint256 releaseHeight_) {
        require(bytes(assetName_).length != 0, "asset");
        require(bytes(recipient_).length != 0, "recipient");
        require(bytes(amount_).length != 0, "amount");
        require(releaseHeight_ > block.number, "height");
        assetName = assetName_;
        recipient = recipient_;
        amount = amount_;
        releaseHeight = releaseHeight_;
    }

    function release() external {
        require(!released, "released");
        require(block.number >= releaseHeight, "not due");
        released = true;
        require(ASSET.transferAsset(assetName, recipient, amount, ""), "transfer");
        emit Released(assetName, recipient, amount);
    }

    function deposit() external payable {}
}
`
