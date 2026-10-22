//go:build legacy_evm_e2e

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	gethcommon "github.com/ethereum/go-ethereum/common"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestEVMEndToEndSolidityCounter(t *testing.T) {
	compiled := loadSolidityContract(t, "Counter")
	rt := evm.NewRuntime(nil)
	deployer := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	callers := []evm.EVMAddress{
		mustE2EEVMAddress(t, "0x2222222222222222222222222222222222222222"),
		mustE2EEVMAddress(t, "0x3333333333333333333333333333333333333333"),
		mustE2EEVMAddress(t, "0x4444444444444444444444444444444444444444"),
	}
	block := evm.BlockContext{Number: 200, Time: 1710000000, GasLimit: 3000000, FixedGasPrice: 1}

	contract := deployCompiledSolidityTx(t, rt, deployer, compiled, nil, 1, block)

	for i, caller := range callers {
		input := packSolidityMethod(t, compiled.ABI, "incrementBy", big.NewInt(int64(i+1)))
		call := rt.Call(evm.CallRequest{
			CallerAddress: caller.String(),
			TargetAddress: contract.MustEncode(),
			CallID:        fmt.Sprintf("counter-increment-%d", i),
			Input:         input,
			Gas:           200000,
			Block:         block,
		})
		require.NoError(t, call.Err)
		require.Equal(t, evm.ResultStatusSuccess, call.Status)
	}

	value := callSolidityViewUint256(t, rt, evm.ContractAddressHash(contract), callers[0], compiled.ABI, "value", block)
	require.Equal(t, uint64(6), value.Uint64())
	lastCaller := callSolidityViewAddress(t, rt, evm.ContractAddressHash(contract), callers[0], compiled.ABI, "lastCaller", block)
	require.Equal(t, gethcommon.Address(evm.GethAddress(callers[2])), lastCaller)
}

func TestEVMEndToEndSolidityMiniERC20(t *testing.T) {
	compiled := loadSolidityContract(t, "MiniERC20")
	rt := evm.NewRuntime(nil)
	deployer := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	alice := mustE2EEVMAddress(t, "0x2222222222222222222222222222222222222222")
	bob := mustE2EEVMAddress(t, "0x3333333333333333333333333333333333333333")
	block := evm.BlockContext{Number: 201, Time: 1710000000, GasLimit: 5000000, FixedGasPrice: 1}
	initialSupply := new(big.Int).Mul(big.NewInt(1000), big.NewInt(1_000_000))

	contract := deployCompiledSolidityTx(t, rt, deployer, compiled,
		[]interface{}{"SatoshiNet Test Token", "SNT", uint8(6), initialSupply}, 2, block)

	transferAlice := packSolidityMethod(t, compiled.ABI, "transfer", gethcommon.Address(evm.GethAddress(alice)), big.NewInt(125_000_000))
	call := rt.Call(evm.CallRequest{
		CallerAddress: deployer.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "erc20-transfer-alice",
		Input:         transferAlice,
		Gas:           250000,
		Block:         block,
	})
	require.NoError(t, call.Err)
	require.Equal(t, evm.ResultStatusSuccess, call.Status)

	approveBob := packSolidityMethod(t, compiled.ABI, "approve", gethcommon.Address(evm.GethAddress(bob)), big.NewInt(20_000_000))
	call = rt.Call(evm.CallRequest{
		CallerAddress: alice.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "erc20-approve-bob",
		Input:         approveBob,
		Gas:           250000,
		Block:         block,
	})
	require.NoError(t, call.Err)
	require.Equal(t, evm.ResultStatusSuccess, call.Status)

	transferFromAlice := packSolidityMethod(t, compiled.ABI, "transferFrom",
		gethcommon.Address(evm.GethAddress(alice)),
		gethcommon.Address(evm.GethAddress(bob)),
		big.NewInt(12_500_000),
	)
	call = rt.Call(evm.CallRequest{
		CallerAddress: bob.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "erc20-transfer-from",
		Input:         transferFromAlice,
		Gas:           300000,
		Block:         block,
	})
	require.NoError(t, call.Err)
	require.Equal(t, evm.ResultStatusSuccess, call.Status)

	require.Equal(t, uint64(112_500_000), callERC20Balance(t, rt, evm.ContractAddressHash(contract), alice, deployer, compiled.ABI, block).Uint64())
	require.Equal(t, uint64(12_500_000), callERC20Balance(t, rt, evm.ContractAddressHash(contract), bob, deployer, compiled.ABI, block).Uint64())
	require.Equal(t, uint64(875_000_000), callERC20Balance(t, rt, evm.ContractAddressHash(contract), deployer, deployer, compiled.ABI, block).Uint64())
}

