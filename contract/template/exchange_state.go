package template

import (
	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func (c *ExchangeContract) ApplyFundingState(state *TemplateRuntimeState, output ContractOutput, gasAssetName string) (bool, error) {
	amt, err := output.AssetAmount(c.AssetAName)
	if err != nil {
		return true, err
	}
	amt = parseDecimalOrZero(amt.String())
	if amt.Sign() > 0 {
		if state.ExchangeData().AssetAInPool == nil {
			state.ExchangeData().AssetAInPool = parseDecimalOrZero("0")
		}
		state.ExchangeData().AssetAInPool = scommon.DecimalAdd(state.ExchangeData().AssetAInPool, amt)
	}
	if gasAssetName != "" && gasAssetName != c.AssetAName && gasAssetName != c.AssetBName {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return true, err
		}
		state.ExchangeData().GasBalance = decimalAddAllowNil(state.ExchangeData().GasBalance, gas)
	}
	return true, nil
}

func (c *ExchangeContract) ApplyGasFundingState(state *TemplateRuntimeState, output ContractOutput, gasAssetName string) (bool, error) {
	if gasAssetName == "" {
		return true, nil
	}
	if gasAssetName == c.AssetAName || gasAssetName == c.AssetBName {
		return true, nil
	}
	gas, err := output.AssetAmount(gasAssetName)
	if err != nil {
		return true, err
	}
	state.ExchangeData().GasBalance = decimalAddAllowNil(state.ExchangeData().GasBalance, gas)
	return true, nil
}

func (c *ExchangeContract) ApplyRunningData(state *TemplateRuntimeState, item *InvokeItem) bool {
	if state == nil || item == nil {
		return true
	}
	exchange := state.ExchangeData()
	applyDefaultInvokeRetentionToExchange(exchange, item)
	if item.Reason == InvokeReasonInvalid {
		return true
	}
	switch item.OrderType {
	case OrderTypeFund:
		if item.InAmt != nil {
			if exchange.TotalInputAssetA == nil {
				exchange.TotalInputAssetA = parseDecimalOrZero("0")
			}
			if exchange.AssetAInPool == nil {
				exchange.AssetAInPool = parseDecimalOrZero("0")
			}
			exchange.TotalInputAssetA = scommon.DecimalAdd(exchange.TotalInputAssetA, item.InAmt)
			exchange.AssetAInPool = scommon.DecimalAdd(exchange.AssetAInPool, item.InAmt)
		}
		return true
	case OrderTypeExchange:
		if item.OutAmt != nil {
			if exchange.TotalInputAssetA == nil {
				exchange.TotalInputAssetA = parseDecimalOrZero("0")
			}
			if exchange.AssetAInPool == nil {
				exchange.AssetAInPool = parseDecimalOrZero("0")
			}
			exchange.TotalInputAssetA = scommon.DecimalAdd(exchange.TotalInputAssetA, item.OutAmt)
			exchange.AssetAInPool = scommon.DecimalAdd(exchange.AssetAInPool, item.OutAmt)
		}
		if item.InAmt != nil {
			if exchange.TotalInputAssetB == nil {
				exchange.TotalInputAssetB = parseDecimalOrZero("0")
			}
			exchange.TotalInputAssetB = scommon.DecimalAdd(exchange.TotalInputAssetB, item.InAmt)
		}
		return true
	case OrderTypeClose:
		if item.InAmt != nil {
			if exchange.TotalInputAssetA == nil {
				exchange.TotalInputAssetA = parseDecimalOrZero("0")
			}
			exchange.TotalInputAssetA = scommon.DecimalAdd(exchange.TotalInputAssetA, item.InAmt)
		}
		if item.RemainingAmt != nil {
			if exchange.TotalInputAssetB == nil {
				exchange.TotalInputAssetB = parseDecimalOrZero("0")
			}
			exchange.TotalInputAssetB = scommon.DecimalAdd(exchange.TotalInputAssetB, item.RemainingAmt)
		}
		return true
	default:
		return false
	}
}

func exchangeFundingAmounts(contract *ExchangeContract, output ContractOutput) (*scommon.Decimal, *scommon.Decimal, string, error) {
	inputA, err := output.AssetAmount(contract.AssetAName)
	if err != nil {
		return nil, nil, "", err
	}
	inputA = parseDecimalOrZero(inputA.String())
	inputB := parseDecimalOrZero("0")
	if contract.AssetBName == SatoshiAssetName {
		inputB = scommon.NewDefaultDecimal(output.PlainValue())
	} else {
		var err error
		inputB, err = output.AssetAmount(contract.AssetBName)
		if err != nil {
			return nil, nil, "", err
		}
		inputB = parseDecimalOrZero(inputB.String())
	}
	return inputA, inputB, output.OutPoint.String(), nil
}

func newExchangeItem(id int64, action string, req ApplyInvokeRequest, inUtxos, assetBName string,
	inputB *scommon.Decimal) *InvokeItem {

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
		RemainingAmt:   nil,
		GasFee:         req.ResultGasFee.Clone(),
		Param:          contractframework.CloneBytes(req.Param),
		Reason:         InvokeReasonNormal,
		Done:           ItemStatusInit,
		RemainingValue: 0,
	}
	if inputB != nil && inputB.Sign() > 0 {
		item.InAmt = inputB
		item.RemainingAmt = inputB
	}
	return item
}

func fundingValue(output ContractOutput) int64 {
	return output.PlainValue()
}
