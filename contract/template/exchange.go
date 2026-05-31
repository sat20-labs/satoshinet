package template

import (
	"fmt"
	"strconv"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	ExchangePriceModeHeight = "height"
	ExchangePriceModeSoldA  = "sold_a"
)

type ExchangePriceStep struct {
	Threshold string `json:"threshold"`
	BPerA     string `json:"bPerA"`
}

type ExchangeContract struct {
	AssetAName string              `json:"assetAName"`
	AssetBName string              `json:"assetBName"`
	PriceMode  string              `json:"priceMode"`
	Steps      []ExchangePriceStep `json:"steps"`
}

func NewExchangeContract(assetAName, assetBName, priceMode string, steps []ExchangePriceStep) *ExchangeContract {
	return &ExchangeContract{
		AssetAName: assetAName,
		AssetBName: assetBName,
		PriceMode:  priceMode,
		Steps:      append([]ExchangePriceStep(nil), steps...),
	}
}

func (c *ExchangeContract) TemplateName() string {
	return TemplateExchange
}

func (c *ExchangeContract) Version() uint32 {
	return CurrentTemplateVersion
}

func (c *ExchangeContract) Encode() ([]byte, error) {
	builder := txscript.NewScriptBuilder().
		AddData([]byte(c.AssetAName)).
		AddData([]byte(c.AssetBName)).
		AddData([]byte(c.PriceMode)).
		AddInt64(int64(len(c.Steps)))
	for _, step := range c.Steps {
		builder.AddData([]byte(step.Threshold)).
			AddData([]byte(step.BPerA))
	}
	return builder.Script()
}

func (c *ExchangeContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset A name")
	}
	c.AssetAName = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset B name")
	}
	c.AssetBName = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing price mode")
	}
	c.PriceMode = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing price step count")
	}
	count := tokenizer.ExtractInt64()
	if count < 0 {
		return fmt.Errorf("invalid price step count %d", count)
	}
	c.Steps = make([]ExchangePriceStep, 0, count)
	for i := int64(0); i < count; i++ {
		if !tokenizer.Next() || tokenizer.Err() != nil {
			return fmt.Errorf("missing price step %d threshold", i)
		}
		threshold := string(tokenizer.Data())
		if !tokenizer.Next() || tokenizer.Err() != nil {
			return fmt.Errorf("missing price step %d bPerA", i)
		}
		c.Steps = append(c.Steps, ExchangePriceStep{
			Threshold: threshold,
			BPerA:     string(tokenizer.Data()),
		})
	}
	if tokenizer.Next() {
		return fmt.Errorf("unexpected exchange contract content")
	}
	return tokenizer.Err()
}

func (c *ExchangeContract) CheckContent() error {
	if err := checkTemplateAssetName(c.AssetAName); err != nil {
		return fmt.Errorf("invalid asset A: %w", err)
	}
	if err := checkTemplateAssetName(c.AssetBName); err != nil {
		return fmt.Errorf("invalid asset B: %w", err)
	}
	if c.AssetAName == c.AssetBName {
		return fmt.Errorf("asset A and asset B must be different")
	}
	switch c.PriceMode {
	case ExchangePriceModeHeight, ExchangePriceModeSoldA:
	default:
		return fmt.Errorf("unsupported exchange price mode %s", c.PriceMode)
	}
	if len(c.Steps) == 0 {
		return fmt.Errorf("missing exchange price steps")
	}
	var prevHeight int64 = -1
	var prevSold *scommon.Decimal
	for i, step := range c.Steps {
		if _, err := parsePositiveDecimal("bPerA", step.BPerA); err != nil {
			return err
		}
		switch c.PriceMode {
		case ExchangePriceModeHeight:
			height, err := strconv.ParseInt(step.Threshold, 10, 64)
			if err != nil || height < 0 {
				return fmt.Errorf("invalid height threshold %s", step.Threshold)
			}
			if i == 0 && height != 0 {
				return fmt.Errorf("first height threshold must be zero")
			}
			if height <= prevHeight {
				return fmt.Errorf("height thresholds must be strictly increasing")
			}
			prevHeight = height
		case ExchangePriceModeSoldA:
			sold, err := parseNonNegativeDecimal("sold A threshold", step.Threshold)
			if err != nil {
				return err
			}
			if i == 0 && sold.Sign() != 0 {
				return fmt.Errorf("first sold A threshold must be zero")
			}
			if prevSold != nil && sold.Cmp(prevSold) <= 0 {
				return fmt.Errorf("sold A thresholds must be strictly increasing")
			}
			prevSold = sold
		}
	}
	return nil
}

func (c *ExchangeContract) CheckInvoke(action string, param []byte) error {
	switch action {
	case InvokeAPIExchange:
		var invokeParam ExchangeInvokeParam
		if err := invokeParam.Decode(param); err != nil {
			return err
		}
		return invokeParam.Check()
	case InvokeAPIClose:
		return (&CloseInvokeParam{}).Decode(param)
	default:
		return fmt.Errorf("unsupported exchange action %s", action)
	}
}

type ExchangeInvokeParam struct {
	MinOutA string `json:"minOutA,omitempty"`
}

func (p *ExchangeInvokeParam) Encode() ([]byte, error) {
	if p == nil || p.MinOutA == "" {
		return nil, nil
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(p.MinOutA)).
		Script()
}

func (p *ExchangeInvokeParam) Decode(data []byte) error {
	p.MinOutA = ""
	if len(data) == 0 {
		return nil
	}
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing min out A")
	}
	p.MinOutA = string(tokenizer.Data())
	if tokenizer.Next() {
		return fmt.Errorf("unexpected exchange invoke param")
	}
	return tokenizer.Err()
}

func (p ExchangeInvokeParam) Check() error {
	if p.MinOutA == "" {
		return nil
	}
	_, err := parsePositiveDecimal("min out A", p.MinOutA)
	return err
}

type CloseInvokeParam struct{}

func (p *CloseInvokeParam) Encode() ([]byte, error) {
	return nil, nil
}

func (p *CloseInvokeParam) Decode(data []byte) error {
	if len(data) != 0 {
		return fmt.Errorf("close takes no parameters")
	}
	return nil
}

func parseNonNegativeDecimal(field, value string) (*scommon.Decimal, error) {
	if value == "" {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	decimal, err := scommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	if decimal.Sign() < 0 {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	return decimal, nil
}