func TestEVMEndToEndSolidityVaultTriggerAssetSettlement(t *testing.T) {
	const (
		gasAsset      = "ordx:ft:gas"
		vaultAsset    = "ordx:ft:usd"
		recipientAddr = "tb1qvaultrecipient"
	)
	compiled := loadSolidityContract(t, "SatoshiNetTimelockVault")
	deployer := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, deployer, 3)
	require.NoError(t, err)
	block := evm.BlockContext{Number: 300, Time: 1710000000, GasLimit: 5000000, FixedGasPrice: 1}
	cfg := evm.GasConfig{
		GasAssetName:     gasAsset,
		FixedGasPrice:    1,
		ResultPackingFee: 100,
		MaxGasPerInvoke:  1000000,
		MaxGasPerBlock:   3000000,
	}
	rt := evm.NewRuntime(nil)

	deployed := deployCompiledSolidityTx(t, rt, deployer, compiled,
		[]interface{}{vaultAsset, recipientAddr, "1.25", big.NewInt(301)}, 3, block)
	require.True(t, contract.Equal(deployed))

	triggers := rt.State.Triggers()
	require.Len(t, triggers, 1)
	require.Equal(t, "vault-release", triggers[0].ID)
	require.Equal(t, int64(301), triggers[0].Height)

	releaseBlock := evm.BlockContext{Number: 301, Time: 1710000060, GasLimit: 5000000, FixedGasPrice: 1}
	inputs := []evm.UTXO{
		contractframework.UTXOFromTxOutput(
			evm.OutPoint{TxID: strings.Repeat("11", 32), Vout: 0}, contract, 300,
			&wire.TxOut{Assets: e2EAsset(gasAsset, 2000000)}),
		contractframework.UTXOFromTxOutput(
			evm.OutPoint{TxID: strings.Repeat("22", 32), Vout: 0}, contract, 300,
			&wire.TxOut{Assets: e2EDecimalAsset(vaultAsset, mustE2EDecimal(t, "2.50"))}),
	}
	managed, ok := rt.State.ManagedBalance(contract)
	require.True(t, ok)
	for _, input := range inputs {
		require.NoError(t, managed.Credit(input.PhysicalValue(), input.TxAssets()))
	}
	build, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
		Runtime:        rt,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          releaseBlock,
		ContractUTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return inputs, nil
		},
		ResolveScript: solidityE2EResultScriptResolver(contract),
		ResolveOutput: func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(tx, evm.TestnetContractPrefix,
				evmcommon.ParseContractPkScript, solidityE2ERecipientResolver)
		},
		AssetPrecision: func(name string) (int, bool) { return 2, name == vaultAsset },
	})
	require.NoError(t, err)
	require.Len(t, build.ResultTxs, 1)
	require.Len(t, build.Execution.Records, 1)
	require.Equal(t, evm.ResultStatusSuccess, build.Execution.Records[0].Status)
	require.Len(t, rt.State.Triggers(), 0)

	index := newSolidityE2EAssetIndex(t, contract)
	for _, tx := range build.ResultTxs {
		index.ApplyResultTx(t, tx)
	}
	require.Equal(t, "1.25", index.AssetBalance(recipientAddr, vaultAsset).String())
	require.Equal(t, "1.25", index.AssetBalance(contract.MustEncode(), vaultAsset).String())
	recordFee, err := contractframework.RecordResultGasFee(
		cfg, evm.SettlementPrecision(nil), build.Execution.Records[0])
	require.NoError(t, err)
	gasChange := scommon.NewDefaultDecimal(2000000).SubAlignPrecision(recordFee)
	require.Equal(t, gasChange.String(), index.AssetBalance(contract.MustEncode(), gasAsset).String())
}

type compiledSolidityContract struct {
	ABI      gethabi.ABI
	Bytecode []byte
}

