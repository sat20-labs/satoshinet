package template

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
)

const (
	TemplateLimitOrder = "limitorder.tc"
	TemplateAMM        = "amm.tc"
)

const (
	CurrentTemplateVersion uint32 = 1
	MaxPriceDivisibility          = 10
	SwapInvokeFee          int64  = 10
	SwapServiceFeeRatio    int64  = 8
)

const (
	InvokeAPISwap            = "swap"
	InvokeAPIRefund          = "refund"
	InvokeAPIAddLiquidity    = "addliq"
	InvokeAPIRemoveLiquidity = "removeliq"
	InvokeAPIProfit          = "profit"
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

type ExecutionKind byte

const (
	ExecutionKindDeploy ExecutionKind = iota + 1
	ExecutionKindInvoke
	ExecutionKindTrigger
)

type ContractAddress = contractcommon.ContractAddress

type OutPoint struct {
	TxID string
	Vout uint32
}

func (o OutPoint) String() string {
	return fmt.Sprintf("%s:%d", o.TxID, o.Vout)
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

type InvokableContract interface {
	Contract
	CheckInvoke(action string, param []byte) error
}
