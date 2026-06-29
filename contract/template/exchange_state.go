package template

import scommon "github.com/sat20-labs/indexer/common"

func (c *ExchangeContract) ApplyFundingState(state *TemplateRuntimeState, outputs []ContractOutput, gasAssetName string) (bool, error) {
	if len(outputs) == 0 {
		return true, nil
	}
	for _, output := range outputs {
		amt, err := output.AssetAmount(c.AssetAName)
		if err != nil {
			return true, err
		}
		amt = parseDecimalOrZero(amt.String())
		if amt.Sign() > 0 {
			if state.Running.AssetAInPool == nil {
				state.Running.AssetAInPool = parseDecimalOrZero("0")
			}
			state.Running.AssetAInPool = scommon.DecimalAdd(state.Running.AssetAInPool, amt)
		}
		if gasAssetName != "" && gasAssetName != c.AssetAName && gasAssetName != c.AssetBName {
			gas, err := output.AssetAmount(gasAssetName)
			if err != nil {
				return true, err
			}
			state.Running.GasBalance = decimalAddAllowNil(state.Running.GasBalance, gas)
		}
	}
	return true, nil
}

func (c *ExchangeContract) ApplyGasFundingState(state *TemplateRuntimeState, outputs []ContractOutput, gasAssetName string) (bool, error) {
	if len(outputs) == 0 || gasAssetName == "" {
		return true, nil
	}
	if gasAssetName == c.AssetAName || gasAssetName == c.AssetBName {
		return true, nil
	}
	for _, output := range outputs {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return true, err
		}
		state.Running.GasBalance = decimalAddAllowNil(state.Running.GasBalance, gas)
	}
	return true, nil
}

func (c *ExchangeContract) ApplyRunningData(r *RunningData, item *InvokeItem) bool {
	if item == nil {
		return true
	}
	r.applyDefaultInvokeRetention(item)
	if item.Reason == InvokeReasonInvalid {
		return true
	}
	switch item.OrderType {
	case OrderTypeFund:
		if item.InAmt != nil {
			if r.TotalInputAssetA == nil {
				r.TotalInputAssetA = parseDecimalOrZero("0")
			}
			if r.AssetAInPool == nil {
				r.AssetAInPool = parseDecimalOrZero("0")
			}
			r.TotalInputAssetA = scommon.DecimalAdd(r.TotalInputAssetA, item.InAmt)
			r.AssetAInPool = scommon.DecimalAdd(r.AssetAInPool, item.InAmt)
		}
		return true
	case OrderTypeExchange:
		if item.OutAmt != nil {
			if r.TotalInputAssetA == nil {
				r.TotalInputAssetA = parseDecimalOrZero("0")
			}
			if r.AssetAInPool == nil {
				r.AssetAInPool = parseDecimalOrZero("0")
			}
			r.TotalInputAssetA = scommon.DecimalAdd(r.TotalInputAssetA, item.OutAmt)
			r.AssetAInPool = scommon.DecimalAdd(r.AssetAInPool, item.OutAmt)
		}
		if item.InAmt != nil {
			if r.TotalInputAssetB == nil {
				r.TotalInputAssetB = parseDecimalOrZero("0")
			}
			r.TotalInputAssetB = scommon.DecimalAdd(r.TotalInputAssetB, item.InAmt)
		}
		return true
	case OrderTypeClose:
		if item.InAmt != nil {
			if r.TotalInputAssetA == nil {
				r.TotalInputAssetA = parseDecimalOrZero("0")
			}
			r.TotalInputAssetA = scommon.DecimalAdd(r.TotalInputAssetA, item.InAmt)
		}
		if item.RemainingAmt != nil {
			if r.TotalInputAssetB == nil {
				r.TotalInputAssetB = parseDecimalOrZero("0")
			}
			r.TotalInputAssetB = scommon.DecimalAdd(r.TotalInputAssetB, item.RemainingAmt)
		}
		return true
	default:
		return false
	}
}

func exchangeFundingAmounts(contract *ExchangeContract, outputs []ContractOutput) (*scommon.Decimal, *scommon.Decimal, string, error) {
	inputA := parseDecimalOrZero("0")
	inputB := parseDecimalOrZero("0")
	inUtxos := ""
	for i, output := range outputs {
		if i > 0 {
			inUtxos += ","
		}
		inUtxos += output.OutPoint.String()
		amtA, err := output.AssetAmount(contract.AssetAName)
		if err != nil {
			return nil, nil, "", err
		}
		amtA = parseDecimalOrZero(amtA.String())
		amtB := parseDecimalOrZero("0")
		if contract.AssetBName == SatoshiAssetName {
			amtB = scommon.NewDefaultDecimal(output.PlainValue())
		} else {
			var err error
			amtB, err = output.AssetAmount(contract.AssetBName)
			if err != nil {
				return nil, nil, "", err
			}
			amtB = parseDecimalOrZero(amtB.String())
		}
		inputA = scommon.DecimalAdd(inputA, amtA)
		inputB = scommon.DecimalAdd(inputB, amtB)
	}
	return inputA, inputB, inUtxos, nil
}

func newExchangeItem(id int64, action string, req ApplyInvokeRequest, inUtxos, assetBName string,
	inputB *scommon.Decimal, minOutA string) *InvokeItem {

	item := &InvokeItem{
		ID:             id,
		CallID:         req.CallID,
		Action:         action,
		OrderType:      OrderTypeExchange,
		Height:         req.Height,
		OrderTime:      req.Timestamp,
		AssetName:      assetBName,
		Address:        req.Invoker,
		InUtxos:        inUtxos,
		InAmt:          nil,
		ExpectedAmt:    nil,
		RemainingAmt:   nil,
		GasFee:         req.ResultGasFee.Clone(),
		Reason:         InvokeReasonNormal,
		Done:           ItemStatusInit,
		RemainingValue: 0,
	}
	if inputB != nil && inputB.Sign() > 0 {
		item.InAmt = inputB
		item.RemainingAmt = inputB
	}
	if minOutA != "" {
		item.ExpectedAmt = parseDecimalOrZero(minOutA)
	}
	return item
}

func fundingValue(outputs []ContractOutput) int64 {
	var value int64
	for _, output := range outputs {
		value += output.PlainValue()
	}
	return value
}