// Solidity fixtures are compiled offline; E2E execution never invokes solc.
func loadSolidityContract(t *testing.T, contractName string) compiledSolidityContract {
	t.Helper()
	sourceName := contractName + ".sol"
	source, err := os.ReadFile(filepath.Join("testdata", "solidity", sourceName))
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join("testdata", "solidity", contractName+".json"))
	require.NoError(t, err)
	var fixture struct {
		SourceName       string          `json:"sourceName"`
		SourceSHA256     string          `json:"sourceSHA256"`
		ContractName     string          `json:"contractName"`
		CompilerVersion  string          `json:"compilerVersion"`
		EVMVersion       string          `json:"evmVersion"`
		OptimizerEnabled bool            `json:"optimizerEnabled"`
		OptimizerRuns    int             `json:"optimizerRuns"`
		ABI              json.RawMessage `json:"abi"`
		Bytecode         string          `json:"bytecode"`
	}
	require.NoError(t, json.Unmarshal(raw, &fixture))
	sum := sha256.Sum256(source)
	require.Equal(t, sourceName, fixture.SourceName)
	require.Equal(t, hex.EncodeToString(sum[:]), fixture.SourceSHA256, "stale Solidity fixture; regenerate it offline")
	require.Equal(t, contractName, fixture.ContractName)
	require.NotEmpty(t, fixture.CompilerVersion)
	require.Equal(t, "paris", fixture.EVMVersion)
	require.True(t, fixture.OptimizerEnabled)
	require.Equal(t, 200, fixture.OptimizerRuns)
	abiValue, err := gethabi.JSON(bytes.NewReader(fixture.ABI))
	require.NoError(t, err)
	bytecode, err := hex.DecodeString(strings.TrimPrefix(fixture.Bytecode, "0x"))
	require.NoError(t, err)
	require.NotEmpty(t, bytecode)
	return compiledSolidityContract{ABI: abiValue, Bytecode: bytecode}
}

func deployCompiledSolidityTx(t *testing.T, rt *evm.Runtime, caller evm.EVMAddress, compiled compiledSolidityContract,
	constructorArgs []interface{}, nonce uint64, block evm.BlockContext) evm.ContractAddress {
	t.Helper()
	initCode := solidityDeployCode(t, compiled, constructorArgs)
	cfg := evm.DefaultGasConfig()
	gasLimit := block.GasLimit
	if cfg.MaxGasPerInvoke > 0 && gasLimit > cfg.MaxGasPerInvoke {
		gasLimit = cfg.MaxGasPerInvoke
	}
	if gasLimit < cfg.DeployBaseGas {
		gasLimit = cfg.DeployBaseGas
	}
	fundingGas, err := cfg.ContractFundingFee(evm.ExecutionKindDeploy, gasLimit, true, block.Number)
	require.NoError(t, err)
	tx, contract, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix:  evm.TestnetContractPrefix,
		Deployer:        caller.String(),
		GasLimit:        gasLimit,
		DeployNonce:     nonce,
		ContractContent: initCode,
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString(cfg.GasAssetName),
			Amount: *fundingGas.Clone(),
		}}},
		Inputs: []wire.OutPoint{{Hash: mustSolidityE2EHash(t, byte(nonce)), Index: 0}},
	})
	require.NoError(t, err)

	parsed, err := evm.ParseTx(tx, evm.StandardContractScriptResolver(evm.TestnetContractPrefix))
	require.NoError(t, err)
	require.NotNil(t, parsed.Deploy)
	require.Equal(t, initCode, parsed.Deploy.ContractContent)
	require.GreaterOrEqual(t, len(tx.TxOut), 2)

	executed, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
		Txs:            []*wire.MsgTx{tx},
		Runtime:        rt,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          block,
		ResolveCaller:  e2EFixedCaller(caller),
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 1)
	require.Equal(t, evm.ResultStatusSuccess, executed.Records[0].Status)
	require.True(t, contract.Equal(executed.Records[0].Contract))
	return contract
}

