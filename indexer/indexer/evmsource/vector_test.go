package evmsource

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Captured from the approved native solc 0.8.30 binary with nativeSource and
// the fixed standard-json profile. This test checks deployment proof and byte
// matching independently of the platform's compiler resource limits.
func TestSourceDeploymentProofAndCompiledVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/solc-0.8.30-example.json")
	require.NoError(t, err)
	var output struct {
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
			EVM struct {
				Bytecode struct {
					Object string `json:"object"`
				} `json:"bytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	}
	require.NoError(t, json.Unmarshal(raw, &output))
	compiled := output.Contracts["Contract.sol"]["Example"]
	code, err := hex.DecodeString(compiled.EVM.Bytecode.Object)
	require.NoError(t, err)
	require.NotEmpty(t, code)
	a := artifact{ABI: compiled.ABI, InitCode: code}
	args := make([]byte, 32)
	args[31] = 42
	init := append(append([]byte(nil), code...), args...)
	gas := evm.DefaultGasConfig()
	limit := gas.DeployBaseGas + 100000
	fee, err := gas.ContractFundingFee(framework.ExecutionKindDeploy, limit, true, 1)
	require.NoError(t, err)
	deploy, address, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: contract.TestnetContractPrefix,
		Deployer:       "0x11112233445566778899aabbccddeeff00112233", DeployNonce: 1,
		GasLimit: limit, ContractContent: init,
		Funding: wire.TxOut{Assets: wire.TxAssets{{Name: *wire.NewAssetNameFromString(gas.GasAssetName), Amount: *fee}}},
	})
	require.NoError(t, err)
	resultScript, err := contract.ResultNullDataScript(contract.ResultPayload{Status: contract.ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	result := wire.NewMsgTx(2)
	result.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: deploy.TxHash(), Index: uint32(len(deploy.TxOut) - 1)}})
	result.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	block := &wire.MsgBlock{Transactions: []*wire.MsgTx{deploy, result}}
	source := contract.EVMSourceMetadata{Version: 1, Language: "Solidity", Source: nativeSource, ContractName: "Example",
		ContractAddress: address.EncodeAddress(), DeployTxID: deploy.TxID(), CompilerConfig: contract.DefaultEVMCompilerConfig(),
		ABI: a.ABI, ConstructorArgs: hex.EncodeToString(args), InitCodeHash: hex.EncodeToString(crypto.Keccak256(init))}
	verifier := Verifier{Params: &chaincfg.TestNetParams, GasConfig: gas,
		LoadBlock: func(string) (*wire.MsgBlock, int64, error) { return block, 1, nil }}
	txid := deploy.TxHash()
	content, err := verifier.deploymentContent(source, address, &txid)
	require.NoError(t, err)
	require.Equal(t, init, content)
	require.NoError(t, matchCompiledSource(source, content, a, args))
	for _, mode := range []string{"args", "abi", "hash", "init"} {
		t.Run(mode, func(t *testing.T) {
			next := source
			input, constructorArgs := append([]byte(nil), content...), args
			switch mode {
			case "args":
				constructorArgs = []byte{0}
			case "abi":
				next.ABI = json.RawMessage(`[]`)
			case "hash":
				next.InitCodeHash = "00"
			case "init":
				input[0] ^= 1
			}
			require.Error(t, matchCompiledSource(next, input, a, constructorArgs))
		})
	}
	block.Transactions = []*wire.MsgTx{deploy}
	_, err = verifier.deploymentContent(source, address, &txid)
	require.ErrorContains(t, err, "successful canonical Result")
	block.Transactions = []*wire.MsgTx{deploy, result}
	result.TxIn[0].PreviousOutPoint.Index--
	_, err = verifier.deploymentContent(source, address, &txid)
	require.ErrorContains(t, err, "successful canonical Result")
	result.TxIn[0].PreviousOutPoint.Index++
	wrongAddress, err := contract.NewContractAddress(contract.TestnetContractPrefix, contract.AddressVersionV1, contract.ContractTypeEVM, contract.EVMAddress{1})
	require.NoError(t, err)
	_, err = verifier.deploymentContent(source, wrongAddress, &txid)
	require.Error(t, err)
	value, err := json.Marshal(source)
	require.NoError(t, err)
	record := &wire.DKVSRecord{Key: "/contract/evm/source/" + source.ContractAddress, Value: value}
	verifier.Params = &chaincfg.MainNetParams
	require.ErrorContains(t, verifier.Verify(record), "network")
}
