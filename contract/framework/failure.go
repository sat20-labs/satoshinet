package framework

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
)

var (
	ErrCallAdmission       = errors.New("contract call admission failed")
	ErrAccountingInvariant = errors.New("contract accounting invariant failed")
)

// FundingFailureRequest contains only this call's funding. Existing contract
// balances must never subsidize a rejected caller or its refund.
type FundingFailureRequest struct {
	Height    int64
	TxID      string
	Kind      ExecutionKind
	Contract  contract.ContractAddress
	CallID    string
	Recipient string
	Funding   []ContractOutput
	GasLimit  int64
	GasUsed   int64
	GasAsset  string
	GasFee    *scommon.Decimal
	Status    contract.ResultStatus
}

const InvalidRefundSatoshiFee int64 = 10

func FundingFailureOutcome(req FundingFailureRequest) (ExecutionOutcome, error) {
	if req.Recipient == "" {
		return ExecutionOutcome{}, fmt.Errorf("%w: missing refund recipient", ErrCallAdmission)
	}
	if len(req.Funding) == 0 {
		return ExecutionOutcome{}, fmt.Errorf("%w: missing call funding", ErrCallAdmission)
	}
	if req.Status == contract.ResultStatusSuccess {
		req.Status = contract.ResultStatusInvalid
	}
	fee := CloneDecimal(req.GasFee)
	if fee.Sign() < 0 {
		return ExecutionOutcome{}, fmt.Errorf("%w: negative failure fee", ErrAccountingInvariant)
	}
	seen := make(map[OutPoint]struct{}, len(req.Funding))
	for _, output := range req.Funding {
		if !output.Contract.Equal(req.Contract) {
			return ExecutionOutcome{}, fmt.Errorf("%w: mixed refund contracts", ErrAccountingInvariant)
		}
		if _, exists := seen[output.OutPoint]; exists {
			return ExecutionOutcome{}, fmt.Errorf("%w: duplicate refund funding", ErrAccountingInvariant)
		}
		seen[output.OutPoint] = struct{}{}
	}

	mode := ResultFeeModeGasAsset
	gasRefundRecipient := req.Recipient
	requiresResult := true
	readyGas, err := OutputsHaveRequiredGas(req.Funding, req.GasAsset, fee)
	if err != nil {
		return ExecutionOutcome{}, err
	}
	var intents []AssetIntent
	if readyGas {
		intents, err = NonGasFundingRefundIntents(req.Contract, req.Funding, req.GasAsset, req.Recipient)
	} else {
		plain, err := fundingPlainSats(req.Funding)
		if err != nil {
			return ExecutionOutcome{}, err
		}
		if plain >= InvalidRefundSatoshiFee {
			mode = ResultFeeModeSatoshiFee
			fee = ZeroDecimal()
			gasRefundRecipient = ""
			intents, err = NonGasFundingRefundIntents(req.Contract, req.Funding, "", req.Recipient)
			if err == nil {
				intents, err = deductSatoshiRefundFee(intents, InvalidRefundSatoshiFee)
			}
		} else {
			// The call is invalid but cannot pay for its own refund Result. The
			// funding remains physically at the contract address, is never
			// credited to managed state, and is therefore unmanaged surplus.
			requiresResult = false
			fee = ZeroDecimal()
			gasRefundRecipient = ""
			intents = nil
		}
	}
	if err != nil {
		return ExecutionOutcome{}, err
	}
	for i := range intents {
		intents[i].CallID = req.CallID
	}
	kind := req.Kind
	if kind == 0 {
		kind = ExecutionKindInvoke
	}
	txType := contract.TxTypeInvoke
	if kind == ExecutionKindDeploy {
		txType = contract.TxTypeDeploy
	}
	return ExecutionOutcome{
		Height: req.Height, TxID: req.TxID, Type: txType, Kind: kind,
		CallID: req.CallID, Contract: req.Contract, Status: req.Status,
		GasLimit: req.GasLimit, GasUsed: req.GasUsed, GasFee: fee,
		FundingInputs: ContractOutputOutPoints(req.Funding), GasRefundRecipient: gasRefundRecipient,
		AssetIntents: intents, RequiresResult: requiresResult, ResultFeeMode: mode,
	}, nil
}

func fundingPlainSats(outputs []ContractOutput) (int64, error) {
	var total int64
	for _, output := range outputs {
		plain := output.PlainValue()
		next, overflow := AddInt64(total, plain)
		if overflow {
			return 0, fmt.Errorf("%w: refund plain sats overflow", ErrAccountingInvariant)
		}
		total = next
	}
	return total, nil
}

func deductSatoshiRefundFee(intents []AssetIntent, fee int64) ([]AssetIntent, error) {
	remaining := fee
	for i := range intents {
		if remaining == 0 || intents[i].AssetName != contract.SatoshiAssetName || intents[i].Amount == nil {
			continue
		}
		value, err := DecimalToInt64(*intents[i].Amount)
		if err != nil {
			return nil, err
		}
		deduct := remaining
		if int64(value) < deduct {
			deduct = int64(value)
		}
		value -= deduct
		remaining -= deduct
		intents[i].Amount = scommon.NewDefaultDecimal(value)
	}
	if remaining != 0 {
		return nil, fmt.Errorf("%w: insufficient plain sats for refund fee", ErrAccountingInvariant)
	}
	out := intents[:0]
	for _, intent := range intents {
		if intent.Amount != nil && intent.Amount.Sign() > 0 {
			out = append(out, intent)
		}
	}
	return out, nil
}

// FailureResultPlan is shared by non-VM backends. It remains explicit so a
// failed call cannot collect unrelated contract-address UTXOs.
func FailureResultPlan(record ExecutionRecord) (ResultPlan, error) {
	if record.Status == contract.ResultStatusSuccess {
		return ResultPlan{}, fmt.Errorf("cannot build failure plan for successful call")
	}
	return RecordIntentResultPlan(record)
}

// RecordIntentResultPlan adds immediate refunds/transfers to the business
// settlement plans. Fees and gas refunds are added once by the caller's record
// accounting, not here. Successful close funding can also be returned this way.
func RecordIntentResultPlan(record ExecutionRecord) (ResultPlan, error) {
	plan := ResultPlan{
		Contract:    record.Contract.MustEncode(),
		Height:      record.Height,
		InputScope:  ResultInputScopeExplicit,
		Inputs:      append([]OutPoint(nil), record.FundingInputs...),
		CallFunding: append([]OutPoint(nil), record.FundingInputs...),
	}
	for _, intent := range record.AssetIntents {
		if !intent.From.Equal(record.Contract) {
			return ResultPlan{}, fmt.Errorf("%w: cross-contract refund intent", ErrAccountingInvariant)
		}
		output, err := resultOutputFromIntent(intent, intent.Amount)
		if err != nil {
			return ResultPlan{}, err
		}
		output.Reason = "refund"
		plan.Outputs = append(plan.Outputs, output)
	}
	return plan, nil
}