func solidityDeployCode(t *testing.T, compiled compiledSolidityContract, constructorArgs []interface{}) []byte {
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

func packSolidityMethod(t *testing.T, abiValue gethabi.ABI, method string, args ...interface{}) []byte {
	t.Helper()
	input, err := abiValue.Pack(method, args...)
	require.NoError(t, err)
	return input
}

func callSolidityViewUint256(t *testing.T, rt *evm.Runtime, target evm.EVMAddress, caller evm.EVMAddress,
	abiValue gethabi.ABI, method string, block evm.BlockContext, args ...interface{}) *big.Int {
	t.Helper()
	ret := callSolidityView(t, rt, target, caller, abiValue, method, block, args...)
	values, err := abiValue.Unpack(method, ret)
	require.NoError(t, err)
	require.Len(t, values, 1)
	value, ok := values[0].(*big.Int)
	require.Truef(t, ok, "unexpected %s return type %T", method, values[0])
	return value
}

func callSolidityViewAddress(t *testing.T, rt *evm.Runtime, target evm.EVMAddress, caller evm.EVMAddress,
	abiValue gethabi.ABI, method string, block evm.BlockContext, args ...interface{}) gethcommon.Address {
	t.Helper()
	ret := callSolidityView(t, rt, target, caller, abiValue, method, block, args...)
	values, err := abiValue.Unpack(method, ret)
	require.NoError(t, err)
	require.Len(t, values, 1)
	value, ok := values[0].(gethcommon.Address)
	require.Truef(t, ok, "unexpected %s return type %T", method, values[0])
	return value
}

func callSolidityView(t *testing.T, rt *evm.Runtime, target evm.EVMAddress, caller evm.EVMAddress,
	abiValue gethabi.ABI, method string, block evm.BlockContext, args ...interface{}) []byte {
	t.Helper()
	input := packSolidityMethod(t, abiValue, method, args...)
	call := rt.Call(evm.CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: target.String(),
		CallID:        "view-" + method,
		Input:         input,
		Gas:           200000,
		Block:         block,
	})
	require.NoError(t, call.Err)
	require.Equal(t, evm.ResultStatusSuccess, call.Status)
	return call.ReturnData
}

func callERC20Balance(t *testing.T, rt *evm.Runtime, token evm.EVMAddress, owner evm.EVMAddress,
	caller evm.EVMAddress, abiValue gethabi.ABI, block evm.BlockContext) *big.Int {
	t.Helper()
	return callSolidityViewUint256(t, rt, token, caller, abiValue, "balanceOf", block,
		gethcommon.Address(evm.GethAddress(owner)))
}

func solidityE2EResultScriptResolver(contract evm.ContractAddress) evm.ResultRecipientScriptResolver {
	return func(output evm.ResultOutput) ([]byte, error) {
		if output.To == contract.MustEncode() {
			return evm.ContractPkScript(contract)
		}
		return []byte{0x51}, nil
	}
}

func solidityE2ERecipientResolver(pkScript []byte) (string, bool, error) {
	if len(pkScript) == 1 && pkScript[0] == 0x51 {
		return "tb1qvaultrecipient", true, nil
	}
	return "", false, nil
}

func mustE2EDecimal(t *testing.T, amount string) *scommon.Decimal {
	t.Helper()
	decimal, err := evm.ParseDecimalAmountString(amount)
	require.NoError(t, err)
	return decimal
}

func mustSolidityE2EHash(t *testing.T, seed byte) chainhash.Hash {
	t.Helper()
	var h chainhash.Hash
	h[0] = seed
	return h
}

type solidityE2EAssetIndex struct {
	contract evm.ContractAddress
	assets   map[string]map[string]*scommon.Decimal
}

func newSolidityE2EAssetIndex(t *testing.T, contract evm.ContractAddress) *solidityE2EAssetIndex {
	t.Helper()
	return &solidityE2EAssetIndex{
		contract: contract,
		assets:   make(map[string]map[string]*scommon.Decimal),
	}
}

func (i *solidityE2EAssetIndex) ApplyResultTx(t *testing.T, tx *wire.MsgTx) {
	t.Helper()
	outputs, err := contractframework.ResultOutputsFromTx(tx, evm.TestnetContractPrefix,
		evmcommon.ParseContractPkScript, solidityE2ERecipientResolver)
	require.NoError(t, err)
	for _, output := range outputs {
		if output.Value > 0 {
			i.add(output.To, evmcommon.SatoshiAssetName, scommon.NewDefaultDecimal(int64(output.Value)))
		}
		for _, asset := range output.Assets {
			i.add(output.To, asset.Name.String(), asset.Amount.Clone())
		}
	}
}

func (i *solidityE2EAssetIndex) AssetBalance(address, assetName string) *scommon.Decimal {
	if byAsset := i.assets[address]; byAsset != nil {
		if balance := byAsset[assetName]; balance != nil {
			return balance.Clone()
		}
	}
	return scommon.NewDefaultDecimal(0)
}

func (i *solidityE2EAssetIndex) add(address, assetName string, amount *scommon.Decimal) {
	if amount == nil || amount.IsZero() {
		return
	}
	if i.assets[address] == nil {
		i.assets[address] = make(map[string]*scommon.Decimal)
	}
	if i.assets[address][assetName] == nil {
		i.assets[address][assetName] = amount.Clone()
		return
	}
	i.assets[address][assetName] = i.assets[address][assetName].AddAlignPrecision(amount)
}
