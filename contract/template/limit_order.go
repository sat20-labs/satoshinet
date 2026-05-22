package template

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type LimitOrderContract struct {
	AssetName string `json:"assetName"`
}

func NewLimitOrderContract(assetName string) *LimitOrderContract {
	return &LimitOrderContract{AssetName: assetName}
}

func (c *LimitOrderContract) TemplateName() string {
	return TemplateLimitOrder
}

func (c *LimitOrderContract) Version() uint32 {
	return CurrentTemplateVersion
}

func (c *LimitOrderContract) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(c.AssetName)).
		Script()
}

func (c *LimitOrderContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	c.AssetName = string(tokenizer.Data())
	return nil
}

func (c *LimitOrderContract) CheckContent() error {
	return checkTemplateAssetName(c.AssetName)
}

func (c *LimitOrderContract) CheckInvoke(action string, param []byte) error {
	switch action {
	case InvokeAPISwap:
	case InvokeAPIRefund:
		var refundParam RefundInvokeParam
		return refundParam.Decode(param)
	default:
		return fmt.Errorf("unsupported limit order action %s", action)
	}

	var invokeParam LimitOrderInvokeParam
	if err := invokeParam.Decode(param); err != nil {
		return err
	}
	if invokeParam.AssetName != "" && invokeParam.AssetName != c.AssetName {
		return fmt.Errorf("asset name mismatch %s != %s", invokeParam.AssetName, c.AssetName)
	}
	return invokeParam.Check(action)
}

type LimitOrderInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"`
	Amt       string `json:"amt"`
	UnitPrice string `json:"unitPrice"`
}

func (p *LimitOrderInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddData([]byte(p.UnitPrice)).
		Script()
}

func (p *LimitOrderInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing order type")
	}
	p.OrderType = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	p.AssetName = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset amt")
	}
	p.Amt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing unit price")
	}
	p.UnitPrice = string(tokenizer.Data())
	return nil
}

func (p *LimitOrderInvokeParam) Check(action string) error {
	switch action {
	case InvokeAPISwap:
		if p.OrderType != OrderTypeBuy && p.OrderType != OrderTypeSell {
			return fmt.Errorf("invalid swap order type %d", p.OrderType)
		}
	default:
		return fmt.Errorf("unsupported action %s", action)
	}
	if p.AssetName != "" {
		if err := checkTemplateAssetName(p.AssetName); err != nil {
			return err
		}
	}
	if _, err := parsePositiveDecimal("amt", p.Amt); err != nil {
		return err
	}
	if p.UnitPrice != "" {
		if _, err := parsePositiveDecimal("unit price", p.UnitPrice); err != nil {
			return err
		}
	}
	return nil
}

type RefundInvokeParam struct {
	ItemIDs []int64 `json:"itemIds,omitempty"`
}

func (p *RefundInvokeParam) Encode() ([]byte, error) {
	if p == nil || len(p.ItemIDs) == 0 {
		return nil, nil
	}
	builder := txscript.NewScriptBuilder()
	for _, itemID := range p.ItemIDs {
		if itemID < 0 {
			return nil, fmt.Errorf("invalid refund item id %d", itemID)
		}
		builder.AddInt64(itemID)
	}
	return builder.Script()
}

func (p *RefundInvokeParam) Decode(data []byte) error {
	p.ItemIDs = nil
	if len(data) == 0 {
		return nil
	}
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	for tokenizer.Next() {
		itemID := tokenizer.ExtractInt64()
		if itemID < 0 {
			return fmt.Errorf("invalid refund item id %d", itemID)
		}
		p.ItemIDs = append(p.ItemIDs, itemID)
	}
	return tokenizer.Err()
}

func checkTemplateAssetName(assetName string) error {
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	if name.Type != scommon.ASSET_TYPE_FT {
		return fmt.Errorf("invalid asset type %s", name.Type)
	}
	if name.Protocol == "" || name.Ticker == "" {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	return nil
}

func parsePositiveDecimal(field, value string) (*scommon.Decimal, error) {
	if value == "" || value == "0" {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	decimal, err := scommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	if decimal.Cmp(scommon.NewDefaultDecimal(0)) <= 0 {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	return decimal, nil
}
