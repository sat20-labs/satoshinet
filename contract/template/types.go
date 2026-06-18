package template

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
	TemplateLimitOrder = contractcommon.TemplateLimitOrder
	TemplateSwapLegacy = contractcommon.TemplateSwapLegacy
	TemplateAMM        = contractcommon.TemplateAMM
	TemplateExchange   = contractcommon.TemplateExchange
)

const (
	CurrentTemplateVersion uint32 = 1
	MaxPriceDivisibility          = 10
	SwapInvokeFee          int64  = 10
	SwapServiceFeeRatio    int64  = 8
	AMMProfitShareLP              = 60
)

const (
	InvokeAPISwap            = contractcommon.TemplateInvokeAPISwap
	InvokeAPIRefund          = contractcommon.TemplateInvokeAPIRefund
	InvokeAPIAddLiquidity    = contractcommon.TemplateInvokeAPIAddLiquidity
	InvokeAPIRemoveLiquidity = contractcommon.TemplateInvokeAPIRemoveLiquidity
	InvokeAPIProfit          = contractcommon.TemplateInvokeAPIProfit
	InvokeAPIExchange        = contractcommon.TemplateInvokeAPIExchange
	InvokeAPIClose           = contractcommon.TemplateInvokeAPIClose
)

const (
	OrderTypeNoSpec          = 0
	OrderTypeSell            = 1
	OrderTypeBuy             = 2
	OrderTypeRefund          = 3
	OrderTypeFund            = 4
	OrderTypeProfit          = 5
	OrderTypeDeposit         = 6
	OrderTypeWithdraw        = 7
	OrderTypeMint            = 8
	OrderTypeAddLiquidity    = 9
	OrderTypeRemoveLiquidity = 10
	OrderTypeStake           = 11
	OrderTypeUnstake         = 12
	OrderTypeRecycle         = 13
	OrderTypeReward          = 14
	OrderTypeRegister        = 15
	OrderTypeDonate          = 16
	OrderTypeAirdrop         = 17
	OrderTypeValidate        = 18
	OrderTypeBind            = 19
	OrderTypeClose           = 20
	OrderTypeUnused          = 21
	OrderTypeExchange        = 22
)

const (
	ItemStatusReadyToSend    = -1
	ItemStatusInit           = 0
	ItemStatusDealt          = 1
	ItemStatusRefunded       = 2
	ItemStatusClosedDirectly = 3
	ItemStatusCancelled      = 4
)

const (
	InvokeReasonNormal           = ""
	InvokeReasonRefund           = "refund"
	InvokeReasonCancel           = "cancel"
	InvokeReasonInvalid          = "invalid"
	InvokeReasonInnerError       = "inner error"
	InvokeReasonNoEnoughAsset    = "no enough asset"
	InvokeReasonSlippageProtect  = "slippage protection"
	InvokeReasonNoProfit         = "no profit"
	InvokeReasonInputUTXOTooTiny = "input utxo value too small"
)

const (
	SettlementReasonDeal   = "deal"
	SettlementReasonRefund = "refund"
)

const (
	MainnetContractPrefix = contractcommon.MainnetContractPrefix
	TestnetContractPrefix = contractcommon.TestnetContractPrefix
	AddressVersionV1      = contractcommon.AddressVersionV1
	ContractTypeTemplate  = contractcommon.ContractTypeTemplate
	ContractTypeEVM       = contractcommon.ContractTypeEVM
	ContractTypeAgent     = contractcommon.ContractTypeAgent
	PayloadVersionV1      = contractcommon.PayloadVersionV1
	SatoshiAssetName      = contractcommon.SatoshiAssetName
)

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

const (
	ResultStatusSuccess  = contractcommon.ResultStatusSuccess
	ResultStatusRevert   = contractcommon.ResultStatusRevert
	ResultStatusOutOfGas = contractcommon.ResultStatusOutOfGas
	ResultStatusInvalid  = contractcommon.ResultStatusInvalid
)

type ExecutionKind = contractframework.ExecutionKind
type ExecutionRecord = contractframework.ExecutionRecord
type GasConfig = contractframework.GasConfig
type ContractExistsFunc func(ContractAddress) bool
type DeployValidation = contractframework.DeployValidation
type InvokeValidation = contractframework.InvokeValidation

const (
	ExecutionKindDeploy  = contractframework.ExecutionKindDeploy
	ExecutionKindInvoke  = contractframework.ExecutionKindInvoke
	ExecutionKindTrigger = contractframework.ExecutionKindTrigger
)

type ContractAddress = contractcommon.ContractAddress

type OutPoint = contractframework.OutPoint

type AssetIntent = contractframework.AssetIntent

