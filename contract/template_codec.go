package contract

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	TemplateLimitOrder = "limitorder.tc"
	TemplateSwapLegacy = "swap.tc"
	TemplateAMM        = "amm.tc"
	TemplateExchange   = "exchange.tc"
	TemplateAutopay    = "autopay.tc"
)

const (
	TemplateInvokeAPISwap            = "swap"
	TemplateInvokeAPIRefund          = "refund"
	TemplateInvokeAPIAddLiquidity    = "addliq"
	TemplateInvokeAPIRemoveLiquidity = "removeliq"
	TemplateInvokeAPIProfit          = "profit"
	TemplateInvokeAPIExchange        = "exchange"
	TemplateInvokeAPIConfig          = "config"
	TemplateInvokeAPICancel          = "cancel"
	TemplateInvokeAPIClose           = "close"
)

const (
	CurrentTemplateVersion uint32 = 1
	MaxPriceDivisibility          = 10
	SwapInvokeFee          int64  = 0
)

var templateInvokeActions = map[string]map[string]struct{}{
	TemplateLimitOrder: {
		TemplateInvokeAPISwap:   {},
		TemplateInvokeAPIRefund: {},
		TemplateInvokeAPIClose:  {},
	},
	TemplateAMM: {
		TemplateInvokeAPISwap:            {},
		TemplateInvokeAPIAddLiquidity:    {},
		TemplateInvokeAPIRemoveLiquidity: {},
		TemplateInvokeAPIClose:           {},
	},
	TemplateExchange: {
		TemplateInvokeAPIExchange: {},
		TemplateInvokeAPIClose:    {},
	},
	TemplateAutopay: {
		TemplateInvokeAPIConfig: {},
		TemplateInvokeAPICancel: {},
		TemplateInvokeAPIClose:  {},
	},
}

const (
	OrderTypeNoSpec          = 0
	OrderTypeSell            = 1
	OrderTypeBuy             = 2
	OrderTypeRefund          = 3
	OrderTypeFund            = 4
	OrderTypeProfit          = 5
	OrderTypeDeposit         = 6
	OrderTypeWithdraw        = 7
	OrderTypeMint            = 8
	OrderTypeAddLiquidity    = 9
	OrderTypeRemoveLiquidity = 10
	OrderTypeStake           = 11
	OrderTypeUnstake         = 12
	OrderTypeRecycle         = 13
	OrderTypeReward          = 14
	OrderTypeRegister        = 15
	OrderTypeDonate          = 16
	OrderTypeAirdrop         = 17
	OrderTypeValidate        = 18
	OrderTypeBind            = 19
	OrderTypeClose           = 20
	OrderTypeExchange        = 21
	OrderTypeCancel          = 22
	OrderTypeUnused          = 23
)

const (
	ExchangePriceModeHeight = "height"
	ExchangePriceModeSoldA  = "sold_a"
	MaxExchangePriceSteps   = 128
)

type TemplateLimitOrderInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"`
	Amt       string `json:"amt"`
	UnitPrice string `json:"unitPrice"`
}

type TemplateAddLiquidityInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"`
	Amt       string `json:"amt"`
	Value     int64  `json:"value"`
}

type TemplateRemoveLiquidityInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"`
	LptAmt    string `json:"lptAmt"`
}

type TemplateExchangePriceStep struct {
	Threshold string `json:"threshold"`
	BPerA     string `json:"bPerA"`
}

type TemplateExchangeContract struct {
	AssetAName string                      `json:"assetAName"`
	AssetBName string                      `json:"assetBName"`
	PriceMode  string                      `json:"priceMode"`
	Steps      []TemplateExchangePriceStep `json:"steps"`
}

type TemplateAutopayContract struct {
	ServiceName       string `json:"serviceName"`
	Recipient         string `json:"recipient"`
	FeeAssetName      string `json:"feeAssetName"`
	MinAmountPerBlock string `json:"minAmountPerBlock"`
}

