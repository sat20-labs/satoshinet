package common

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	TemplateLimitOrder = "limitorder.tc"
	TemplateSwapLegacy = "swap.tc"
	TemplateAMM        = "amm.tc"
	TemplateExchange   = "exchange.tc"
)

const (
	TemplateInvokeAPISwap            = "swap"
	TemplateInvokeAPIRefund          = "refund"
	TemplateInvokeAPIAddLiquidity    = "addliq"
	TemplateInvokeAPIRemoveLiquidity = "removeliq"
	TemplateInvokeAPIProfit          = "profit"
	TemplateInvokeAPIExchange        = "exchange"
	TemplateInvokeAPIClose           = "close"
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
}

func NormalizeTemplateName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case TemplateSwapLegacy, TemplateLimitOrder:
		return TemplateLimitOrder
	case TemplateAMM:
		return TemplateAMM
	case TemplateExchange:
		return TemplateExchange
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

type TemplateRefundInvokeParam struct {
	ItemIDs []int64 `json:"itemIds,omitempty"`
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
