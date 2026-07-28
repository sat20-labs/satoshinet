package template

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

func decodeLimitOrderItemParamRaw(item *InvokeItem) (LimitOrderInvokeParam, error) {
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

func decodeLimitOrderItemParam(item *InvokeItem) (LimitOrderInvokeParam, error) {
	param, err := decodeLimitOrderItemParamRaw(item)
	if err != nil {
		return param, err
	}
	if len(item.Param) == 0 {
		if item.Action == contractcommon.ContractInvokeAPIDefault || item.Reason == InvokeReasonInvalid {
			return param, nil
		}
		return param, fmt.Errorf("invoke item %d limit order param is empty", item.ID)
	}
	if err := validateInvokeItemOrderType(item, param.OrderType); err != nil {
		return param, err
	}
	if _, err := resolveInvokeItemAssetName(item.AssetName, param.AssetName); err != nil {
		return param, err
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
	if err := validateInvokeItemOrderType(item, OrderTypeRefund); err != nil {
		return param, err
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
	if err := validateInvokeItemOrderType(item, param.OrderType); err != nil {
		return param, err
	}
	if _, err := resolveInvokeItemAssetName(item.AssetName, param.AssetName); err != nil {
		return param, err
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
	if err := validateInvokeItemOrderType(item, OrderTypeExchange); err != nil {
		return nil, err
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
	if err := validateInvokeItemOrderType(item, OrderTypeValidate); err != nil {
		return param, err
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

func validateInvokeItemOrderType(item *InvokeItem, encoded int) error {
	if item == nil || item.Reason == InvokeReasonInvalid {
		return nil
	}
	if item.OrderType != encoded {
		return fmt.Errorf("invoke item %d order type %d does not match parameter order type %d", item.ID, item.OrderType, encoded)
	}
	return nil
}

func validateInvokeItemParamConsistency(item *InvokeItem) error {
	if item == nil || item.Reason == InvokeReasonInvalid {
		return nil
	}

	switch item.Action {
	case contractcommon.ContractInvokeAPIDefault:
		switch item.OrderType {
		case OrderTypeBuy, OrderTypeSell, OrderTypeFund, OrderTypeExchange:
		default:
			return fmt.Errorf("invoke item %d has invalid default order type %d", item.ID, item.OrderType)
		}
		if len(item.Param) == 0 {
			return nil
		}
		param, err := decodeLimitOrderItemParam(item)
		if err != nil {
			return err
		}
		if param.OrderType != OrderTypeBuy && param.OrderType != OrderTypeSell {
			return fmt.Errorf("invoke item %d has invalid default limit order type %d", item.ID, param.OrderType)
		}
		return nil
	case InvokeAPISwap:
		_, err := decodeLimitOrderItemParam(item)
		return err
	case InvokeAPIRefund:
		_, err := decodeRefundItemParam(item)
		return err
	case InvokeAPIAddLiquidity:
		var param AddLiquidityInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return fmt.Errorf("decode invoke item %d add liquidity param: %w", item.ID, err)
		}
		if err := validateInvokeItemOrderType(item, param.OrderType); err != nil {
			return err
		}
		_, err := resolveInvokeItemAssetName(item.AssetName, param.AssetName)
		return err
	case InvokeAPIRemoveLiquidity:
		_, err := decodeRemoveLiquidityItemParam(item)
		return err
	case InvokeAPIExchange:
		_, err := exchangeItemMinimumOutput(item)
		return err
	case InvokeAPIConfig:
		_, err := decodeAutopayConfigItemParam(item)
		return err
	case InvokeAPICancel:
		if len(item.Param) != 0 {
			return fmt.Errorf("invoke item %d cancel param is not empty", item.ID)
		}
		return validateInvokeItemOrderType(item, OrderTypeCancel)
	case InvokeAPIClose:
		if len(item.Param) != 0 {
			return fmt.Errorf("invoke item %d close param is not empty", item.ID)
		}
		return validateInvokeItemOrderType(item, OrderTypeClose)
	default:
		return nil
	}
}

func normalizeInvokeItemParam(contract Contract, item *InvokeItem) error {
	if item == nil || item.Reason == InvokeReasonInvalid {
		return nil
	}

	expectedAssetName := contractAssetName(contract)
	var err error
	switch item.Action {
	case contractcommon.ContractInvokeAPIDefault:
		if len(item.Param) == 0 {
			break
		}
		param, decodeErr := decodeLimitOrderItemParam(item)
		if decodeErr != nil {
			return decodeErr
		}
		item.AssetName, err = resolveInvokeItemAssetName(expectedAssetName, param.AssetName)
	case InvokeAPISwap:
		param, decodeErr := decodeLimitOrderItemParam(item)
		if decodeErr != nil {
			return decodeErr
		}
		item.AssetName, err = resolveInvokeItemAssetName(expectedAssetName, param.AssetName)
	case InvokeAPIAddLiquidity:
		var param AddLiquidityInvokeParam
		if decodeErr := param.Decode(item.Param); decodeErr != nil {
			return fmt.Errorf("decode invoke item %d add liquidity param: %w", item.ID, decodeErr)
		}
		item.AssetName, err = resolveInvokeItemAssetName(expectedAssetName, param.AssetName)
	case InvokeAPIRemoveLiquidity:
		param, decodeErr := decodeRemoveLiquidityItemParam(item)
		if decodeErr != nil {
			return decodeErr
		}
		item.AssetName, err = resolveInvokeItemAssetName(expectedAssetName, param.AssetName)
	}
	if err != nil {
		return err
	}
	return validateInvokeItemParamConsistency(item)
}
