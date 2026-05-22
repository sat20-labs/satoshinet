package template

import (
	"fmt"
	"sort"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
)

type SettlementPlan struct {
	Contract  string               `json:"contract"`
	Height    int64                `json:"height"`
	Deals     []SettlementDeal     `json:"deals,omitempty"`
	Transfers []SettlementTransfer `json:"transfers,omitempty"`
	ItemIDs   []int64              `json:"itemIds,omitempty"`
	Inputs    []OutPoint           `json:"inputs,omitempty"`
}

type SettlementDeal struct {
	BuyItemID  int64  `json:"buyItemId"`
	SellItemID int64  `json:"sellItemId"`
	AssetAmt   string `json:"assetAmt"`
	SatValue   int64  `json:"satValue"`
	UnitPrice  string `json:"unitPrice"`
}

type SettlementTransfer struct {
	ItemID    int64  `json:"itemId"`
	To        string `json:"to,omitempty"`
	AssetName string `json:"assetName,omitempty"`
	AssetAmt  string `json:"assetAmt,omitempty"`
	SatValue  int64  `json:"satValue,omitempty"`
	Reason    string `json:"reason"`
}

func settlementPlanHasChanges(plan *SettlementPlan) bool {
	return plan != nil && (len(plan.Deals) != 0 || len(plan.Transfers) != 0 || len(plan.ItemIDs) != 0)
}

func cloneSettlementPlans(plans []*SettlementPlan) []*SettlementPlan {
	out := make([]*SettlementPlan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, cloneSettlementPlan(plan))
	}
	return out
}

func (r *ContractRuntime) settleLimitOrders(height int64) (*SettlementPlan, error) {
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	plan := &SettlementPlan{
		Contract: addr.EncodeAddress(),
		Height:   height,
	}
	applyRefunds(&state, plan, height)
	buyIDs, sellIDs := activeLimitOrderIDs(state.Items, height)
	sortLimitOrders(state.Items, buyIDs, true)
	sortLimitOrders(state.Items, sellIDs, false)

	i, j := 0, 0
	for i < len(buyIDs) && j < len(sellIDs) {
		buy := &state.Items[buyIDs[i]]
		sell := &state.Items[sellIDs[j]]
		buyPrice := parseDecimalOrZero(buy.UnitPrice)
		sellPrice := parseDecimalOrZero(sell.UnitPrice)
		if buyPrice.Cmp(sellPrice) < 0 {
			break
		}
		matchAmt, matchValue, err := matchLimitOrderAmount(buy, sell, sellPrice)
		if err != nil {
			return nil, err
		}
		if matchAmt.Sign() == 0 || matchValue == 0 {
			break
		}

		applyLimitOrderDeal(buy, sell, matchAmt, matchValue)
		plan.Deals = append(plan.Deals, SettlementDeal{
			BuyItemID:  buy.ID,
			SellItemID: sell.ID,
			AssetAmt:   matchAmt.String(),
			SatValue:   matchValue,
			UnitPrice:  sell.UnitPrice,
		})
		addSettlementInputs(plan, buy)
		addSettlementInputs(plan, sell)
		addLimitOrderDealTransfers(plan, buy, sell, matchAmt.String(), matchValue)

		if buy.Finished() {
			addLimitOrderBuyRemainderTransfer(plan, buy)
			i++
		}
		if sell.Finished() {
			j++
		}
	}
	if len(plan.Deals) == 0 && len(plan.Transfers) == 0 {
		return plan, nil
	}
	recomputeRunningDataPreserveGas(&state)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return plan, nil
}

