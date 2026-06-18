package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	SubtypePrediction = "prediction"

	CurrentAgentVersion uint32 = 1

	InvokeAPIReady   = "ready"
	InvokeAPIBet     = "bet"
	InvokeAPIConfirm = "confirm"
	InvokeAPIReject  = "reject"

	TimeBaseUnix   = "unix"
	TimeBaseHeight = "height"

	ResultTypeOutcome      = "outcome"
	ResultTypeCancelled    = "cancelled"
	ResultTypeInvalid      = "invalid"
	ResultTypeUnverifiable = "unverifiable"
)

const (
	PredictionDeployerFeeBPS = 600
	PredictionAgentFeeBPS    = 300
	PredictionBootstrapBPS   = 100
	PredictionWinnerPoolBPS  = 9000
	PredictionTotalBPS       = 10000
)

const (
	StatusPendingReady = "PendingReady"
	StatusReady        = "Ready"
	StatusRejected     = "Rejected"
	StatusInvalid      = "Invalid"
	StatusCompleted    = "Completed"
	StatusFailed       = "Failed"
	StatusDisputed     = "Disputed"
	StatusExpired      = "Expired"
)

const (
	PredictionStatusBetting       = "Betting"
	PredictionStatusClosedForBet  = "ClosedForBet"
	PredictionStatusPendingResult = "PendingResult"
	PredictionStatusConfirmed     = "Confirmed"
	PredictionStatusSettled       = "Settled"
	PredictionStatusRefundable    = "Refundable"
	PredictionStatusRejected      = "Rejected"
)

const (
	MainnetContractPrefix = contractcommon.MainnetContractPrefix
	TestnetContractPrefix = contractcommon.TestnetContractPrefix
	AddressVersionV1      = contractcommon.AddressVersionV1
	ContractTypeAgent     = contractcommon.ContractTypeAgent
	PayloadVersionV1      = contractcommon.PayloadVersionV1
	SatoshiAssetName      = contractcommon.SatoshiAssetName
)

type ContractAddress = contractcommon.ContractAddress

type DeployPayload = contractcommon.AgentDeployPayload
type InvokePayload = contractcommon.AgentInvokePayload

type TxOrderInfo = contractframework.TxOrderInfo

type TxType = contractcommon.TxType
type Tx = contractcommon.Tx
type ParsedTx = contractframework.ParsedTx

const (
	TxTypeDeploy            = contractcommon.TxTypeDeploy
	TxTypeInvoke            = contractcommon.TxTypeInvoke
	TxTypeResult            = contractcommon.TxTypeResult
	TxTypeCoinbaseStateRoot = contractcommon.TxTypeCoinbaseStateRoot
)

type ResultPayload = contractcommon.ResultPayload
type StateRootPayload = contractcommon.StateRootPayload
type ResultStatus = contractcommon.ResultStatus

type ExecutionKind = contractframework.ExecutionKind
type ExecutionRecord = contractframework.ExecutionRecord
type GasConfig = contractframework.GasConfig
type AssetIntent = contractframework.AssetIntent
type ResultOutput = contractframework.ResultOutput
type ResultPlan = contractframework.ResultPlan
type UTXO = contractframework.UTXO
type ContractUTXOProvider = contractframework.ContractUTXOProvider
type ResultRecipientScriptResolver = contractframework.ResultRecipientScriptResolver
type ResultOutputResolver = contractframework.ResultOutputResolver
type ContractExistsFunc func(ContractAddress) bool
type DeployValidation = contractframework.DeployValidation
type InvokeValidation = contractframework.InvokeValidation

const (
	ResultStatusSuccess  = contractcommon.ResultStatusSuccess
	ResultStatusRevert   = contractcommon.ResultStatusRevert
	ResultStatusOutOfGas = contractcommon.ResultStatusOutOfGas
	ResultStatusInvalid  = contractcommon.ResultStatusInvalid
)

const (
	ExecutionKindDeploy  = contractframework.ExecutionKindDeploy
	ExecutionKindInvoke  = contractframework.ExecutionKindInvoke
	ExecutionKindTrigger = contractframework.ExecutionKindTrigger
)

const AddressHashLen = 32

var (
	ContractPkScript      = contractcommon.ContractPkScript
	ParseContractPkScript = contractcommon.ParseContractPkScript
	IsContractPkScript    = contractcommon.IsContractPkScript
	DecodeContractAddress = contractcommon.DecodeContractAddress
	NewContractTxOut      = contractframework.NewContractTxOut
	MsgTxHex              = contractframework.MsgTxHex
	ParseOutPoint         = contractframework.ParseOutPoint
	WireOutPointToAgent   = contractframework.WireOutPointToFramework
	DefaultGasConfig      = contractframework.DefaultGasConfig
	ErrInvalidAsset       = contractframework.ErrInvalidAsset
)

