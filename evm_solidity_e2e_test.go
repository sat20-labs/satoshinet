package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	gethcommon "github.com/ethereum/go-ethereum/common"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestEVMEndToEndSolidityCounter(t *testing.T) {
	compiled := compileSolidityContract(t, solidityCounterSource, "Counter")
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
			Caller: caller,
			Target: evm.ContractAddressHash(contract),
			CallID: fmt.Sprintf("counter-increment-%d", i),
			Input:  input,
			Gas:    200000,
			Block:  block,
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
	compiled := compileSolidityContract(t, solidityMiniERC20Source, "MiniERC20")
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
		Caller: deployer,
		Target: evm.ContractAddressHash(contract),
		CallID: "erc20-transfer-alice",
		Input:  transferAlice,
		Gas:    250000,
		Block:  block,
	})
	require.NoError(t, call.Err)
	require.Equal(t, evm.ResultStatusSuccess, call.Status)

	approveBob := packSolidityMethod(t, compiled.ABI, "approve", gethcommon.Address(evm.GethAddress(bob)), big.NewInt(20_000_000))
	call = rt.Call(evm.CallRequest{
		Caller: alice,
		Target: evm.ContractAddressHash(contract),
		CallID: "erc20-approve-bob",
		Input:  approveBob,
		Gas:    250000,
		Block:  block,
	})
	require.NoError(t, call.Err)
	require.Equal(t, evm.ResultStatusSuccess, call.Status)

	transferFromAlice := packSolidityMethod(t, compiled.ABI, "transferFrom",
		gethcommon.Address(evm.GethAddress(alice)),
		gethcommon.Address(evm.GethAddress(bob)),
		big.NewInt(12_500_000),
	)
	call = rt.Call(evm.CallRequest{
		Caller: bob,
		Target: evm.ContractAddressHash(contract),
		CallID: "erc20-transfer-from",
		Input:  transferFromAlice,
		Gas:    300000,
		Block:  block,
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
	compiled := compileSolidityContract(t, solidityVaultSource, "SatoshiNetTimelockVault")
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
		{
			OutPoint: evm.OutPoint{TxID: strings.Repeat("11", 32), Vout: 0},
			Contract: contract,
			Assets:   e2EAsset(gasAsset, 2000000),
			Height:   300,
		},
		{
			OutPoint: evm.OutPoint{TxID: strings.Repeat("22", 32), Vout: 0},
			Contract: contract,
			Assets:   e2EDecimalAsset(vaultAsset, mustE2EDecimal(t, "2.50")),
			Height:   300,
		},
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
			return evm.ResultOutputsFromTx(tx, evm.TestnetContractPrefix, solidityE2ERecipientResolver)
		},
	})
	require.NoError(t, err)
	require.Len(t, build.ResultTxs, 1)
	require.Len(t, build.Execution.Records, 1)
	require.Len(t, rt.State.Triggers(), 0)

	index := newSolidityE2EAssetIndex(t, contract)
	for _, tx := range build.ResultTxs {
		index.ApplyResultTx(t, tx)
	}
	require.Equal(t, "1.25", index.AssetBalance(recipientAddr, vaultAsset).String())
	require.Equal(t, "1.25", index.AssetBalance(contract.MustEncode(), vaultAsset).String())
	gasChange := 2000000 - build.Execution.Records[0].GasUsed - cfg.ResultPackingFee
	require.Equal(t, scommon.NewDefaultDecimal(int64(gasChange)).String(),
		index.AssetBalance(contract.MustEncode(), gasAsset).String())
}

type compiledSolidityContract struct {
	ABI      gethabi.ABI
	Bytecode []byte
}

func compileSolidityContract(t *testing.T, source, contractName string) compiledSolidityContract {
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
	cmd := exec.Command(solc, "--standard-json")
	cmd.Stdin = bytes.NewReader(encoded)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "solc failed: %s", string(out))

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
	return compiledSolidityContract{ABI: abiValue, Bytecode: bytecode}
}

