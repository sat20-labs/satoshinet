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
			state.Running.GasBalance += gas.Int64()
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
		state.Running.GasBalance += gas.Int64()
	}
	return true, nil
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
		amtB, err := output.AssetAmount(contract.AssetBName)
		if err != nil {
			return nil, nil, "", err
		}
		amtB = parseDecimalOrZero(amtB.String())
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
		ServiceFee:     int64(req.ResultGasFee),
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
		value += output.Value
	}
	return value
}