func (r *ContractRuntime) settleAMM(height int64) (*SettlementPlan, error) {
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	plan := &SettlementPlan{
		Contract: addr.EncodeAddress(),
		Height:   height,
	}
	applyRefunds(&state, plan, height)
	itemIDs := activeAMMItemIDs(state.Items, height)
	sort.SliceStable(itemIDs, func(i, j int) bool {
		a := state.Items[itemIDs[i]]
		b := state.Items[itemIDs[j]]
		if a.Height != b.Height {
			return a.Height < b.Height
		}
		if a.OrderTime != b.OrderTime {
			return a.OrderTime < b.OrderTime
		}
		return a.ID < b.ID
	})

	poolAsset := parseDecimalOrZero(state.Running.AssetAmtInPool)
	if !state.Running.TradingReady {
		return plan, nil
	}
	changed, err := applyAMMLiquidity(&state, plan)
	if err != nil {
		return nil, err
	}
	poolAsset = parseDecimalOrZero(state.Running.AssetAmtInPool)
	poolGas := state.Running.SatValueInPool
	for _, id := range itemIDs {
		item := &state.Items[id]
		switch item.OrderType {
		case OrderTypeBuy:
			beforeReason, beforeDone := item.Reason, item.Done
			deal, transfer, ok, err := settleAMMBuy(item, poolAsset, poolGas)
			if err != nil {
				return nil, err
			}
			changed = changed || item.Reason != beforeReason || item.Done != beforeDone
			if !ok {
				continue
			}
			changed = true
			poolAsset = scommon.DecimalSub(poolAsset, parseDecimalOrZero(deal.AssetAmt))
			poolGas += deal.SatValue
			plan.Deals = append(plan.Deals, deal)
			plan.Transfers = append(plan.Transfers, transfer)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			addSettlementInputs(plan, item)
		case OrderTypeSell:
			beforeReason, beforeDone := item.Reason, item.Done
			deal, transfer, ok, err := settleAMMSell(item, poolAsset, poolGas)
			if err != nil {
				return nil, err
			}
			changed = changed || item.Reason != beforeReason || item.Done != beforeDone
			if !ok {
				continue
			}
			changed = true
			poolAsset = scommon.DecimalAdd(poolAsset, parseDecimalOrZero(item.InAmt))
			poolGas -= deal.SatValue
			if poolGas < 0 {
				poolGas = 0
			}
			plan.Deals = append(plan.Deals, deal)
			plan.Transfers = append(plan.Transfers, transfer)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			addSettlementInputs(plan, item)
		}
	}
	if !changed {
		return plan, nil
	}
	state.Running.AssetAmtInPool = decimalString(poolAsset)
	state.Running.SatValueInPool = poolGas
	recomputeRunningDataPreservePool(&state, state.Running.AssetAmtInPool, state.Running.SatValueInPool)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return plan, nil
}

func applyAMMLiquidity(state *TemplateRuntimeState, plan *SettlementPlan) (bool, error) {
	if state == nil || plan == nil {
		return false, nil
	}
	poolAsset := parseDecimalOrZero(state.Running.AssetAmtInPool)
	poolGas := state.Running.SatValueInPool
	totalLPT := parseDecimalOrZero(state.Running.TotalLPTAmt)
	if totalLPT.Sign() == 0 && poolAsset.Sign() > 0 && poolGas > 0 {
		totalLPT = scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas)).Sqrt()
	}
	if state.Running.LPBalances == nil {
		state.Running.LPBalances = make(map[string]string)
	}
	changed := false
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal {
			continue
		}
		switch item.OrderType {
		case OrderTypeAddLiquidity:
			addAsset := parseDecimalOrZero(item.InAmt)
			addGas := item.InValue
			if addAsset.Sign() <= 0 || addGas <= 0 {
				item.Reason = InvokeReasonNoEnoughAsset
				item.Done = ItemStatusClosedDirectly
				changed = true
				continue
			}
			minted := mintLPTAmount(addAsset, addGas, poolAsset, poolGas, totalLPT)
			if minted.Sign() <= 0 {
				item.Reason = InvokeReasonNoEnoughAsset
				item.Done = ItemStatusClosedDirectly
				changed = true
				continue
			}
			poolAsset = scommon.DecimalAdd(poolAsset, addAsset)
			poolGas += addGas
			totalLPT = scommon.DecimalAdd(totalLPT, minted)
			state.Running.LPBalances[item.Address] = decimalStringAdd(state.Running.LPBalances[item.Address], minted.String())
			item.OutAmt = minted.String()
			item.RemainingAmt = ""
			item.RemainingValue = 0
			item.Done = ItemStatusDealt
			addSettlementInputs(plan, item)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			changed = true
		case OrderTypeRemoveLiquidity:
			owned := parseDecimalOrZero(state.Running.LPBalances[item.Address])
			remove := parseDecimalOrZero(item.ExpectedAmt)
			remove = minDecimal(remove, owned)
			remove = minDecimal(remove, totalLPT)
			if remove.Sign() <= 0 || totalLPT.Sign() <= 0 {
				item.Reason = InvokeReasonNoEnoughAsset
				item.Done = ItemStatusClosedDirectly
				changed = true
				continue
			}
			ratio := scommon.DecimalDiv(remove, totalLPT)
			outAsset := scommon.DecimalMul(poolAsset, ratio)
			outGas := scommon.DecimalMul(scommon.NewDefaultDecimal(poolGas), ratio).Int64()
			poolAsset = scommon.DecimalSub(poolAsset, outAsset)
			poolGas -= outGas
			if poolGas < 0 {
				poolGas = 0
			}
			totalLPT = scommon.DecimalSub(totalLPT, remove)
			left := scommon.DecimalSub(owned, remove)
			if left.Sign() > 0 {
				state.Running.LPBalances[item.Address] = left.String()
			} else {
				delete(state.Running.LPBalances, item.Address)
			}
			item.OutAmt = outAsset.String()
			item.OutValue = outGas
			item.Done = ItemStatusDealt
			addSettlementInputs(plan, item)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			transfer := SettlementTransfer{
				ItemID:    item.ID,
				To:        item.Address,
				AssetName: item.AssetName,
				AssetAmt:  item.OutAmt,
				SatValue:  item.OutValue,
				Reason:    SettlementReasonDeal,
			}
			plan.Transfers = append(plan.Transfers, transfer)
			changed = true
		}
	}
	if changed {
		state.Running.AssetAmtInPool = decimalString(poolAsset)
		state.Running.SatValueInPool = poolGas
		state.Running.TotalLPTAmt = decimalString(totalLPT)
	}
	return changed, nil
}