type TemplateExchangeInvokeParam struct {
	MinOutA string `json:"minOutA,omitempty"`
}

type TemplateAutopayConfigInvokeParam struct {
	AmountPerBlock string `json:"amountPerBlock"`
	BlobKeyLimit   uint32 `json:"blobKeyLimit"`
}

type TemplateCloseInvokeParam struct{}

type TemplateRefundInvokeParam struct {
	ItemIDs []int64 `json:"itemIds,omitempty"`
}

func NormalizeTemplateName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case TemplateSwapLegacy, TemplateLimitOrder:
		return TemplateLimitOrder
	case TemplateAMM:
		return TemplateAMM
	case TemplateExchange:
		return TemplateExchange
	case TemplateAutopay:
		return TemplateAutopay
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func TemplateInvokeActions(templateName string) []string {
	actions, ok := templateInvokeActions[NormalizeTemplateName(templateName)]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(actions))
	for action := range actions {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

func IsTemplateInvokeActionSupported(templateName, action string) bool {
	actions, ok := templateInvokeActions[NormalizeTemplateName(templateName)]
	if !ok {
		return false
	}
	_, ok = actions[strings.ToLower(strings.TrimSpace(action))]
	return ok
}

func IsKnownTemplateName(templateName string) bool {
	_, ok := templateInvokeActions[NormalizeTemplateName(templateName)]
	return ok
}

func EncodeTemplateLimitOrderContent(assetName string) ([]byte, error) {
	if err := checkTemplateAssetName(assetName); err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(assetName)).
		Script()
}

func EncodeTemplateAMMContent(assetName string, assetAmt string, satValue int64, k string) ([]byte, error) {
	if err := checkTemplateAssetName(assetName); err != nil {
		return nil, err
	}
	if satValue <= 0 {
		return nil, fmt.Errorf("invalid sat value %d", satValue)
	}
	assetAmount, err := parsePositiveDecimal("asset amt", assetAmt)
	if err != nil {
		return nil, err
	}
	kValue, err := parsePositiveDecimal("K", k)
	if err != nil {
		return nil, err
	}
	expectedK := indexercommon.DecimalMul(assetAmount, indexercommon.NewDefaultDecimal(satValue))
	if kValue.Cmp(expectedK) != 0 {
		return nil, fmt.Errorf("k is not the result of assetAmt*satValue")
	}
	base, err := EncodeTemplateLimitOrderContent(assetName)
	if err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(assetAmt)).
		AddInt64(satValue).
		AddData([]byte(k)).
		Script()
}

func EncodeTemplateExchangeContent(contract TemplateExchangeContract) ([]byte, error) {
	if err := contract.Check(); err != nil {
		return nil, err
	}
	builder := txscript.NewScriptBuilder().
		AddData([]byte(contract.AssetAName)).
		AddData([]byte(contract.AssetBName)).
		AddData([]byte(contract.PriceMode)).
		AddInt64(int64(len(contract.Steps)))
	for _, step := range contract.Steps {
		builder.AddData([]byte(step.Threshold)).
			AddData([]byte(step.BPerA))
	}
	return builder.Script()
}

func EncodeTemplateAutopayContent(contract TemplateAutopayContract) ([]byte, error) {
	if err := contract.Check(); err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(contract.ServiceName)).
		AddData([]byte(contract.Recipient)).
		AddData([]byte(contract.FeeAssetName)).
		AddData([]byte(contract.MinAmountPerBlock)).
		Script()
}

func (p *TemplateAutopayConfigInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.AmountPerBlock)).
		AddInt64(int64(p.BlobKeyLimit)).
		Script()
}

func (p *TemplateAutopayConfigInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing amount per block")
	}
	p.AmountPerBlock = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing blob key limit")
	}
	limit := tokenizer.ExtractInt64()
	if limit < 0 || limit > 1024 {
		return fmt.Errorf("invalid blob key limit")
	}
	p.BlobKeyLimit = uint32(limit)
	if tokenizer.Next() {
		return fmt.Errorf("unexpected autopay config fields")
	}
	return tokenizer.Err()
}

