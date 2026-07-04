package template

import (
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
	K              string            `json:"k,omitempty"`
	TotalLPTAmt    string            `json:"totalLptAmt,omitempty"`
	LPBalances     map[string]string `json:"lpBalances,omitempty"`
	TotalDealCount int               `json:"totalDealCount"`
	Closed         bool              `json:"closed,omitempty"`
}

type AutopayStateView struct {
	TemplateStateView
	Recipient     string `json:"recipient"`
	FeeAssetName  string `json:"feeAssetName"`
	ScheduleMode  string `json:"scheduleMode"`
	BaseAmount    string `json:"baseAmount"`
	StepAmount    string `json:"stepAmount,omitempty"`
	EndHeight     int64  `json:"endHeight,omitempty"`
	Status        string `json:"status"`
	FeeBalance    string `json:"feeBalance,omitempty"`
	GasBalance    string `json:"gasBalance,omitempty"`
	ActiveHeight  int64  `json:"activeHeight,omitempty"`
	NextPayHeight int64  `json:"nextPayHeight,omitempty"`
	LastPayHeight int64  `json:"lastPayHeight,omitempty"`
	PaidBlocks    int64  `json:"paidBlocks,omitempty"`
	Closed        bool   `json:"closed,omitempty"`
}

func (r *ContractRuntime) StateView(ctx contractframework.StateViewContext) (interface{}, error) {
	state, err := r.RuntimeState()
	if err != nil {
		return nil, err
	}
	base := r.templateStateViewBase(state)
	switch contract := r.Contract().(type) {
	case *AMMContract:
		view := AMMStateView{
			TemplateStateView: base,
			TradingReady:      state.Running.TradingReady,
			AssetAInPool:      decimalString(state.Running.AssetAInPool),
			AssetBInPool:      decimalString(state.Running.AssetBInPool),
			RequiredAssetA:    decimalString(state.Running.RequiredAssetA),
			RequiredAssetB:    decimalString(state.Running.RequiredAssetB),
			K:                 decimalString(state.Running.K),
			TotalLPTAmt:       decimalString(state.Running.TotalLPTAmt),
			LPBalances:        decimalStringMap(state.Running.LPBalances),
			TotalDealCount:    state.Running.TotalDealCount,
			Closed:            state.Running.Closed,
		}
		view.AssetName = contract.AssetName
		view.Assets = templateViewAssets(contract.AssetName, SatoshiAssetName)
		return view, nil
	case *LimitOrderContract:
		buyIDs, sellIDs := activeLimitOrderIDs(state.Items, ctx.Height)
		sortLimitOrders(state.Items, buyIDs, true)
		sortLimitOrders(state.Items, sellIDs, false)
		view := LimitOrderStateView{
			TemplateStateView: base,
			TradingReady:      state.Running.TradingReady,
			ActiveBuyCount:    len(buyIDs),
			ActiveSellCount:   len(sellIDs),
			BuyDepth:          limitOrderDepth(state.Items, buyIDs, true),
			SellDepth:         limitOrderDepth(state.Items, sellIDs, false),
			AssetAInPool:      decimalString(state.Running.AssetAInPool),
			AssetBInPool:      decimalString(state.Running.AssetBInPool),
			TotalDealCount:    state.Running.TotalDealCount,
		}
		view.AssetName = contract.AssetName
		view.Assets = templateViewAssets(contract.AssetName, SatoshiAssetName)
		return view, nil
	case *AutopayContract:
		status := state.Running.AutopayStatus
		if status == "" {
			status = AutopayStatusFunding
		}
		view := AutopayStateView{
			TemplateStateView: base,
			Recipient:         contract.Recipient,
			FeeAssetName:      contract.FeeAssetName,
			ScheduleMode:      contract.ScheduleMode,
			BaseAmount:        contract.BaseAmount,
			StepAmount:        contract.StepAmount,
			EndHeight:         contract.EndHeight,
			Status:            status,
			FeeBalance:        decimalString(state.Running.FeeBalance),
			GasBalance:        decimalString(state.Running.GasBalance),
			ActiveHeight:      state.Running.ActiveHeight,
			NextPayHeight:     state.Running.NextPayHeight,
			LastPayHeight:     state.Running.LastPayHeight,
			PaidBlocks:        state.Running.PaidBlockCount,
			Closed:            state.Running.Closed,
		}
		view.AssetName = contract.FeeAssetName
		view.Assets = templateViewAssets(contract.FeeAssetName)
		return view, nil
	default:
		return base, nil
	}
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

func limitOrderDepth(items []InvokeItem, ids []int, buy bool) []*DepthInfo {
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
		price := item.UnitPrice
		if price == "" {
			price = "0"
		}
		row, ok := rows[price]
		if !ok {
			row = &depthRow{price: price}
			rows[price] = row
		}
		if buy {
			row.value += item.RemainingValue
			amt := parseDecimalOrZero("0")
			if item.ExpectedAmt != nil {
				amt = item.ExpectedAmt
			}
			if item.OutAmt != nil {
				amt = amt.Sub(item.OutAmt)
			}
			row.amt = decimalString(parseDecimalOrZero(row.amt).Add(amt))
			continue
		}
		if item.RemainingAmt != nil {
			row.amt = decimalString(parseDecimalOrZero(row.amt).Add(item.RemainingAmt))
			row.value += parseDecimalOrZero(price).Mul(item.RemainingAmt).Int64()
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
	return out
}
