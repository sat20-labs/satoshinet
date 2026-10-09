package evmsource

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	idx "github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const nativeSource = `pragma solidity ^0.8.30;
contract Example {
 uint public immutable born;
 constructor(uint n) { born = n + block.number; }
}`

func nativeArtifact(t *testing.T) (string, artifact) {
	t.Helper()
	path := os.Getenv("SATOSHINET_TEST_SOLC_0830")
	if path == "" {
		t.Skip("set SATOSHINET_TEST_SOLC_0830 to an approved native solc 0.8.30 binary")
	}
	a, err := compile(path, nativeSource, "Example")
	require.NoError(t, err)
	return path, a
}

func TestNativeSourceCompilationAndAdmission(t *testing.T) {
	path, a := nativeArtifact(t)
	gas := evm.DefaultGasConfig()
	limit := gas.DeployBaseGas + 100000
	fee, err := gas.ContractFundingFee(framework.ExecutionKindDeploy, limit, true, 1)
	require.NoError(t, err)
	args := make([]byte, 32)
	args[31] = 42
	init := append(append([]byte(nil), a.InitCode...), args...)
	deploy, address, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: contract.TestnetContractPrefix,
		Deployer:       "0x11112233445566778899aabbccddeeff00112233", DeployNonce: 1,
		GasLimit: limit, ContractContent: init,
		Inputs:  []wire.OutPoint{{Hash: chainhash.Hash{1}}},
		Funding: wire.TxOut{Assets: wire.TxAssets{{Name: *wire.NewAssetNameFromString(gas.GasAssetName), Amount: *fee}}},
	})
	require.NoError(t, err)
	resultScript, err := contract.ResultNullDataScript(contract.ResultPayload{Status: contract.ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	result := wire.NewMsgTx(2)
	result.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: deploy.TxHash(), Index: uint32(len(deploy.TxOut) - 1)}})
	result.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	block := &wire.MsgBlock{Transactions: []*wire.MsgTx{deploy, result}}
	source := contract.EVMSourceMetadata{
		Version: 1, Language: "Solidity", ContractAddress: address.EncodeAddress(), DeployTxID: deploy.TxID(),
		ContractName: "Example", Source: nativeSource, CompilerConfig: contract.DefaultEVMCompilerConfig(),
		ConstructorArgs: hex.EncodeToString(args), ABI: a.ABI, InitCodeHash: hex.EncodeToString(crypto.Keccak256(init)),
	}
	verifier := Verifier{CompilerPath: path, Params: &chaincfg.TestNetParams, GasConfig: gas,
		LoadBlock: func(txid string) (*wire.MsgBlock, int64, error) {
			if txid != deploy.TxID() {
				return nil, 0, fmt.Errorf("not canonical")
			}
			return block, 1, nil
		}}
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	sign := func(metadata contract.EVMSourceMetadata) *wire.DKVSRecord {
		value, err := json.Marshal(metadata)
		require.NoError(t, err)
		record, err := dkvs.NewRecord("/contract/evm/source/"+address.EncodeAddress(), value, priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 1})
		require.NoError(t, err)
		hash := dkvs.SigningHash(record)
		record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
		return record
	}
	record := sign(source)
	require.NoError(t, verifier.Verify(record))
	for _, mode := range []string{"source", "args", "abi", "hash", "failed-deploy", "funding", "txid", "network", "runtime-claim", "compiler-profile"} {
		t.Run(mode, func(t *testing.T) {
			next := source
			check := verifier
			switch mode {
			case "source":
				next.Source = `pragma solidity ^0.8.30; contract Example {}`
			case "args":
				next.ConstructorArgs = "00"
			case "abi":
				next.ABI = json.RawMessage(`[]`)
			case "hash":
				next.InitCodeHash = "00"
			case "txid":
				next.DeployTxID = chainhash.Hash{2}.String()
			case "network":
				check.Params = &chaincfg.MainNetParams
			case "runtime-claim":
				next.RuntimeCodeHash = "00"
			case "compiler-profile":
				next.CompilerConfig.Optimizer.Runs++
			case "failed-deploy":
				check.LoadBlock = func(string) (*wire.MsgBlock, int64, error) {
					return &wire.MsgBlock{Transactions: []*wire.MsgTx{deploy}}, 1, nil
				}
			case "funding":
				bad := deploy.Copy()
				bad.TxOut[len(bad.TxOut)-1].Assets[0].Amount = *idx.NewDefaultDecimal(0)
				next.DeployTxID = bad.TxID()
				check.LoadBlock = func(string) (*wire.MsgBlock, int64, error) {
					return &wire.MsgBlock{Transactions: []*wire.MsgTx{bad, result}}, 1, nil
				}
			}
			require.Error(t, check.Verify(sign(next)))
		})
	}
	kv := db.NewKVDB(t.TempDir())
	require.NotNil(t, kv)
	defer kv.Close()
	store := dkvs.New(kv, dkvs.Config{EVMSourceVerifier: verifier.Verify, CurrentHeight: func() uint64 { return 1 }})
	wrong := source
	wrong.Source = `pragma solidity ^0.8.30; contract Example {}`
	_, err = store.PutLocalCAS(sign(wrong), dkvs.WritePrecondition{ExpectAbsent: true})
	require.Error(t, err)
	_, err = store.Get(record.Key)
	require.ErrorIs(t, err, dkvs.ErrRecordNotFound, "invalid source must not occupy the permanent slot")
	updated, err := store.PutLocalCAS(record, dkvs.WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	require.True(t, updated)
	updated, err = store.PutLocalCAS(record, dkvs.WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	require.False(t, updated)
}

func TestSourceCompilerRejectsImportsAndUnapprovedBinaries(t *testing.T) {
	_, err := compile("unused", `import "file:///etc/passwd"; contract Example {}`, "Example")
	require.ErrorContains(t, err, "imports are disabled")
	path := t.TempDir() + "/compiler"
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho bad\n"), 0700))
	_, err = compile(path, nativeSource, "Example")
	require.ErrorContains(t, err, "not an approved")
	w := &boundedOutput{limit: 4}
	_, err = w.Write([]byte("oversize"))
	require.Error(t, err)
	require.Zero(t, w.Len())
}