func activeLimitOrderIDs(items []InvokeItem, height int64) ([]int, []int) {
	buyIDs := make([]int, 0)
	sellIDs := make([]int, 0)
	for i := range items {
		item := &items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal || item.Height > height {
			continue
		}
		switch item.OrderType {
		case OrderTypeBuy:
			if item.RemainingValue > 0 {
				buyIDs = append(buyIDs, i)
			}
		case OrderTypeSell:
			if parseDecimalOrZero(item.RemainingAmt).Sign() > 0 {
				sellIDs = append(sellIDs, i)
			}
		}
	}
	return buyIDs, sellIDs
}

func sortLimitOrders(items []InvokeItem, ids []int, buy bool) {
	sort.SliceStable(ids, func(i, j int) bool {
		a := items[ids[i]]
		b := items[ids[j]]
		priceCmp := parseDecimalOrZero(a.UnitPrice).Cmp(parseDecimalOrZero(b.UnitPrice))
		if priceCmp != 0 {
			if buy {
				return priceCmp > 0
			}
			return priceCmp < 0
		}
		if a.Height != b.Height {
			return a.Height < b.Height
		}
		if a.OrderTime != b.OrderTime {
			return a.OrderTime < b.OrderTime
		}
		return a.ID < b.ID
	})
}

func matchLimitOrderAmount(buy, sell *InvokeItem, price *scommon.Decimal) (*scommon.Decimal, int64, error) {
	if price == nil || price.Sign() <= 0 {
		return nil, 0, fmt.Errorf("invalid match price")
	}
	sellRemaining := parseDecimalOrZero(sell.RemainingAmt)
	if sellRemaining.Sign() <= 0 || buy.RemainingValue <= 0 {
		return scommon.NewDefaultDecimal(0), 0, nil
	}
	sellValue := scommon.DecimalMul(sellRemaining, price).Ceil()
	if sellValue <= buy.RemainingValue {
		return sellRemaining, sellValue, nil
	}

	toBuy := scommon.NewDecimal(buy.RemainingValue, sellRemaining.Precision)
	matchAmt := scommon.DecimalDiv(toBuy, price)
	if matchAmt == nil {
		return nil, 0, fmt.Errorf("failed to calculate match amount")
	}
	expected := parseDecimalOrZero(buy.ExpectedAmt)
	bought := parseDecimalOrZero(buy.OutAmt)
	if expected.Sign() > 0 {
		remainingExpected := scommon.DecimalSub(expected, bought)
		if remainingExpected.Sign() < 0 {
			remainingExpected = scommon.NewDefaultDecimal(0)
		}
		if matchAmt.Cmp(remainingExpected) > 0 {
			matchAmt = remainingExpected
		}
	}
	matchValue := scommon.DecimalMul(price, matchAmt).Ceil()
	if matchAmt.Sign() > 0 && matchValue == 0 {
		matchValue = 1
	}
	return matchAmt, matchValue, nil
}

