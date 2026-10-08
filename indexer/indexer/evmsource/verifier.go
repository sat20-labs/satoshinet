package evmsource

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/wire"
)

type Verifier struct {
	CompilerPath string
	Params       *chaincfg.Params
	GasConfig    framework.GasConfig
	// Loading via the node's existing local RPC also proves canonical block
	// membership. The indexer's summaries are never used as authorization.
	LoadBlock func(string) (*wire.MsgBlock, int64, error)
}

func LoadCanonicalDeployBlock(txid string) (*wire.MsgBlock, int64, error) {
	tx, err := satsnet_rpc.GetTxVerbose(txid)
	if err != nil {
		return nil, 0, err
	}
	if tx.Confirmations <= 0 || tx.BlockHash == "" {
		return nil, 0, fmt.Errorf("deployment is not confirmed")
	}
	header, err := satsnet_rpc.GetRawBlockVerbose(tx.BlockHash)
	if err != nil {
		return nil, 0, err
	}
	hash, err := satsnet_rpc.GetBlockHash(header.Height)
	if err != nil {
		return nil, 0, err
	}
	if hash.String() != tx.BlockHash {
		return nil, 0, fmt.Errorf("deployment is not canonical")
	}
	block, err := satsnet_rpc.GetRawBlock(hash)
	if err != nil {
		return nil, 0, err
	}
	if block.BlockHash() != *hash {
		return nil, 0, fmt.Errorf("deployment block hash mismatch")
	}
	return block, header.Height, nil
}

func (v Verifier) Verify(record *wire.DKVSRecord) error {
	if record == nil {
		return fmt.Errorf("missing source record")
	}
	var source contract.EVMSourceMetadata
	decoder := json.NewDecoder(bytes.NewReader(record.Value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("source JSON contains trailing data")
	}
	if source.Version != 1 || source.Language != "Solidity" || strings.TrimSpace(source.Source) == "" || strings.TrimSpace(source.ContractName) == "" ||
		source.CompilerConfig != contract.DefaultEVMCompilerConfig() {
		return fmt.Errorf("invalid source version, language or compiler configuration")
	}
	addr, err := contract.DecodeContractAddress(source.ContractAddress)
	if err != nil {
		return err
	}
	if addr.ContractType() != contract.ContractTypeEVM || addr.EncodeAddress() != source.ContractAddress ||
		v.Params == nil || !addr.IsForNet(v.Params) || record.Key != "/blob/evm/source/"+source.ContractAddress {
		return fmt.Errorf("source address, network or blob path mismatch")
	}
	txid, err := chainhash.NewHashFromStr(source.DeployTxID)
	if err != nil || txid.String() != source.DeployTxID {
		return fmt.Errorf("invalid canonical deployment txid")
	}
	args, err := hex.DecodeString(source.ConstructorArgs)
	if err != nil || hex.EncodeToString(args) != source.ConstructorArgs {
		return fmt.Errorf("constructor args must be lowercase hex without 0x")
	}
	if source.RuntimeCodeHash != "" || source.VerifyError != "" || source.SubmittedAt != 0 || source.UpdatedAt != 0 {
		return fmt.Errorf("source record cannot claim runtime verification or mutable metadata")
	}
	content, err := v.deploymentContent(source, addr, txid)
	if err != nil {
		return err
	}
	compiled, err := compile(v.CompilerPath, source.Source, source.ContractName)
	if err != nil {
		return err
	}
	return matchCompiledSource(source, content, compiled, args)
}

// deploymentContent reads only the confirmed canonical Deploy and Result.
// It does not execute the constructor or require historical state snapshots.
func (v Verifier) deploymentContent(source contract.EVMSourceMetadata, addr contract.ContractAddress, txid *chainhash.Hash) ([]byte, error) {
	load := v.LoadBlock
	if load == nil {
		load = LoadCanonicalDeployBlock
	}
	block, height, err := load(source.DeployTxID)
	if err != nil {
		return nil, err
	}
	if block == nil || height <= 0 {
		return nil, fmt.Errorf("missing confirmed deployment block")
	}
	var deploy *wire.MsgTx
	for _, tx := range block.Transactions {
		if tx.TxHash() == *txid {
			deploy = tx
			break
		}
	}
	if deploy == nil {
		return nil, fmt.Errorf("deployment tx not in canonical block")
	}
	validated, err := evm.ValidateDeployTxBasic(deploy, v.GasConfig)
	if err != nil {
		return nil, err
	}
	prefix := contract.TestnetContractPrefix
	if v.Params.Net == chaincfg.MainNetParams.Net {
		prefix = contract.MainnetContractPrefix
	}
	funding, err := evm.FindContractOutputsForContract(deploy, evm.StandardContractScriptResolver(prefix), addr)
	if err != nil || len(funding) != 1 {
		return nil, fmt.Errorf("deployment has no unique matching contract funding output")
	}
	// A successful canonical Result is a consensus-validated deployment,
	// including its derived address and gas charge. Confirm its funding is
	// sufficient independently, without consulting mutable summary status.
	gas := v.GasConfig.Normalize()
	reserve, err := gas.ContractFundingFee(framework.ExecutionKindDeploy, validated.Payload.GasLimit, true, uint64(height))
	if err != nil {
		return nil, err
	}
	ready, err := framework.OutputsHaveRequiredGas(funding, gas.GasAssetName, reserve)
	if err != nil || !ready {
		return nil, fmt.Errorf("deployment gas funding is insufficient")
	}
	success := false
	for _, tx := range block.Transactions {
		result, err := evm.ValidateResultTxBasic(tx)
		if err != nil || result.Payload.Status != contract.ResultStatusSuccess {
			continue
		}
		for _, input := range tx.TxIn {
			if input.PreviousOutPoint.Hash == *txid && input.PreviousOutPoint.Index == funding[0].Vout {
				success = true
			}
		}
	}
	if !success {
		return nil, fmt.Errorf("deployment has no successful canonical Result")
	}
	return validated.Payload.ContractContent, nil
}

func matchCompiledSource(source contract.EVMSourceMetadata, content []byte, compiled artifact, args []byte) error {
	initCode := append(compiled.InitCode, args...)
	if !bytes.Equal(initCode, content) {
		return fmt.Errorf("compiled init code and constructor args do not match DeployTx")
	}
	if source.InitCodeHash != hex.EncodeToString(crypto.Keccak256(initCode)) {
		return fmt.Errorf("init code hash mismatch")
	}
	var providedABI, compiledABI any
	if json.Unmarshal(source.ABI, &providedABI) != nil || json.Unmarshal(compiled.ABI, &compiledABI) != nil {
		return fmt.Errorf("invalid ABI")
	}
	provided, _ := json.Marshal(providedABI)
	expected, _ := json.Marshal(compiledABI)
	if !bytes.Equal(provided, expected) {
		return fmt.Errorf("ABI does not match compiler output")
	}
	return nil
}