func (p *TemplateLimitOrderInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddData([]byte(p.UnitPrice)).
		Script()
}

func (p *TemplateLimitOrderInvokeParam) Decode(data []byte) error {
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

func (p *TemplateRefundInvokeParam) Encode() ([]byte, error) {
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

func (p *TemplateRefundInvokeParam) Decode(data []byte) error {
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

func (p *TemplateAddLiquidityInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddInt64(p.Value).
		Script()
}

func (p *TemplateAddLiquidityInvokeParam) Decode(data []byte) error {
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

func (p *TemplateRemoveLiquidityInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.LptAmt)).
		Script()
}

func (p *TemplateRemoveLiquidityInvokeParam) Decode(data []byte) error {
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

func (p *TemplateExchangeInvokeParam) Encode() ([]byte, error) {
	if p == nil || p.MinOutA == "" {
		return nil, nil
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(p.MinOutA)).
		Script()
}

func (p *TemplateExchangeInvokeParam) Decode(data []byte) error {
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

func (p *TemplateCloseInvokeParam) Encode() ([]byte, error) {
	return nil, nil
}

func (p *TemplateCloseInvokeParam) Decode(data []byte) error {
	if len(data) != 0 {
		return fmt.Errorf("close takes no parameters")
	}
	return nil
}

func (c TemplateExchangeContract) Check() error {
	if err := checkTemplateAssetName(c.AssetAName); err != nil {
		return fmt.Errorf("invalid asset A: %w", err)
	}
	if c.AssetBName == "" {
		return fmt.Errorf("invalid asset B %s", c.AssetBName)
	}
	if c.AssetBName != SatoshiAssetName {
		if err := checkTemplateAssetName(c.AssetBName); err != nil {
			return fmt.Errorf("invalid asset B: %w", err)
		}
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
	if len(c.Steps) > MaxExchangePriceSteps {
		return fmt.Errorf("exchange price step count %d exceeds maximum %d", len(c.Steps), MaxExchangePriceSteps)
	}
	var prevHeight int64 = -1
	var prevSold *indexercommon.Decimal
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

func (c TemplateAutopayContract) Check() error {
	if strings.TrimSpace(c.ServiceName) == "" {
		return fmt.Errorf("autopay service name is empty")
	}
	if err := checkTemplatePayableAssetName(c.FeeAssetName); err != nil {
		return fmt.Errorf("invalid fee asset: %w", err)
	}
	minAmount, err := parsePositiveDecimal("minimum amount per block", c.MinAmountPerBlock)
	if err != nil {
		return err
	}
	if c.FeeAssetName == SatoshiAssetName {
		if minAmount.Cmp(minAmount.NewPrecision(0)) != 0 {
			return fmt.Errorf("satoshi autopay minimum amount must be an integer")
		}
	}
	return nil
}

func checkTemplateAssetName(assetName string) error {
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	if name.Type != indexercommon.ASSET_TYPE_FT {
		return fmt.Errorf("invalid asset type %s", name.Type)
	}
	if name.Protocol == "" || name.Ticker == "" {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	return nil
}

func checkTemplatePayableAssetName(assetName string) error {
	if assetName == SatoshiAssetName {
		return nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	if name.Protocol == "" || name.Ticker == "" {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	return nil
}

func parsePositiveDecimal(field, value string) (*indexercommon.Decimal, error) {
	if value == "" || value == "0" {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	decimal, err := indexercommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	if decimal.Cmp(indexercommon.NewDefaultDecimal(0)) <= 0 {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	return decimal, nil
}

func parseNonNegativeDecimal(field, value string) (*indexercommon.Decimal, error) {
	if value == "" {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	decimal, err := indexercommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	if decimal.Sign() < 0 {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	return decimal, nil
}