type ContractScriptResolver = contractframework.ContractScriptResolver

func StandardContractScriptResolver(prefix string) ContractScriptResolver {
	return contractframework.ContractScriptResolverForType(prefix, ContractTypeAgent)
}

func ContractPrefixForNet(net wire.BitcoinNet) string {
	return contractframework.ContractPrefixForNet(net, MainnetContractPrefix, TestnetContractPrefix)
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string) (TxOrderInfo, error) {
	return contractframework.ClassifyTxForBlockOrder(tx, contractPrefix, contractframework.TxOrderSpec{
		ParseSpec:        agentParseSpec,
		Resolver:         StandardContractScriptResolver,
		ContractType:     ContractTypeAgent,
		DefaultInvokeGas: DefaultGasConfig().InvokeBaseGas,
		SetModuleFlag: func(info *TxOrderInfo) {
			info.IsAgent = true
		},
	})
}

func EncodeDeployPayload(p DeployPayload) ([]byte, error) {
	return contractcommon.EncodeAgentDeployPayload(p)
}

func DecodeDeployPayload(data []byte) (DeployPayload, error) {
	return contractcommon.DecodeAgentDeployPayload(data)
}

func EncodeInvokePayload(p InvokePayload) ([]byte, error) {
	return contractcommon.EncodeAgentInvokePayload(p)
}

func DecodeInvokePayload(data []byte) (InvokePayload, error) {
	return contractcommon.DecodeAgentInvokePayload(data)
}

func DeployNullDataScripts(p DeployPayload) ([][]byte, error) {
	encoded, err := EncodeDeployPayload(p)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScripts(TxTypeDeploy, encoded)
}

func InvokeNullDataScripts(p InvokePayload) ([][]byte, error) {
	encoded, err := EncodeInvokePayload(p)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScripts(TxTypeInvoke, encoded)
}

func DeployNullDataScript(p DeployPayload) ([]byte, error) {
	encoded, err := EncodeDeployPayload(p)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScript(TxTypeDeploy, encoded)
}

func InvokeNullDataScript(p InvokePayload) ([]byte, error) {
	encoded, err := EncodeInvokePayload(p)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScript(TxTypeInvoke, encoded)
}

func ReadDeployNullDataScript(script []byte) (DeployPayload, error) {
	var zero DeployPayload
	txType, content, err := contractcommon.ReadNullDataScript(script)
	if err != nil {
		return zero, err
	}
	if txType != TxTypeDeploy {
		return zero, fmt.Errorf("unexpected agent tx type %d", txType)
	}
	return DecodeDeployPayload(content)
}

func ReadInvokeNullDataScript(script []byte) (InvokePayload, error) {
	var zero InvokePayload
	txType, content, err := contractcommon.ReadNullDataScript(script)
	if err != nil {
		return zero, err
	}
	if txType != TxTypeInvoke {
		return zero, fmt.Errorf("unexpected agent tx type %d", txType)
	}
	return DecodeInvokePayload(content)
}

func DeriveContractAddress(prefix string, subtype string, content []byte, deployer string, random []byte) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := DeriveContractHash(subtype, content, deployer, random)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := contractcommon.NewContractAddressFromHash(
		prefix,
		contractcommon.AddressVersionV1,
		contractcommon.ContractTypeAgent,
		hash[:],
	)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveContractHash(subtype string, content []byte, deployer string, random []byte) ([AddressHashLen]byte, error) {
	if subtype == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract subtype is empty")
	}
	if len(content) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract deployer is empty")
	}
	if len(random) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract random value is empty")
	}

	var buf bytes.Buffer
	writeHashBytes(&buf, []byte(subtype))
	writeHashBytes(&buf, content)
	writeHashBytes(&buf, []byte(deployer))
	writeHashBytes(&buf, random)
	return sha256.Sum256(buf.Bytes()), nil
}

func writeHashBytes(buf *bytes.Buffer, data []byte) {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:n])
	buf.Write(data)
}

func DeriveDeployCallID(deployTxID string, contract ContractAddress) string {
	return contractframework.DeriveDeployCallID("agent-deploy", deployTxID, contract)
}

func DeriveInvokeCallID(invokeTxID string, vout uint32, contract ContractAddress) string {
	return contractframework.DeriveInvokeCallID("agent-invoke", invokeTxID, vout, contract)
}
