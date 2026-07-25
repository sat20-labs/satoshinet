package template

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
)

// defaultInvokeParam is the compact parameter encoding used for internally
// generated default invokes. Explicit invokes keep the original OP_RETURN
// parameter bytes unchanged in InvokeItem.Param.
type defaultInvokeParam struct {
	OrderType   int
	UnitPrice   string
	ExpectedAmt string
}

func (p defaultInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.UnitPrice)).
		AddData([]byte(p.ExpectedAmt)).
		Script()
}

func (p *defaultInvokeParam) Decode(data []byte) error {
	if p == nil {
		return fmt.Errorf("nil default invoke param")
	}
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing default invoke order type")
	}
	p.OrderType = int(tokenizer.ExtractInt64())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing default invoke unit price")
	}
	p.UnitPrice = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing default invoke expected amount")
	}
	p.ExpectedAmt = string(tokenizer.Data())
	if tokenizer.Next() {
		return fmt.Errorf("unexpected default invoke param fields")
	}
	return tokenizer.Err()
}

type invokeItemParamView struct {
	OrderType     int
	UnitPrice     string
	ExpectedAmt   *scommon.Decimal
	BlobKeyLimit  uint32
	RefundItemIDs []int64
}

func encodeDefaultInvokeItemParam(orderType int, unitPrice string) ([]byte, error) {
	return (defaultInvokeParam{OrderType: orderType, UnitPrice: unitPrice}).Encode()
}

func decodeInvokeItemParam(item *InvokeItem) (invokeItemParamView, error) {
	var view invokeItemParamView
	if item == nil {
		return view, fmt.Errorf("nil invoke item")
	}

	switch item.Action {
	case contractcommon.ContractInvokeAPIDefault:
		var param defaultInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = param.OrderType
		view.UnitPrice = param.UnitPrice
		if param.ExpectedAmt != "" {
			view.ExpectedAmt = parseDecimalOrZero(param.ExpectedAmt)
		}
	case InvokeAPISwap:
		var param LimitOrderInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = param.OrderType
		view.UnitPrice = param.UnitPrice
		if param.Amt != "" {
			view.ExpectedAmt = parseDecimalOrZero(param.Amt)
		}
	case InvokeAPIRefund:
		var param RefundInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = OrderTypeRefund
		view.RefundItemIDs = append([]int64(nil), param.ItemIDs...)
	case InvokeAPIAddLiquidity:
		var param AddLiquidityInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = param.OrderType
		if param.Amt != "" {
			view.ExpectedAmt = parseDecimalOrZero(param.Amt)
		}
	case InvokeAPIRemoveLiquidity:
		var param RemoveLiquidityInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = param.OrderType
		if param.LptAmt != "" {
			view.ExpectedAmt = parseDecimalOrZero(param.LptAmt)
		}
	case InvokeAPIExchange:
		var param ExchangeInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = OrderTypeExchange
		if param.MinOutA != "" {
			view.ExpectedAmt = parseDecimalOrZero(param.MinOutA)
		}
	case InvokeAPIConfig:
		var param AutopayConfigInvokeParam
		if err := param.Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = OrderTypeValidate
		view.ExpectedAmt = parseDecimalOrZero(param.AmountPerBlock)
		view.BlobKeyLimit = normalizeAutopayBlobKeyLimit(param.BlobKeyLimit)
	case InvokeAPICancel:
		if err := (&CloseInvokeParam{}).Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = OrderTypeCancel
	case InvokeAPIClose:
		if err := (&CloseInvokeParam{}).Decode(item.Param); err != nil {
			return view, err
		}
		view.OrderType = OrderTypeClose
	default:
		return view, fmt.Errorf("unsupported invoke action %s", item.Action)
	}
	return view, nil
}

func invokeItemOrderType(item *InvokeItem) (int, error) {
	view, err := decodeInvokeItemParam(item)
	return view.OrderType, err
}

func invokeItemUnitPrice(item *InvokeItem) (string, error) {
	view, err := decodeInvokeItemParam(item)
	return view.UnitPrice, err
}

func invokeItemExpectedAmt(item *InvokeItem) (*scommon.Decimal, error) {
	view, err := decodeInvokeItemParam(item)
	if view.ExpectedAmt == nil {
		return nil, err
	}
	return view.ExpectedAmt.Clone(), err
}

func invokeItemRefundIDs(item *InvokeItem) ([]int64, error) {
	view, err := decodeInvokeItemParam(item)
	return append([]int64(nil), view.RefundItemIDs...), err
}

func invokeItemAutopayConfig(item *InvokeItem) (*scommon.Decimal, uint32, error) {
	view, err := decodeInvokeItemParam(item)
	if err != nil {
		return nil, 0, err
	}
	if view.OrderType != OrderTypeValidate || view.ExpectedAmt == nil {
		return nil, 0, fmt.Errorf("invoke item is not an autopay config")
	}
	return view.ExpectedAmt.Clone(), view.BlobKeyLimit, nil
}

func invokeItemOrderTypeOrNoSpec(item *InvokeItem) int {
	orderType, err := invokeItemOrderType(item)
	if err != nil {
		return OrderTypeNoSpec
	}
	return orderType
}

func invokeItemUnitPriceOrEmpty(item *InvokeItem) string {
	unitPrice, err := invokeItemUnitPrice(item)
	if err != nil {
		return ""
	}
	return unitPrice
}

func invokeItemExpectedAmtOrNil(item *InvokeItem) *scommon.Decimal {
	amount, err := invokeItemExpectedAmt(item)
	if err != nil {
		return nil
	}
	return amount
}
