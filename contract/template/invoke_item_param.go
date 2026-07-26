package template

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
)

func decodeLimitOrderItemParam(item *InvokeItem) (LimitOrderInvokeParam, error) {
	var param LimitOrderInvokeParam
	if item == nil {
		return param, fmt.Errorf("nil invoke item")
	}
	if len(item.Param) == 0 {
		return param, nil
	}
	if err := param.Decode(item.Param); err != nil {
		return param, fmt.Errorf("decode invoke item %d limit order param: %w", item.ID, err)
	}
	return param, nil
}

func limitOrderItemPrice(item *InvokeItem) (string, error) {
	param, err := decodeLimitOrderItemParam(item)
	if err != nil {
		return "", err
	}
	return param.UnitPrice, nil
}

func limitOrderItemExpectedAmount(item *InvokeItem) (*scommon.Decimal, error) {
	param, err := decodeLimitOrderItemParam(item)
	if err != nil {
		return nil, err
	}
	return parseDecimalOrZero(param.Amt), nil
}

func decodeRefundItemParam(item *InvokeItem) (RefundInvokeParam, error) {
	var param RefundInvokeParam
	if item == nil {
		return param, fmt.Errorf("nil invoke item")
	}
	if err := param.Decode(item.Param); err != nil {
		return param, fmt.Errorf("decode invoke item %d refund param: %w", item.ID, err)
	}
	return param, nil
}

func decodeRemoveLiquidityItemParam(item *InvokeItem) (RemoveLiquidityInvokeParam, error) {
	var param RemoveLiquidityInvokeParam
	if item == nil {
		return param, fmt.Errorf("nil invoke item")
	}
	if err := param.Decode(item.Param); err != nil {
		return param, fmt.Errorf("decode invoke item %d remove liquidity param: %w", item.ID, err)
	}
	return param, nil
}

func removeLiquidityItemAmount(item *InvokeItem) (*scommon.Decimal, error) {
	param, err := decodeRemoveLiquidityItemParam(item)
	if err != nil {
		return nil, err
	}
	return parseDecimalOrZero(param.LptAmt), nil
}

func exchangeItemMinimumOutput(item *InvokeItem) (*scommon.Decimal, error) {
	var param ExchangeInvokeParam
	if item == nil {
		return nil, fmt.Errorf("nil invoke item")
	}
	if err := param.Decode(item.Param); err != nil {
		return nil, fmt.Errorf("decode invoke item %d exchange param: %w", item.ID, err)
	}
	return parseDecimalOrZero(param.MinOutA), nil
}

func decodeAutopayConfigItemParam(item *InvokeItem) (AutopayConfigInvokeParam, error) {
	var param AutopayConfigInvokeParam
	if item == nil {
		return param, fmt.Errorf("nil invoke item")
	}
	if err := param.Decode(item.Param); err != nil {
		return param, fmt.Errorf("decode invoke item %d autopay config param: %w", item.ID, err)
	}
	return param, nil
}
