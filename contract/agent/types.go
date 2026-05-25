package agent

import contractcommon "github.com/sat20-labs/satoshinet/contract/common"

const (
	SubtypePrediction = "prediction"

	CurrentAgentVersion uint32 = 1

	InvokeAPIReady   = "ready"
	InvokeAPIBet     = "bet"
	InvokeAPIConfirm = "confirm"

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
