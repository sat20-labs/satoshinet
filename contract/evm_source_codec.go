package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"

	"github.com/fxamacker/cbor/v2"
)

// EVM source values use deterministic CBOR arrays. HTTP metadata remains JSON.
// Field order is part of the value format; Version is the source schema version.
type evmSourceValue struct {
	_               struct{} `cbor:",toarray"`
	Version         uint32
	Language        string
	ContractAddress string
	DeployTxID      string
	ContractName    string
	Source          string
	ABI             cbor.RawMessage
	CompilerConfig  evmCompilerValue
	ConstructorArgs string
	InitCodeHash    string
	RuntimeCodeHash string
	Verified        bool
	VerifyStatus    string
	VerifyError     string
	SubmittedAt     int64
	UpdatedAt       int64
}

type evmCompilerValue struct {
	_                struct{} `cbor:",toarray"`
	SolcVersion      string
	EVMVersion       string
	OptimizerEnabled bool
	OptimizerRuns    int
	BytecodeHash     string
	SingleFileOnly   bool
	AllowImports     bool
}

var evmSourceEncoder = func() cbor.EncMode {
	mode, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return mode
}()

var evmSourceDecoder = func() cbor.DecMode {
	mode, err := (cbor.DecOptions{
		DupMapKey:      cbor.DupMapKeyEnforcedAPF,
		DefaultMapType: reflect.TypeOf(map[string]any{}),
		BigIntDec:      cbor.BigIntDecodePointer,
	}).DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}()

// EncodeEVMSourceMetadata must run before the DKVS record is signed. ABI JSON
// is converted into CBOR values, not embedded as JSON text or a JSON byte blob.
func EncodeEVMSourceMetadata(source EVMSourceMetadata) ([]byte, error) {
	abi := cbor.RawMessage{0xf7} // Undefined distinguishes omitted ABI from JSON null.
	if len(source.ABI) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(source.ABI))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return nil, fmt.Errorf("ABI contains trailing data")
		}
		value, err := evmABINumbers(value)
		if err != nil {
			return nil, err
		}
		abi, err = evmSourceEncoder.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	config := source.CompilerConfig
	return evmSourceEncoder.Marshal(evmSourceValue{
		Version: source.Version, Language: source.Language, ContractAddress: source.ContractAddress,
		DeployTxID: source.DeployTxID, ContractName: source.ContractName, Source: source.Source, ABI: abi,
		CompilerConfig: evmCompilerValue{SolcVersion: config.SolcVersion, EVMVersion: config.EVMVersion,
			OptimizerEnabled: config.Optimizer.Enabled, OptimizerRuns: config.Optimizer.Runs,
			BytecodeHash: config.Metadata.BytecodeHash, SingleFileOnly: config.SingleFileOnly, AllowImports: config.AllowImports},
		ConstructorArgs: source.ConstructorArgs, InitCodeHash: source.InitCodeHash, RuntimeCodeHash: source.RuntimeCodeHash,
		Verified: source.Verified, VerifyStatus: source.VerifyStatus, VerifyError: source.VerifyError,
		SubmittedAt: source.SubmittedAt, UpdatedAt: source.UpdatedAt,
	})
}

// evmABINumbers preserves integer precision and normalizes equivalent JSON
// integer spellings. Noninteger numbers retain encoding/json's float64 semantics.
func evmABINumbers(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		if rational, ok := new(big.Rat).SetString(string(v)); ok && rational.IsInt() {
			return rational.Num(), nil
		}
		f, err := v.Float64()
		if err != nil {
			return nil, err
		}
		if math.Trunc(f) == f {
			integer, _ := new(big.Float).SetFloat64(f).Int(nil)
			return integer, nil
		}
		return f, nil
	case []any:
		for i := range v {
			next, err := evmABINumbers(v[i])
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
	case map[string]any:
		for key := range v {
			next, err := evmABINumbers(v[key])
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
	}
	return value, nil
}

// DecodeEVMSourceMetadata rejects old JSON and noncanonical values. It never
// rewrites record.Value: verification and storage keep the signer's exact bytes.
func DecodeEVMSourceMetadata(value []byte) (EVMSourceMetadata, error) {
	var stored evmSourceValue
	if err := evmSourceDecoder.Unmarshal(value, &stored); err != nil {
		return EVMSourceMetadata{}, err
	}
	source := EVMSourceMetadata{
		Version: stored.Version, Language: stored.Language, ContractAddress: stored.ContractAddress,
		DeployTxID: stored.DeployTxID, ContractName: stored.ContractName, Source: stored.Source,
		ConstructorArgs: stored.ConstructorArgs, InitCodeHash: stored.InitCodeHash, RuntimeCodeHash: stored.RuntimeCodeHash,
		Verified: stored.Verified, VerifyStatus: stored.VerifyStatus, VerifyError: stored.VerifyError,
		SubmittedAt: stored.SubmittedAt, UpdatedAt: stored.UpdatedAt,
	}
	config := stored.CompilerConfig
	source.CompilerConfig.SolcVersion = config.SolcVersion
	source.CompilerConfig.EVMVersion = config.EVMVersion
	source.CompilerConfig.Optimizer.Enabled = config.OptimizerEnabled
	source.CompilerConfig.Optimizer.Runs = config.OptimizerRuns
	source.CompilerConfig.Metadata.BytecodeHash = config.BytecodeHash
	source.CompilerConfig.SingleFileOnly = config.SingleFileOnly
	source.CompilerConfig.AllowImports = config.AllowImports
	if !bytes.Equal(stored.ABI, []byte{0xf7}) {
		var abi any
		if err := evmSourceDecoder.Unmarshal(stored.ABI, &abi); err != nil {
			return EVMSourceMetadata{}, err
		}
		encoded, err := json.Marshal(abi)
		if err != nil {
			return EVMSourceMetadata{}, err
		}
		source.ABI = encoded
	}
	canonical, err := EncodeEVMSourceMetadata(source)
	if err != nil {
		return EVMSourceMetadata{}, err
	}
	if !bytes.Equal(value, canonical) {
		return EVMSourceMetadata{}, fmt.Errorf("noncanonical EVM source value")
	}
	return source, nil
}
