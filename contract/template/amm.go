package template

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
)

type AMMContract struct {
	LimitOrderContract
	AssetAmt string `json:"assetAmt"`
	SatValue int64  `json:"satValue"`
	K        string `json:"k"`
}

func NewAMMContract(assetName string, assetAmt string, satValue int64, k string) *AMMContract {
	return &AMMContract{
		LimitOrderContract: *NewLimitOrderContract(assetName),
		AssetAmt:           assetAmt,
		SatValue:           satValue,
		K:                  k,
	}
}

func (c *AMMContract) TemplateName() string {
	return TemplateAMM
}

func (c *AMMContract) ApplyRunningData(running *RunningData, item *InvokeItem) bool {
	return false
}

func (c *AMMContract) Encode() ([]byte, error) {
	base, err := c.LimitOrderContract.Encode()
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(c.AssetAmt)).
		AddInt64(c.SatValue).
		AddData([]byte(c.K)).
		Script()
}

func (c *AMMContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing base content")
	}
	if err := c.LimitOrderContract.Decode(tokenizer.Data()); err != nil {
		return err
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset amt")
	}
	c.AssetAmt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing sat value")
	}
	c.SatValue = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing K parameter")
	}
	c.K = string(tokenizer.Data())
	return nil
}

func (c *AMMContract) CheckContent() error {
	if err := c.LimitOrderContract.CheckContent(); err != nil {
		return err
	}
	if c.SatValue <= 0 {
		return fmt.Errorf("invalid sat value %d", c.SatValue)
	}
	assetAmt, err := parsePositiveDecimal("asset amt", c.AssetAmt)
	if err != nil {
		return err
	}
	k, err := parsePositiveDecimal("K", c.K)
	if err != nil {
		return err
	}
	expectedK := scommon.DecimalMul(assetAmt, scommon.NewDefaultDecimal(c.SatValue))
	if k.Cmp(expectedK) != 0 {
		return fmt.Errorf("k is not the result of assetAmt*satValue")
	}
	return nil
}

func (c *AMMContract) CheckInvoke(action string, param []byte) error {
	switch action {
	case InvokeAPISwap:
		var invokeParam LimitOrderInvokeParam
		if err := invokeParam.Decode(param); err != nil {
			return err
		}
		if invokeParam.AssetName != "" && invokeParam.AssetName != c.AssetName {
			return fmt.Errorf("asset name mismatch %s != %s", invokeParam.AssetName, c.AssetName)
		}
		return invokeParam.CheckAMMSwap()
	case InvokeAPIAddLiquidity:
		var invokeParam AddLiquidityInvokeParam
		if err := invokeParam.Decode(param); err != nil {
			return err
		}
		if invokeParam.AssetName != "" && invokeParam.AssetName != c.AssetName {
			return fmt.Errorf("asset name mismatch %s != %s", invokeParam.AssetName, c.AssetName)
		}
		return invokeParam.Check()
	case InvokeAPIRemoveLiquidity:
		var invokeParam RemoveLiquidityInvokeParam
		if err := invokeParam.Decode(param); err != nil {
			return err
		}
		if invokeParam.AssetName != "" && invokeParam.AssetName != c.AssetName {
			return fmt.Errorf("asset name mismatch %s != %s", invokeParam.AssetName, c.AssetName)
		}
		return invokeParam.Check()
	case InvokeAPIClose:
		return (&CloseInvokeParam{}).Decode(param)
	default:
		return fmt.Errorf("unsupported AMM action %s", action)
	}
}

func (p *LimitOrderInvokeParam) CheckAMMSwap() error {
	if p.OrderType != OrderTypeBuy && p.OrderType != OrderTypeSell {
		return fmt.Errorf("invalid swap order type %d", p.OrderType)
	}
	if p.AssetName != "" {
		if err := checkTemplateAssetName(p.AssetName); err != nil {
			return err
		}
	}
	if p.Amt != "" && p.Amt != "0" {
		if _, err := parsePositiveDecimal("amt", p.Amt); err != nil {
			return err
		}
	}
	if _, err := parsePositiveDecimal("unit price", p.UnitPrice); err != nil {
		return err
	}
	return nil
}

type AddLiquidityInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"`
	Amt       string `json:"amt"`
	Value     int64  `json:"value"`
}

func (p *AddLiquidityInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddInt64(p.Value).
		Script()
}

func (p *AddLiquidityInvokeParam) Decode(data []byte) error {
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
		return fmt.Errorf("missing sat value")
	}
	p.Value = tokenizer.ExtractInt64()
	return nil
}

func (p *AddLiquidityInvokeParam) Check() error {
	if p.OrderType != OrderTypeAddLiquidity {
		return fmt.Errorf("invalid add liquidity order type %d", p.OrderType)
	}
	if p.AssetName != "" {
		if err := checkTemplateAssetName(p.AssetName); err != nil {
			return err
		}
	}
	if _, err := parsePositiveDecimal("asset amt", p.Amt); err != nil {
		return err
	}
	if p.Value <= 0 {
		return fmt.Errorf("invalid sat value %d", p.Value)
	}
	return nil
}

type RemoveLiquidityInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"`
	LptAmt    string `json:"lptAmt"`
}

func (p *RemoveLiquidityInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.LptAmt)).
		Script()
}

func (p *RemoveLiquidityInvokeParam) Decode(data []byte) error {
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
		return fmt.Errorf("missing lpt amt")
	}
	p.LptAmt = string(tokenizer.Data())
	return nil
}

func (p *RemoveLiquidityInvokeParam) Check() error {
	if p.OrderType != OrderTypeRemoveLiquidity {
		return fmt.Errorf("invalid remove liquidity order type %d", p.OrderType)
	}
	if p.AssetName != "" {
		if err := checkTemplateAssetName(p.AssetName); err != nil {
			return err
		}
	}
	_, err := parsePositiveDecimal("lpt amt", p.LptAmt)
	return err
}