func deployCompiledSolidityTx(t *testing.T, rt *evm.Runtime, caller evm.EVMAddress, compiled compiledSolidityContract,
	constructorArgs []interface{}, nonce uint64, block evm.BlockContext) evm.ContractAddress {
	t.Helper()
	initCode := solidityDeployCode(t, compiled, constructorArgs)
	tx, contract, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: evm.TestnetContractPrefix,
		Caller:         caller,
		GasLimit:       block.GasLimit,
		DeployNonce:    nonce,
		InitCode:       initCode,
		Funding: evm.TxFunding{Assets: []evm.AssetAmount{{
			AssetName: evm.DefaultGasConfig().GasAssetName,
			Amount:    scommon.NewDefaultDecimal(int64(block.GasLimit)),
		}}},
		Inputs: []wire.OutPoint{{Hash: mustSolidityE2EHash(t, byte(nonce)), Index: 0}},
	})
	require.NoError(t, err)

	parsed, err := evm.ParseTx(tx, evm.StandardContractScriptResolver(evm.TestnetContractPrefix))
	require.NoError(t, err)
	require.NotNil(t, parsed.Deploy)
	require.Equal(t, initCode, parsed.Deploy.InitCode)
	require.GreaterOrEqual(t, len(tx.TxOut), 2)

	executor := evm.NewBlockExecutor(evm.BlockExecutionRequest{
		Runtime:        rt,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      evm.DefaultGasConfig(),
		Block:          block,
		ResolveCaller:  e2EFixedCaller(caller),
	})
	require.NoError(t, executor.ExecuteTx(tx))
	pending := executor.PendingRecords()
	require.Len(t, pending, 1)
	require.Equal(t, evm.ResultStatusSuccess, pending[0].Status)
	require.True(t, contract.Equal(pending[0].Contract))
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
		Caller: caller,
		Target: target,
		CallID: "view-" + method,
		Input:  input,
		Gas:    200000,
		Block:  block,
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
	outputs, err := evm.ResultOutputsFromTx(tx, evm.TestnetContractPrefix, solidityE2ERecipientResolver)
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

const solidityCounterSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

// Counter verifies basic Solidity deployment, persistent storage, msg.sender,
// event emission, and calls from multiple EVM accounts.
contract Counter {
    uint256 public value;
    address public lastCaller;

    event Incremented(address indexed caller, uint256 delta, uint256 value);
    event Reset(address indexed caller);

    function increment() external returns (uint256) {
        return incrementBy(1);
    }

    function incrementBy(uint256 delta) public returns (uint256) {
        require(delta > 0, "delta is zero");
        value += delta;
        lastCaller = msg.sender;
        emit Incremented(msg.sender, delta, value);
        return value;
    }

    function reset() external {
        value = 0;
        lastCaller = msg.sender;
        emit Reset(msg.sender);
    }
}
`

const solidityMiniERC20Source = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

// MiniERC20 is a compact ERC20-compatible token used to exercise common
// Ethereum application behavior: constructor mint, transfer, approve, and
// transferFrom with allowance accounting.
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

const solidityVaultSource = `// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

interface ISatoshiNetAsset {
    function transferAsset(string calldata assetName, string calldata to, string calldata amount, bytes calldata extraData) external returns (bool);
}

interface ISatoshiNetTrigger {
    function registerHeightTrigger(string calldata id, uint256 height, uint256 gasLimit, bytes calldata callData) external returns (bool);
}

// SatoshiNetTimelockVault verifies the SatoshiNet-specific EVM extensions:
// constructor-time trigger registration, height-trigger execution, and native
// multi-asset settlement through the asset precompile.
contract SatoshiNetTimelockVault {
    ISatoshiNetAsset constant ASSET = ISatoshiNetAsset(0x0000000000000000000000000000000000534E01);
    ISatoshiNetTrigger constant TRIGGER = ISatoshiNetTrigger(0x0000000000000000000000000000000000534e02);

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

        bytes memory callData = abi.encodeWithSelector(this.release.selector);
        require(TRIGGER.registerHeightTrigger("vault-release", releaseHeight_, 800000, callData), "trigger");
    }

    function release() external {
        require(!released, "released");
        require(block.number >= releaseHeight, "not due");
        released = true;
        require(ASSET.transferAsset(assetName, recipient, amount, ""), "transfer");
        emit Released(assetName, recipient, amount);
    }
}
`