func applyLimitOrderDeal(buy, sell *InvokeItem, matchAmt *scommon.Decimal, matchValue int64) {
	buy.RemainingValue -= matchValue
	if buy.RemainingValue < 0 {
		buy.RemainingValue = 0
	}
	buy.OutAmt = decimalStringAdd(buy.OutAmt, matchAmt.String())

	sell.RemainingAmt = decimalStringSub(sell.RemainingAmt, matchAmt.String())
	sell.OutValue += matchValue

	if limitOrderBuyFinished(buy, sell.UnitPrice) {
		buy.OutValue = buy.RemainingValue
		buy.RemainingValue = 0
		buy.Done = ItemStatusDealt
	}
	if limitOrderSellFinished(sell) {
		sell.OutAmt = sell.RemainingAmt
		sell.RemainingAmt = ""
		sell.Done = ItemStatusDealt
	}
}

func limitOrderBuyFinished(buy *InvokeItem, price string) bool {
	if buy.RemainingValue == 0 {
		return true
	}
	expected := parseDecimalOrZero(buy.ExpectedAmt)
	if expected.Sign() > 0 && parseDecimalOrZero(buy.OutAmt).Cmp(expected) >= 0 {
		return true
	}
	unitPrice := parseDecimalOrZero(price)
	if unitPrice.Sign() <= 0 {
		return true
	}
	toBuy := scommon.NewDecimal(buy.RemainingValue, MaxPriceDivisibility)
	nextAmt := scommon.DecimalDiv(toBuy, unitPrice)
	return nextAmt == nil || nextAmt.Sign() == 0
}

func limitOrderSellFinished(sell *InvokeItem) bool {
	remaining := parseDecimalOrZero(sell.RemainingAmt)
	if remaining.Sign() == 0 {
		return true
	}
	unitPrice := parseDecimalOrZero(sell.UnitPrice)
	if unitPrice.Sign() <= 0 {
		return true
	}
	return scommon.DecimalMul(remaining, unitPrice).Int64() == 0
}

func addLimitOrderDealTransfers(plan *SettlementPlan, buy, sell *InvokeItem, assetAmt string, satValue int64) {
	if plan == nil || buy == nil || sell == nil {
		return
	}
	plan.ItemIDs = appendPlanItemID(plan.ItemIDs, buy.ID)
	plan.ItemIDs = appendPlanItemID(plan.ItemIDs, sell.ID)
	if assetAmt != "" {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    buy.ID,
			To:        buy.Address,
			AssetName: buy.AssetName,
			AssetAmt:  assetAmt,
			Reason:    SettlementReasonDeal,
		})
	}
	if satValue != 0 {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:   sell.ID,
			To:       sell.Address,
			SatValue: satValue,
			Reason:   SettlementReasonDeal,
		})
	}
}

func addLimitOrderBuyRemainderTransfer(plan *SettlementPlan, item *InvokeItem) {
	if plan == nil || item == nil || item.OutValue == 0 {
		return
	}
	plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
	plan.Transfers = append(plan.Transfers, SettlementTransfer{
		ItemID:   item.ID,
		To:       item.Address,
		SatValue: item.OutValue,
		Reason:   SettlementReasonDeal,
	})
}

func appendPlanItemID(ids []int64, id int64) []int64 {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func addSettlementInputs(plan *SettlementPlan, item *InvokeItem) {
	if plan == nil || item == nil || item.InUtxos == "" {
		return
	}
	for _, raw := range strings.Split(item.InUtxos, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		outpoint, err := ParseOutPoint(raw)
		if err != nil {
			continue
		}
		plan.Inputs = append(plan.Inputs, WireOutPointToTemplate(outpoint))
	}
	plan.Inputs = uniqueOutPoints(plan.Inputs)
}

func applyRefunds(state *TemplateRuntimeState, plan *SettlementPlan, height int64) {
	for i := range state.Items {
		refund := &state.Items[i]
		if refund.Finished() || refund.Reason != InvokeReasonNormal || refund.OrderType != OrderTypeRefund || refund.Height > height {
			continue
		}
		for j := range state.Items {
			item := &state.Items[j]
			if item.ID == refund.ID || item.Finished() || item.Reason != InvokeReasonNormal || item.Address != refund.Address {
				continue
			}
			if !refundMatchesItem(refund, item.ID) {
				continue
			}
			transfer := refundTransfer(item)
			if transfer.AssetAmt == "" && transfer.SatValue == 0 {
				continue
			}
			item.Reason = InvokeReasonRefund
			item.Done = ItemStatusRefunded
			item.OutAmt = transfer.AssetAmt
			item.OutValue = transfer.SatValue
			item.RemainingAmt = ""
			item.RemainingValue = 0
			addSettlementInputs(plan, item)
			plan.Transfers = append(plan.Transfers, transfer)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		}
		refund.Done = ItemStatusRefunded
		refund.Reason = InvokeReasonRefund
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, refund.ID)
	}
}

