package template

import (
	"fmt"
	"sort"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

type DepthInfo struct {
	Price string `json:"price"`
	Amt   string `json:"amt"`
	Value int64  `json:"value"`
}

var _ contractframework.StateViewProvider = (*ContractRuntime)(nil)

type TemplateStateView struct {
	TemplateName string   `json:"templateName"`
	Version      uint32   `json:"version"`
	AssetName    string   `json:"assetName,omitempty"`
	Assets       []string `json:"assets,omitempty"`
	Deployer     string   `json:"deployer,omitempty"`
	InvokeCount  uint64   `json:"invokeCount"`
	CurrentBlock int64    `json:"currentBlock,omitempty"`
}

type LimitOrderStateView struct {
	TemplateStateView
	TradingReady    bool         `json:"tradingReady"`
	ActiveBuyCount  int          `json:"activeBuyCount"`
	ActiveSellCount int          `json:"activeSellCount"`
	BuyDepth        []*DepthInfo `json:"buyDepth"`
	SellDepth       []*DepthInfo `json:"sellDepth"`
	AssetAInPool    string       `json:"assetAInPool,omitempty"`
	AssetBInPool    string       `json:"assetBInPool,omitempty"`
	TotalDealCount  int          `json:"totalDealCount"`
}

type AMMStateView struct {
	TemplateStateView
	TradingReady   bool              `json:"tradingReady"`
	AssetAInPool   string            `json:"assetAInPool,omitempty"`
	AssetBInPool   string            `json:"assetBInPool,omitempty"`
	RequiredAssetA string            `json:"requiredAssetA,omitempty"`
	RequiredAssetB string            `json:"requiredAssetB,omitempty"`
	RequiredK      string            `json:"requiredK,omitempty"`
	K              string            `json:"k,omitempty"`
	TotalLPTAmt    string            `json:"totalLptAmt,omitempty"`
	LPBalances     map[string]string `json:"lpBalances,omitempty"`
	TotalDealCount int               `json:"totalDealCount"`
	Closed         bool              `json:"closed,omitempty"`
}

type AutopayStateView struct {
	TemplateStateView
	ServiceName       string                         `json:"serviceName"`
	Recipient         string                         `json:"recipient"`
	FeeAssetName      string                         `json:"feeAssetName"`
	MinAmountPerBlock string                         `json:"minAmountPerBlock"`
	Status            string                         `json:"status"`
	FeeBalance        string                         `json:"feeBalance,omitempty"`
	GasBalance        string                         `json:"gasBalance,omitempty"`
	ActiveHeight      int64                          `json:"activeHeight,omitempty"`
	NextPayHeight     int64                          `json:"nextPayHeight,omitempty"`
	LastPayHeight     int64                          `json:"lastPayHeight,omitempty"`
	PaidBlocks        int64                          `json:"paidBlocks,omitempty"`
	Closed            bool                           `json:"closed,omitempty"`
	Delegates         map[string]AutopayDelegateView `json:"delegates,omitempty"`
}

type AutopayDelegateView struct {
	AmountPerBlock string `json:"amountPerBlock,omitempty"`
	BlobKeyLimit   uint32 `json:"blobKeyLimit,omitempty"`
	Balance        string `json:"balance,omitempty"`
	TotalPaid      string `json:"totalPaid,omitempty"`
	PaidBlockCount int64  `json:"paidBlockCount,omitempty"`
	LastPayHeight  int64  `json:"lastPayHeight,omitempty"`
	Status         string `json:"status,omitempty"`
}

func (r *ContractRuntime) StateView(ctx contractframework.StateViewContext) (interface{}, error) {
	state, err := r.RuntimeState()
	if err != nil {
		return nil, err
	}
	base := r.templateStateViewBase(state)
	switch contract := r.Contract().(type) {
	case *AMMContract:
		running := state.AMMData()
		view := AMMStateView{
			TemplateStateView: base,
			TradingReady:      running.TradingReady,
			AssetAInPool:      decimalString(running.AssetAInPool),
			AssetBInPool:      decimalString(running.AssetBInPool),
			RequiredAssetA:    decimalString(running.RequiredAssetA),
			RequiredAssetB:    decimalString(running.RequiredAssetB),
			RequiredK:         decimalString(running.RequiredK),
			K:                 decimalString(running.K),
			TotalLPTAmt:       decimalString(running.TotalLPTAmt),
			LPBalances:        decimalStringMap(running.LPBalances),
			TotalDealCount:    running.TotalDealCount,
			Closed:            running.Closed,
		}
		view.AssetName = contract.AssetName
		view.Assets = templateViewAssets(contract.AssetName, SatoshiAssetName)
		return view, nil
	case *LimitOrderContract:
		running := state.LimitOrderData()
		buyIDs, sellIDs := activeLimitOrderIDs(state.Items, ctx.Height)
		sortLimitOrders(state.Items, buyIDs, true)
		sortLimitOrders(state.Items, sellIDs, false)
		buyDepth, err := limitOrderDepth(state.Items, buyIDs, true)
		if err != nil {
			return nil, err
		}
		sellDepth, err := limitOrderDepth(state.Items, sellIDs, false)
		if err != nil {
			return nil, err
		}
		view := LimitOrderStateView{
			TemplateStateView: base,
			TradingReady:      running.TradingReady,
			ActiveBuyCount:    len(buyIDs),
			ActiveSellCount:   len(sellIDs),
			BuyDepth:          buyDepth,
			SellDepth:         sellDepth,
			AssetAInPool:      decimalString(running.AssetAInPool),
			AssetBInPool:      decimalString(running.AssetBInPool),
			TotalDealCount:    running.TotalDealCount,
		}
		view.AssetName = contract.AssetName
		view.Assets = templateViewAssets(contract.AssetName, SatoshiAssetName)
		return view, nil
	case *AutopayContract:
		running := state.AutopayData()
		status := running.AutopayStatus
		if status == "" {
			status = AutopayStatusFunding
		}
		view := AutopayStateView{
			TemplateStateView: base,
			ServiceName:       contract.ServiceName,
			Recipient:         contract.Recipient,
			FeeAssetName:      contract.FeeAssetName,
			MinAmountPerBlock: contract.MinAmountPerBlock,
			Status:            status,
			FeeBalance:        decimalString(running.FeeBalance),
			GasBalance:        decimalString(running.GasBalance),
			ActiveHeight:      running.ActiveHeight,
			NextPayHeight:     running.NextPayHeight,
			LastPayHeight:     running.LastPayHeight,
			PaidBlocks:        running.PaidBlockCount,
			Closed:            running.Closed,
			Delegates:         autopayDelegateViewMap(running.AutopayDelegates),
		}
		view.AssetName = contract.FeeAssetName
		view.Assets = templateViewAssets(contract.FeeAssetName)
		return view, nil
	default:
		return base, nil
	}
}

func autopayDelegateViewMap(in map[string]AutopayDelegate) map[string]AutopayDelegateView {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]AutopayDelegateView, len(in))
	for address, delegate := range in {
		out[address] = AutopayDelegateView{
			AmountPerBlock: decimalString(delegate.AmountPerBlock),
			BlobKeyLimit:   delegate.BlobKeyLimit,
			Balance:        decimalString(delegate.Balance),
			TotalPaid:      decimalString(delegate.TotalPaid),
			PaidBlockCount: delegate.PaidBlockCount,
			LastPayHeight:  delegate.LastPayHeight,
			Status:         delegate.Status,
		}
	}
	return out
}

