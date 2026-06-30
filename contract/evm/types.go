package evm

import (
	"errors"

	gethcommon "github.com/ethereum/go-ethereum/common"
	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MainnetContractPrefix = evmcommon.MainnetContractPrefix
	TestnetContractPrefix = evmcommon.TestnetContractPrefix
	AddressVersionV1      = evmcommon.AddressVersionV1
	ContractTypeTemplate  = evmcommon.ContractTypeTemplate
	ContractTypeEVM       = evmcommon.ContractTypeEVM
	ContractTypeAgent     = evmcommon.ContractTypeAgent
	PayloadVersionV1      = evmcommon.PayloadVersionV1
)

var SatoshiAssetName = evmcommon.SatoshiAssetName

type TxType = evmcommon.TxType
type Tx = evmcommon.Tx
type ParsedTx = contractframework.ParsedTx

const (
	TxTypeDeploy            = evmcommon.TxTypeDeploy
	TxTypeInvoke            = evmcommon.TxTypeInvoke
	TxTypeResult            = evmcommon.TxTypeResult
	TxTypeCoinbaseStateRoot = evmcommon.TxTypeCoinbaseStateRoot
)

type ResultStatus = evmcommon.ResultStatus

const (
	ResultStatusSuccess  = evmcommon.ResultStatusSuccess
	ResultStatusRevert   = evmcommon.ResultStatusRevert
	ResultStatusOutOfGas = evmcommon.ResultStatusOutOfGas
	ResultStatusInvalid  = evmcommon.ResultStatusInvalid
)

type ExecutionKind = contractframework.ExecutionKind
type ExecutionRecord = contractframework.ExecutionRecord

const (
	ExecutionKindDeploy  = contractframework.ExecutionKindDeploy
	ExecutionKindInvoke  = contractframework.ExecutionKindInvoke
	ExecutionKindTrigger = contractframework.ExecutionKindTrigger
)

type ResultFeeMode = contractframework.ResultFeeMode

const (
	ResultFeeModeGasAsset   = contractframework.ResultFeeModeGasAsset
	ResultFeeModePlainTxFee = contractframework.ResultFeeModePlainTxFee
)

type ExecutionStatus byte

const (
	StatusSuccess ExecutionStatus = iota
	StatusRevert
	StatusOutOfGas
	StatusInvalid
)

type EVMAddress = evmcommon.EVMAddress

type ContractAddress = evmcommon.ContractAddress

type GasConfig = contractframework.GasConfig

var (
	ParseEVMAddressHex    = evmcommon.ParseEVMAddressHex
	NewContractAddress    = evmcommon.NewContractAddress
	DecodeContractAddress = evmcommon.DecodeContractAddress
	ContractAddressHash   = evmcommon.ContractAddressHash

	ContractPkScript      = evmcommon.ContractPkScript
	ParseContractPkScript = evmcommon.ParseContractPkScript
	IsContractPkScript    = evmcommon.IsContractPkScript

	ValidateTriggerGasLimit = contractframework.ValidateTriggerGasLimit
)

type DeployPayload = evmcommon.DeployPayload
type InvokePayload = evmcommon.InvokePayload
type ResultPayload = evmcommon.ResultPayload
type StateRootPayload = evmcommon.StateRootPayload

type OutPoint = contractframework.OutPoint

type UTXO = contractframework.UTXO

type AssetIntent = contractframework.AssetIntent
type ResultOutput = contractframework.ResultOutput
type ResultPlan = contractframework.ResultPlan
type ResultRecipientScriptResolver = contractframework.ResultRecipientScriptResolver
type ContractUTXOProvider = contractframework.ContractUTXOProvider
type ResultOutputResolver = contractframework.ResultOutputResolver

type TxOrderInfo = contractframework.TxOrderInfo

func NewAssetSet(assetName string, amount *scommon.Decimal) (wire.TxAssets, error) {
	return contractframework.NewAssetSet(assetName, amount)
}

func SortUTXOsForCanonicalSelection(utxos []UTXO) {
	contractframework.SortUTXOsForCanonicalSelection(utxos)
}