func refundMatchesItem(refund *InvokeItem, itemID int64) bool {
	if refund == nil || len(refund.RefundItemIDs) == 0 {
		return true
	}
	for _, targetID := range refund.RefundItemIDs {
		if targetID == itemID {
			return true
		}
	}
	return false
}

func refundTransfer(item *InvokeItem) SettlementTransfer {
	assetAmt := ""
	satValue := int64(0)
	switch item.OrderType {
	case OrderTypeBuy:
		satValue = item.OutValue + item.RemainingValue
	case OrderTypeSell:
		assetAmt = item.RemainingAmt
	default:
		assetAmt = decimalStringAdd(item.OutAmt, item.RemainingAmt)
		satValue = item.OutValue + item.RemainingValue
	}
	return SettlementTransfer{
		ItemID:    item.ID,
		To:        item.Address,
		AssetName: item.AssetName,
		AssetAmt:  assetAmt,
		SatValue:  satValue,
		Reason:    SettlementReasonRefund,
	}
}

func recomputeRunningData(state *TemplateRuntimeState) {
	var running RunningData
	for i := range state.Items {
		running.Apply(&state.Items[i])
	}
	state.Running = running
}

func recomputeRunningDataPreserveGas(state *TemplateRuntimeState) {
	gasBalance := state.Running.GasBalance
	recomputeRunningData(state)
	state.Running.GasBalance = gasBalance
}

func activeAMMItemIDs(items []InvokeItem, height int64) []int {
	ids := make([]int, 0)
	for i := range items {
		item := &items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal || item.Height > height {
			continue
		}
		switch item.OrderType {
		case OrderTypeBuy:
			if item.RemainingValue > 0 {
				ids = append(ids, i)
			}
		case OrderTypeSell:
			if parseDecimalOrZero(item.RemainingAmt).Sign() > 0 {
				ids = append(ids, i)
			}
		}
	}
	return ids
}

func settleAMMBuy(item *InvokeItem, poolAsset *scommon.Decimal, poolGas int64) (SettlementDeal, SettlementTransfer, bool, error) {
	if poolAsset.Sign() <= 0 || poolGas <= 0 || item.RemainingValue <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	k := scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas))
	inputValue := item.RemainingValue
	realSwapValue := realSwapValue(inputValue)
	if realSwapValue.Sign() == 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	newPoolGas := scommon.DecimalAdd(scommon.NewDecimal(poolGas, 3), realSwapValue)
	newPoolAsset := scommon.DecimalDiv(k, newPoolGas)
	if newPoolAsset == nil {
		return SettlementDeal{}, SettlementTransfer{}, false, fmt.Errorf("failed to calculate AMM buy output")
	}
	outAsset := scommon.DecimalSub(poolAsset, newPoolAsset)
	if outAsset.Sign() <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	minAsset := parseDecimalOrZero(item.ExpectedAmt)
	if minAsset.Sign() > 0 && outAsset.Cmp(minAsset) < 0 {
		item.Reason = InvokeReasonSlippageProtect
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	item.OutAmt = outAsset.String()
	item.RemainingValue = 0
	item.Done = ItemStatusDealt
	unitPrice := decimalString(scommon.DecimalDiv(realSwapValue, outAsset))
	deal := SettlementDeal{
		BuyItemID: item.ID,
		AssetAmt:  item.OutAmt,
		SatValue:  inputValue,
		UnitPrice: unitPrice,
	}
	transfer := SettlementTransfer{
		ItemID:    item.ID,
		To:        item.Address,
		AssetName: item.AssetName,
		AssetAmt:  item.OutAmt,
		Reason:    SettlementReasonDeal,
	}
	return deal, transfer, true, nil
}