func (r *ContractRuntime) templateStateViewBase(state TemplateRuntimeState) TemplateStateView {
	return TemplateStateView{
		TemplateName: r.TemplateName(),
		Version:      r.Version(),
		Deployer:     r.RuntimeBase().Deployer(),
		InvokeCount:  state.InvokeCount,
		CurrentBlock: r.CurrentBlock(),
	}
}

func templateViewAssets(assets ...string) []string {
	out := make([]string, 0, len(assets))
	seen := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		if asset == "" {
			continue
		}
		if _, ok := seen[asset]; ok {
			continue
		}
		seen[asset] = struct{}{}
		out = append(out, asset)
	}
	return out
}

func limitOrderDepth(items []InvokeItem, ids []int, buy bool) ([]*DepthInfo, error) {
	type depthRow struct {
		price string
		amt   string
		value int64
	}
	rows := make(map[string]*depthRow)
	for _, id := range ids {
		if id < 0 || id >= len(items) {
			continue
		}
		item := items[id]
		price, err := invokeItemUnitPrice(&item)
		if err != nil {
			return nil, err
		}
		if price == "" {
			price = "0"
		}
		row, ok := rows[price]
		if !ok {
			row = &depthRow{price: price}
			rows[price] = row
		}
		if buy {
			var overflow bool
			row.value, overflow = contractframework.AddInt64(row.value, item.RemainingValue)
			if overflow {
				return nil, fmt.Errorf("limit order buy depth value overflows int64")
			}
			amt, err := invokeItemExpectedAmt(&item)
			if err != nil {
				return nil, err
			}
			if amt == nil {
				amt = parseDecimalOrZero("0")
			}
			if item.OutAmt != nil {
				amt = amt.Sub(item.OutAmt)
			}
			row.amt = decimalString(parseDecimalOrZero(row.amt).Add(amt))
			continue
		}
		if item.RemainingAmt != nil {
			row.amt = decimalString(parseDecimalOrZero(row.amt).Add(item.RemainingAmt))
			value, err := parseDecimalOrZero(price).Mul(item.RemainingAmt).FloorInt64()
			if err != nil {
				return nil, fmt.Errorf("limit order sell depth value: %w", err)
			}
			var overflow bool
			row.value, overflow = contractframework.AddInt64(row.value, value)
			if overflow {
				return nil, fmt.Errorf("limit order sell depth value overflows int64")
			}
		}
	}
	out := make([]*DepthInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, &DepthInfo{Price: row.price, Amt: row.amt, Value: row.value})
	}
	sort.Slice(out, func(i, j int) bool {
		cmp := parseDecimalOrZero(out[i].Price).Cmp(parseDecimalOrZero(out[j].Price))
		if buy {
			return cmp > 0
		}
		return cmp < 0
	})
	return out, nil
}