type ResultOutput = contractframework.ResultOutput
type ResultPlan = contractframework.ResultPlan
type UTXO = contractframework.UTXO
type ContractUTXOProvider = contractframework.ContractUTXOProvider
type ResultRecipientScriptResolver = contractframework.ResultRecipientScriptResolver
type ResultOutputResolver = contractframework.ResultOutputResolver

type TxOrderInfo = contractframework.TxOrderInfo

type DeployPayload = contractcommon.TemplateDeployPayload
type InvokePayload = contractcommon.TemplateInvokePayload

const AddressHashLen = 32

var (
	ContractPkScript       = contractcommon.ContractPkScript
	ParseContractPkScript  = contractcommon.ParseContractPkScript
	IsContractPkScript     = contractcommon.IsContractPkScript
	DecodeContractAddress  = contractcommon.DecodeContractAddress
	NewContractTxOut       = contractframework.NewContractTxOut
	MsgTxHex               = contractframework.MsgTxHex
	ParseOutPoint          = contractframework.ParseOutPoint
	WireOutPointToTemplate = contractframework.WireOutPointToFramework
	DefaultGasConfig       = contractframework.DefaultGasConfig
	ErrInvalidAsset        = contractframework.ErrInvalidAsset
)

type ContractScriptResolver = contractframework.ContractScriptResolver

func StandardContractScriptResolver(prefix string) ContractScriptResolver {
	return contractframework.ContractScriptResolverForType(prefix, ContractTypeTemplate)
}

func ContractPrefixForNet(net wire.BitcoinNet) string {
	return contractframework.ContractPrefixForNet(net, MainnetContractPrefix, TestnetContractPrefix)
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string) (TxOrderInfo, error) {
	return contractframework.ClassifyTxForBlockOrder(tx, contractPrefix, contractframework.TxOrderSpec{
		ParseSpec:        templateParseSpec,
		Resolver:         StandardContractScriptResolver,
		ContractType:     ContractTypeTemplate,
		DefaultInvokeGas: DefaultGasConfig().InvokeBaseGas,
		SetModuleFlag: func(info *TxOrderInfo) {
			info.IsTemplate = true
		},
	})
}

func FindCoinbaseStateRoot(tx *wire.MsgTx) (StateRootPayload, bool, error) {
	return contractframework.FindCoinbaseStateRoot(tx, "template")
}

func VerifyCoinbaseStateRoot(tx *wire.MsgTx, expected [32]byte) error {
	return contractframework.VerifyCoinbaseStateRoot(tx, expected, "template")
}

func UpsertCoinbaseStateRoot(tx *wire.MsgTx, root [32]byte) error {
	return contractframework.UpsertCoinbaseStateRoot(tx, root, "template")
}

func EncodeDeployPayload(p DeployPayload) ([]byte, error) {
	return contractcommon.EncodeTemplateDeployPayload(p)
}

func DecodeDeployPayload(data []byte) (DeployPayload, error) {
	return contractcommon.DecodeTemplateDeployPayload(data)
}

func EncodeInvokePayload(p InvokePayload) ([]byte, error) {
	return contractcommon.EncodeTemplateInvokePayload(p)
}

func DecodeInvokePayload(data []byte) (InvokePayload, error) {
	return contractcommon.DecodeTemplateInvokePayload(data)
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
		return zero, fmt.Errorf("unexpected template tx type %d", txType)
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
		return zero, fmt.Errorf("unexpected template tx type %d", txType)
	}
	return DecodeInvokePayload(content)
}

func DeriveContractAddress(prefix string, encodedContract []byte, deployer string, random []byte) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := DeriveContractHash(encodedContract, deployer, random)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := contractcommon.NewContractAddressFromHash(
		prefix,
		contractcommon.AddressVersionV1,
		contractcommon.ContractTypeTemplate,
		hash[:],
	)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveContractHash(encodedContract []byte, deployer string, random []byte) ([AddressHashLen]byte, error) {
	if len(encodedContract) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("template contract deployer is empty")
	}
	if len(random) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract random value is empty")
	}

	var buf bytes.Buffer
	writeHashBytes(&buf, encodedContract)
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

type Contract interface {
	TemplateName() string
	Version() uint32
	Encode() ([]byte, error)
	Decode([]byte) error
	CheckContent() error
}

type Runtime interface {
	Contract
	Address() ContractAddress
	URL() string
}

type FundingStateApplier interface {
	ApplyFundingState(state *TemplateRuntimeState, outputs []ContractOutput, gasAssetName string) (bool, error)
}

type GasFundingStateApplier interface {
	ApplyGasFundingState(state *TemplateRuntimeState, outputs []ContractOutput, gasAssetName string) (bool, error)
}

type RunningDataApplier interface {
	ApplyRunningData(running *RunningData, item *InvokeItem) bool
}

type InvokableContract interface {
	Contract
	CheckInvoke(action string, param []byte) error
}