func settleAMMSell(item *InvokeItem, poolAsset *scommon.Decimal, poolGas int64) (SettlementDeal, SettlementTransfer, bool, error) {
	inAsset := parseDecimalOrZero(item.RemainingAmt)
	if inAsset.Sign() <= 0 || poolAsset.Sign() <= 0 || poolGas <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	k := scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas))
	realSwapAmt := realSwapAmt(inAsset)
	if realSwapAmt.Sign() == 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	newPoolAsset := scommon.DecimalAdd(poolAsset, realSwapAmt)
	newPoolGasDecimal := scommon.DecimalDiv(k, newPoolAsset)
	if newPoolGasDecimal == nil {
		return SettlementDeal{}, SettlementTransfer{}, false, fmt.Errorf("failed to calculate AMM sell output")
	}
	outGas := poolGas - newPoolGasDecimal.Ceil()
	if outGas <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	minGas := parseDecimalOrZero(item.ExpectedAmt)
	if minGas.Sign() > 0 && outGas < minGas.Int64() {
		item.Reason = InvokeReasonSlippageProtect
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
	item.OutValue = outGas
	item.RemainingAmt = ""
	item.Done = ItemStatusDealt
	unitPrice := decimalString(scommon.DecimalDiv(scommon.NewDefaultDecimal(outGas), realSwapAmt))
	deal := SettlementDeal{
		SellItemID: item.ID,
		AssetAmt:   inAsset.String(),
		SatValue:   outGas,
		UnitPrice:  unitPrice,
	}
	transfer := SettlementTransfer{
		ItemID:   item.ID,
		To:       item.Address,
		SatValue: outGas,
		Reason:   SettlementReasonDeal,
	}
	return deal, transfer, true, nil
}

func calcSwapFee(value int64) int64 {
	return SwapInvokeFee + calcSwapServiceFee(value)
}

func calcSwapServiceFee(value int64) int64 {
	return value * SwapServiceFeeRatio / 1000
}

func calcLimitOrderTradingValue(amt, unitPrice string) int64 {
	assetAmt := parseDecimalOrZero(amt)
	price := parseDecimalOrZero(unitPrice)
	if assetAmt.Sign() == 0 || price.Sign() == 0 {
		return 0
	}
	return scommon.DecimalMul(price, assetAmt).Ceil()
}

func mintLPTAmount(addAsset *scommon.Decimal, addGas int64, poolAsset *scommon.Decimal, poolGas int64, totalLPT *scommon.Decimal) *scommon.Decimal {
	if addAsset == nil || addAsset.Sign() <= 0 || addGas <= 0 {
		return scommon.NewDefaultDecimal(0)
	}
	if totalLPT == nil || totalLPT.Sign() == 0 || poolAsset == nil || poolAsset.Sign() == 0 || poolGas <= 0 {
		return scommon.DecimalMul(addAsset, scommon.NewDefaultDecimal(addGas)).Sqrt()
	}
	byAsset := scommon.DecimalMul(addAsset, totalLPT)
	byAsset = scommon.DecimalDiv(byAsset, poolAsset)
	byGas := scommon.DecimalMul(scommon.NewDefaultDecimal(addGas), totalLPT)
	byGas = scommon.DecimalDiv(byGas, scommon.NewDefaultDecimal(poolGas))
	return minDecimal(byAsset, byGas)
}

func minDecimal(a, b *scommon.Decimal) *scommon.Decimal {
	if a == nil {
		return scommon.NewDefaultDecimal(0)
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func realSwapValue(value int64) *scommon.Decimal {
	return scommon.NewDecimal(value*(1000-SwapServiceFeeRatio), 3).Div(
		scommon.NewDecimal(1000, 3))
}

func realSwapAmt(amt *scommon.Decimal) *scommon.Decimal {
	if amt == nil {
		return nil
	}
	return scommon.DecimalMulV2(amt, scommon.NewDecimal(1000-SwapServiceFeeRatio, amt.Precision+3)).
		Div(scommon.NewDecimal(1000, amt.Precision+3))
}

func recomputeRunningDataPreservePool(state *TemplateRuntimeState, assetAmt string, gasValue int64) {
	requiredAsset := state.Running.RequiredAsset
	requiredSat := state.Running.RequiredSat
	k := state.Running.K
	ready := state.Running.TradingReady
	gasBalance := state.Running.GasBalance
	totalLPT := state.Running.TotalLPTAmt
	lpBalances := cloneLPBalances(state.Running.LPBalances)
	recomputeRunningData(state)
	state.Running.AssetAmtInPool = assetAmt
	state.Running.SatValueInPool = gasValue
	state.Running.RequiredAsset = requiredAsset
	state.Running.RequiredSat = requiredSat
	state.Running.K = k
	state.Running.TradingReady = ready
	state.Running.GasBalance = gasBalance
	state.Running.TotalLPTAmt = totalLPT
	state.Running.LPBalances = lpBalances
}

func cloneLPBalances(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
