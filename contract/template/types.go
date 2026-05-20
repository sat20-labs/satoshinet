package template

import contractcommon "github.com/sat20-labs/satoshinet/contract/common"

const (
	TemplateLimitOrder = "limitorder.tc"
	TemplateAMM        = "amm.tc"
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

type ContractAddress = contractcommon.ContractAddress

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