func StandardContractScriptResolver(prefix string) ContractScriptResolver {
	return contractframework.ContractScriptResolverForType(prefix, ContractTypeEVM)
}

func ContractPrefixForNet(net wire.BitcoinNet) string {
	return contractframework.ContractPrefixForNet(net, MainnetContractPrefix, TestnetContractPrefix)
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string) (TxOrderInfo, error) {
	return contractframework.ClassifyTxForBlockOrder(tx, contractPrefix, contractframework.TxOrderSpec{
		ParseSpec:        evmParseSpec,
		Resolver:         StandardContractScriptResolver,
		ContractType:     ContractTypeEVM,
		DefaultInvokeGas: DefaultGasConfig().InvokeBaseGas,
		SetModuleFlag: func(info *TxOrderInfo) {
			info.IsEVM = true
		},
	})
}

func FindCoinbaseStateRoot(tx *wire.MsgTx) (StateRootPayload, bool, error) {
	return contractframework.FindCoinbaseStateRoot(tx, "EVM")
}

func VerifyCoinbaseStateRoot(tx *wire.MsgTx, expected [32]byte) error {
	return contractframework.VerifyCoinbaseStateRoot(tx, expected, "EVM")
}

func UpsertCoinbaseStateRoot(tx *wire.MsgTx, root [32]byte) error {
	return contractframework.UpsertCoinbaseStateRoot(tx, root, "EVM")
}

func DeriveCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) (ContractAddress, error) {
	return evmcommon.DeriveEVMCreateContractAddress(prefix, caller, nonce)
}

func MustDeriveCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) ContractAddress {
	addr, err := DeriveCreateContractAddress(prefix, caller, nonce)
	if err != nil {
		panic(err)
	}
	return addr
}

func NewContractTxOut(value int64, assets wire.TxAssets, contract ContractAddress) (*wire.TxOut, error) {
	return contractframework.NewContractTxOut(value, assets, contract)
}

func ContractFromTxOut(txOut *wire.TxOut, prefix string) (ContractAddress, bool, error) {
	if txOut == nil {
		return ContractAddress{}, false, nil
	}
	return ParseContractPkScript(txOut.PkScript, prefix)
}

func DefaultGasConfig() GasConfig {
	return contractframework.DefaultGasConfig()
}

func SplitGasFunding(totalGasAsset, callFee, resultPackingFee uint64) (feeToMiner, contractRemainder uint64, err error) {
	required, overflow := contractframework.AddUint64(callFee, resultPackingFee)
	if overflow {
		return 0, 0, errors.New("gas funding requirement overflows uint64")
	}
	if totalGasAsset < required {
		return 0, 0, ErrInsufficientFunds
	}
	return required, totalGasAsset - required, nil
}

func GethAddress(a EVMAddress) gethcommon.Address {
	return gethcommon.BytesToAddress(a[:])
}

func EVMAddressFromGeth(a gethcommon.Address) EVMAddress {
	var out EVMAddress
	copy(out[:], a.Bytes())
	return out
}

func ContractGethAddress(a ContractAddress) gethcommon.Address {
	return GethAddress(ContractAddressHash(a))
}

var (
	ErrInsufficientFunds = contractframework.ErrInsufficientFunds
	ErrInvalidAsset      = contractframework.ErrInvalidAsset
	WireOutPointToEVM    = contractframework.WireOutPointToFramework
	MsgTxHex             = contractframework.MsgTxHex
	ParseOutPoint        = contractframework.ParseOutPoint
)

func zeroDecimal() *scommon.Decimal {
	return contractframework.ZeroDecimal()
}

func cloneDecimal(d *scommon.Decimal) *scommon.Decimal {
	return contractframework.CloneDecimal(d)
}

func decimalFromUint64(v uint64) (*scommon.Decimal, error) {
	return contractframework.DecimalFromUint64(v)
}

func ParseDecimalAmountString(s string) (*scommon.Decimal, error) {
	return contractframework.ParseDecimalAmountString(s)
}
