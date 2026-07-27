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

func resolveInvokeItemAssetName(expected, encoded string) (string, error) {
	if expected != "" && encoded != "" && expected != encoded {
		return "", fmt.Errorf("invoke parameter asset %q does not match contract asset %q", encoded, expected)
	}
	if expected != "" {
		return expected, nil
	}
	return encoded, nil
}

func validateInvokeItemParamConsistency(item *InvokeItem) error {
	if item == nil || len(item.Param) == 0 || item.Reason == InvokeReasonInvalid {
		return nil
	}

	checkOrderType := func(encoded int) error {
		if item.OrderType != encoded {
			return fmt.Errorf("invoke item %d order type %d does not match parameter order type %d", item.ID, item.OrderType, encoded)
		}
		return nil
	}
	checkAssetName := func(encoded string) error {
		_, err := resolveInvokeItemAssetName(item.AssetName, encoded)
		return err
	}

	switch item.Action {
	case InvokeAPISwap:
		param, err := decodeLimitOrderItemParam(item)
		if err != nil {
			return err
		}
		if err := checkOrderType(param.OrderType); err != nil {
			return err
		}
		return checkAssetName(param.AssetName)
	case InvokeAPIRefund:
		if _, err := decodeRefundItemParam(item); err != nil {
			return err
		}
		return checkOrderType(OrderTypeRefund)
	case InvokeAPIAddLiquidity:
		var param AddLiquidityInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return fmt.Errorf("decode invoke item %d add liquidity param: %w", item.ID, err)
		}
		if err := checkOrderType(param.OrderType); err != nil {
			return err
		}
		return checkAssetName(param.AssetName)
	case InvokeAPIRemoveLiquidity:
		param, err := decodeRemoveLiquidityItemParam(item)
		if err != nil {
			return err
		}
		if err := checkOrderType(param.OrderType); err != nil {
			return err
		}
		return checkAssetName(param.AssetName)
	case InvokeAPIExchange:
		if _, err := exchangeItemMinimumOutput(item); err != nil {
			return err
		}
		return checkOrderType(OrderTypeExchange)
	case InvokeAPIConfig:
		if _, err := decodeAutopayConfigItemParam(item); err != nil {
			return err
		}
		return checkOrderType(OrderTypeValidate)
	case InvokeAPICancel:
		return checkOrderType(OrderTypeCancel)
	case InvokeAPIClose:
		return checkOrderType(OrderTypeClose)
	default:
		return nil
	}
}
