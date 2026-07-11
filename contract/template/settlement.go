package template

import (
	"fmt"
	"math/big"
	"sort"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

type SettlementPlan = contractframework.SettlementPlan
type SettlementDeal = contractframework.SettlementDeal
type SettlementTransfer = contractframework.SettlementTransfer

func settlementPlanHasChanges(plan *SettlementPlan) bool {
	return contractframework.SettlementPlanHasChanges(plan)
}

func cloneSettlementPlans(plans []*SettlementPlan) []*SettlementPlan {
	return contractframework.CloneSettlementPlans(plans)
}

func (r *ContractRuntime) settleLimitOrders(height int64,
	assetPrecision contractframework.AssetPrecisionResolver) (*SettlementPlan, error) {

	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	plan := &SettlementPlan{
		Contract: addr.EncodeAddress(),
		Height:   height,
	}
	changed := applyInvalidItems(r.contract, &state, plan, height)
	applyRefunds(&state, plan, height)
	if applyCloseItems(r.contract, &state, plan, height, r.base.Deployer()) {
		if err := r.saveRuntimeState(state); err != nil {
			return nil, err
		}
		return plan, nil
	}
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
		if !settlementAssetOutputNonZero(sell.AssetName, matchAmt, assetPrecision) {
			sell.Reason = InvokeReasonNoEnoughAsset
			transfer := markItemRefunded(sell)
			if transfer.AssetAmt != "" || transfer.SatValue != 0 {
				plan.Transfers = append(plan.Transfers, transfer)
			}
			addSettlementInputs(plan, sell)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, sell.ID)
			changed = true
			j++
			continue
		}

		if err := applyLimitOrderDeal(buy, sell, matchAmt, matchValue); err != nil {
			return nil, err
		}
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
		if sell.Finished() && sell.OutAmt != nil && sell.OutAmt.Sign() > 0 {
			plan.Transfers = append(plan.Transfers, SettlementTransfer{
				ItemID:    sell.ID,
				To:        sell.Address,
				AssetName: sell.AssetName,
				AssetAmt:  sell.OutAmt.String(),
				Reason:    SettlementReasonRefund,
			})
		}

		if buy.Finished() {
			addLimitOrderBuyRemainderTransfer(plan, buy)
			i++
		}
		if sell.Finished() {
			j++
		}
	}
	if refundOpenLimitOrderBuyExcess(&state, plan) {
		changed = true
	}
	if !changed && !settlementPlanHasChanges(plan) {
		return plan, nil
	}
	recomputeRunningDataPreserveGas(r.contract, &state)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return plan, nil
}

func (r *ContractRuntime) settleAMM(height int64,
	assetPrecision contractframework.AssetPrecisionResolver) (*SettlementPlan, error) {

	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	plan := &SettlementPlan{
		Contract: addr.EncodeAddress(),
		Height:   height,
	}
	changed := applyInvalidItems(r.contract, &state, plan, height)
	applyRefunds(&state, plan, height)
	changed = changed || settlementPlanHasChanges(plan)
	if applyCloseItems(r.contract, &state, plan, height, r.base.Deployer()) {
		if err := r.saveRuntimeState(state); err != nil {
			return nil, err
		}
		return plan, nil
	}
	running := state.AMMData()
	if syncAMMPoolInvariant(running) {
		changed = true
	}

	if running.TradingReady {
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

		poolAsset := running.AssetAInPool
		if poolAsset == nil {
			poolAsset = parseDecimalOrZero("0")
		}
		poolGas := decimalInt64(running.AssetBInPool)
		poolK := ammSettlementK(poolAsset, poolGas)
		for _, id := range itemIDs {
			item := &state.Items[id]
			switch item.OrderType {
			case OrderTypeBuy:
				beforeReason, beforeDone := item.Reason, item.Done
				deal, transfers, ok, err := settleAMMBuy(item, poolAsset, poolGas, poolAsset, poolK, assetPrecision)
				if err != nil {
					return nil, err
				}
				changed = changed || item.Reason != beforeReason || item.Done != beforeDone
				if !ok {
					if item.Done == ItemStatusRefunded {
						appendAMMRefundTransfer(plan, item, firstSettlementTransfer(transfers))
					}
					continue
				}
				changed = true
				poolAsset = scommon.DecimalSub(poolAsset, parseDecimalOrZero(deal.AssetAmt))
				var overflow bool
				poolGas, overflow = contractframework.AddInt64(poolGas, deal.SatValue)
				if overflow {
					return nil, fmt.Errorf("AMM pool gas overflows int64")
				}
				poolK = scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas))
				plan.Deals = append(plan.Deals, deal)
				plan.Transfers = append(plan.Transfers, transfers...)
				plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
				addSettlementInputs(plan, item)
			case OrderTypeSell:
				beforeReason, beforeDone := item.Reason, item.Done
				deal, transfer, ok, err := settleAMMSell(item, poolAsset, poolGas, poolGas, poolK)
				if err != nil {
					return nil, err
				}
				changed = changed || item.Reason != beforeReason || item.Done != beforeDone
				if !ok {
					if item.Done == ItemStatusRefunded {
						appendAMMRefundTransfer(plan, item, transfer)
					}
					continue
				}
				changed = true
				if item.InAmt != nil {
					poolAsset = scommon.DecimalAdd(poolAsset, item.InAmt)
				}
				poolGas -= deal.SatValue
				if poolGas < 0 {
					poolGas = 0
				}
				poolK = scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas))
				plan.Deals = append(plan.Deals, deal)
				plan.Transfers = append(plan.Transfers, transfer)
				plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
				addSettlementInputs(plan, item)
			}
		}
		if changed {
			running.AssetAInPool = poolAsset
			running.AssetBInPool = scommon.NewDefaultDecimal(poolGas)
			syncAMMPoolInvariant(running)
			if poolAsset.Sign() <= 0 || poolGas <= 0 {
				running.TradingReady = false
			}
		}
	}

	liquidityChanged, err := applyAMMLiquidity(&state, plan, r.base.Deployer())
	if err != nil {
		return nil, err
	}
	changed = changed || liquidityChanged
	if !running.TradingReady {
		running.TradingReady = running.ammTradingReady()
	}
	if ammPoolEmpty(*running) {
		running.TradingReady = false
	}
	if syncAMMPoolInvariant(running) {
		changed = true
	}
	if !changed {
		return plan, nil
	}
	recomputeRunningDataPreservePool(r.contract, &state, running.AssetAInPool, running.AssetBInPool)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return plan, nil
}

func applyAMMLiquidity(state *TemplateRuntimeState, plan *SettlementPlan, foundationAddress string) (bool, error) {
	if state == nil || plan == nil {
		return false, nil
	}
	running := state.AMMData()
	poolAsset := running.AssetAInPool
	if poolAsset == nil {
		poolAsset = parseDecimalOrZero("0")
	}
	poolGas := decimalInt64(running.AssetBInPool)
	totalLPT := running.TotalLPTAmt
	if totalLPT == nil {
		totalLPT = parseDecimalOrZero("0")
	}
	if totalLPT.Sign() == 0 && poolAsset.Sign() > 0 && poolGas > 0 {
		totalLPT = scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas)).Sqrt()
	}
	if running.LPBalances == nil {
		running.LPBalances = make(map[string]*scommon.Decimal)
	}
	if running.LPCosts == nil {
		running.LPCosts = make(map[string]int64)
	}
	changed := false
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal {
			continue
		}
		switch item.OrderType {
		case OrderTypeAddLiquidity:
			addAsset := item.RemainingAmt
			if addAsset == nil {
				addAsset = parseDecimalOrZero("0")
			}
			addGas := item.RemainingValue
			if addAsset.Sign() <= 0 || addGas <= 0 {
				item.Reason = InvokeReasonNoEnoughAsset
				item.Done = ItemStatusClosedDirectly
				changed = true
				continue
			}
			reserveAsset, reserveGas, leftAsset, leftGas := reserveAMMLiquidity(addAsset, addGas, poolAsset, poolGas, *running)
			minted := mintLPTAmount(reserveAsset, reserveGas, poolAsset, poolGas, totalLPT)
			if minted.Sign() <= 0 {
				item.Reason = InvokeReasonNoEnoughAsset
				item.Done = ItemStatusClosedDirectly
				changed = true
				continue
			}
			poolAsset = scommon.DecimalAdd(poolAsset, reserveAsset)
			var overflow bool
			poolGas, overflow = contractframework.AddInt64(poolGas, reserveGas)
			if overflow {
				return false, fmt.Errorf("AMM pool gas overflows int64")
			}
			totalLPT = scommon.DecimalAdd(totalLPT, minted)
			if running.LPBalances[item.Address] == nil {
				running.LPBalances[item.Address] = parseDecimalOrZero("0")
			}
			running.LPBalances[item.Address] = scommon.DecimalAdd(running.LPBalances[item.Address], minted)
			cost, err := ammLiquidityCost(reserveAsset, reserveGas)
			if err != nil {
				return false, err
			}
			running.LPCosts[item.Address], overflow = contractframework.AddInt64(running.LPCosts[item.Address], cost)
			if overflow {
				return false, fmt.Errorf("AMM LP cost overflows int64")
			}
			item.OutAmt = minted
			item.RemainingAmt = nil
			item.RemainingValue = 0
			item.Done = ItemStatusDealt
			addSettlementInputs(plan, item)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			if leftAsset.Sign() > 0 || leftGas > 0 {
				plan.Transfers = append(plan.Transfers, SettlementTransfer{
					ItemID:    item.ID,
					To:        item.Address,
					AssetName: item.AssetName,
					AssetAmt:  decimalString(leftAsset),
					SatValue:  leftGas,
					Reason:    SettlementReasonRefund,
				})
			}
			changed = true
		case OrderTypeRemoveLiquidity:
			owned := running.LPBalances[item.Address]
			if owned == nil {
				owned = parseDecimalOrZero("0")
			}
			remove := item.ExpectedAmt
			if remove == nil {
				remove = parseDecimalOrZero("0")
			}
			remove = minDecimal(remove, owned)
			remove = minDecimal(remove, totalLPT)
			if remove.Sign() <= 0 || totalLPT.Sign() <= 0 {
				item.Reason = InvokeReasonNoEnoughAsset
				item.Done = ItemStatusClosedDirectly
				changed = true
				continue
			}
			cost := running.LPCosts[item.Address]
			depositValue := proportionalInt64(cost, remove, owned)
			ratio := scommon.DecimalDiv(remove, totalLPT)
			outAsset := scommon.DecimalMul(poolAsset, ratio)
			outGas := scommon.DecimalMul(scommon.NewDefaultDecimal(poolGas), ratio).Int64()
			lpAsset, lpGas, foundationAsset, foundationGas, err := splitAMMRemoveLiquidity(outAsset, outGas, depositValue)
			if err != nil {
				return false, err
			}
			poolAsset = scommon.DecimalSub(poolAsset, outAsset)
			poolGas -= outGas
			if poolGas < 0 {
				poolGas = 0
			}
			totalLPT = scommon.DecimalSub(totalLPT, remove)
			left := scommon.DecimalSub(owned, remove)
			if left.Sign() > 0 {
				running.LPBalances[item.Address] = left
				running.LPCosts[item.Address] = cost - depositValue
				if running.LPCosts[item.Address] < 0 {
					running.LPCosts[item.Address] = 0
				}
			} else {
				delete(running.LPBalances, item.Address)
				delete(running.LPCosts, item.Address)
			}
			item.OutAmt = lpAsset
			item.OutValue = lpGas
			item.Done = ItemStatusDealt
			addSettlementInputs(plan, item)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			transfer := SettlementTransfer{
				ItemID:    item.ID,
				To:        item.Address,
				AssetName: item.AssetName,
				AssetAmt:  decimalString(item.OutAmt),
				SatValue:  item.OutValue,
				Reason:    SettlementReasonDeal,
			}
			plan.Transfers = append(plan.Transfers, transfer)
			if foundationAddress != "" && (foundationAsset.Sign() > 0 || foundationGas > 0) {
				plan.Transfers = append(plan.Transfers, SettlementTransfer{
					ItemID:    item.ID,
					To:        foundationAddress,
					AssetName: item.AssetName,
					AssetAmt:  decimalString(foundationAsset),
					SatValue:  foundationGas,
					Reason:    SettlementReasonDeal,
				})
			}
			if poolAsset.Sign() <= 0 || poolGas <= 0 || totalLPT.Sign() <= 0 {
				running.TradingReady = false
			}
			changed = true
		}
	}
	if changed {
		running.AssetAInPool = poolAsset
		running.AssetBInPool = scommon.NewDefaultDecimal(poolGas)
		running.TotalLPTAmt = totalLPT
		syncAMMPoolInvariant(running)
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
			if item.RemainingAmt != nil && item.RemainingAmt.Sign() > 0 {
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
	sellRemaining := sell.RemainingAmt
	if sellRemaining == nil {
		sellRemaining = parseDecimalOrZero("0")
	}
	if sellRemaining.Sign() <= 0 || buy.RemainingValue <= 0 {
		return parseDecimalOrZero("0"), 0, nil
	}
	matchAmt := sellRemaining
	sellValue := scommon.DecimalMul(matchAmt, price).Ceil()
	if sellValue > buy.RemainingValue {
		toBuy := scommon.NewDecimal(buy.RemainingValue, sellRemaining.Precision)
		matchAmt = scommon.DecimalDiv(toBuy, price)
		if matchAmt == nil {
			return nil, 0, fmt.Errorf("failed to calculate match amount")
		}
	}
	expected := buy.ExpectedAmt
	if expected == nil {
		expected = parseDecimalOrZero("0")
	}
	bought := buy.OutAmt
	if bought == nil {
		bought = parseDecimalOrZero("0")
	}
	if expected.Sign() > 0 {
		remainingExpected := scommon.DecimalSub(expected, bought)
		if remainingExpected.Sign() < 0 {
			remainingExpected = parseDecimalOrZero("0")
		}
		if matchAmt.Cmp(remainingExpected) > 0 {
			matchAmt = remainingExpected
		}
	}
	if matchAmt.Sign() <= 0 {
		return parseDecimalOrZero("0"), 0, nil
	}
	matchValue := scommon.DecimalMul(price, matchAmt).Ceil()
	if matchAmt.Sign() > 0 && matchValue == 0 {
		matchValue = 1
	}
	return matchAmt, matchValue, nil
}

func applyLimitOrderDeal(buy, sell *InvokeItem, matchAmt *scommon.Decimal, matchValue int64) error {
	buy.RemainingValue -= matchValue
	if buy.RemainingValue < 0 {
		buy.RemainingValue = 0
	}
	if buy.OutAmt == nil {
		buy.OutAmt = parseDecimalOrZero("0")
	}
	buy.OutAmt = scommon.DecimalAdd(buy.OutAmt, matchAmt)

	if sell.RemainingAmt == nil {
		sell.RemainingAmt = parseDecimalOrZero("0")
	}
	sell.RemainingAmt = scommon.DecimalSub(sell.RemainingAmt, matchAmt)
	var overflow bool
	sell.OutValue, overflow = contractframework.AddInt64(sell.OutValue, matchValue)
	if overflow {
		return fmt.Errorf("limit order seller output overflows int64")
	}

	if limitOrderBuyFinished(buy, sell.UnitPrice) {
		buy.OutValue, overflow = contractframework.AddInt64(buy.OutValue, buy.RemainingValue)
		if overflow {
			return fmt.Errorf("limit order buyer refund overflows int64")
		}
		buy.RemainingValue = 0
		buy.Done = ItemStatusDealt
	}
	if limitOrderSellFinished(sell) {
		sell.OutAmt = sell.RemainingAmt
		sell.RemainingAmt = nil
		sell.Done = ItemStatusDealt
	}
	return nil
}

func limitOrderBuyFinished(buy *InvokeItem, price string) bool {
	if buy.RemainingValue == 0 {
		return true
	}
	expected := buy.ExpectedAmt
	if expected == nil {
		expected = parseDecimalOrZero("0")
	}
	bought := buy.OutAmt
	if bought == nil {
		bought = parseDecimalOrZero("0")
	}
	if expected.Sign() > 0 && bought.Cmp(expected) >= 0 {
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
	remaining := sell.RemainingAmt
	if remaining == nil {
		remaining = parseDecimalOrZero("0")
	}
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

func refundOpenLimitOrderBuyExcess(state *TemplateRuntimeState, plan *SettlementPlan) bool {
	if state == nil || plan == nil {
		return false
	}
	changed := false
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal ||
			item.OrderType != OrderTypeBuy || item.OutValue <= 0 {
			continue
		}
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:   item.ID,
			To:       item.Address,
			SatValue: item.OutValue,
			Reason:   SettlementReasonRefund,
		})
		item.OutValue = 0
		changed = true
	}
	return changed
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
	plan.Inputs = contractframework.UniqueOutPoints(plan.Inputs)
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
			item.OutAmt = nil
			if transfer.AssetAmt != "" {
				item.OutAmt = parseDecimalOrZero(transfer.AssetAmt)
			}
			item.OutValue = transfer.SatValue
			item.RemainingAmt = nil
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

func applyInvalidItems(contract Contract, state *TemplateRuntimeState, plan *SettlementPlan, height int64) bool {
	changed := false
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonInvalid || item.Height > height {
			continue
		}
		addSettlementInputs(plan, item)
		plan.Transfers = append(plan.Transfers, invalidRefundTransfers(contract, item)...)
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		item.Done = ItemStatusRefunded
		changed = true
	}
	return changed
}

func invalidRefundTransfers(contract Contract, item *InvokeItem) []SettlementTransfer {
	if item == nil {
		return nil
	}
	assetA, assetB := defaultInvokePoolAssets(contract)
	out := make([]SettlementTransfer, 0, 2)
	if item.RetainedAssetA != nil && item.RetainedAssetA.Sign() > 0 && assetA != "" {
		out = append(out, SettlementTransfer{
			ItemID:    item.ID,
			To:        item.Address,
			AssetName: assetA,
			AssetAmt:  item.RetainedAssetA.String(),
			Reason:    SettlementReasonRefund,
		})
	}
	if item.RetainedAssetB != nil && item.RetainedAssetB.Sign() > 0 && assetB != "" {
		if assetB == SatoshiAssetName {
			out = append(out, SettlementTransfer{
				ItemID:   item.ID,
				To:       item.Address,
				SatValue: decimalInt64(item.RetainedAssetB),
				Reason:   SettlementReasonRefund,
			})
		} else if assetB != assetA {
			out = append(out, SettlementTransfer{
				ItemID:    item.ID,
				To:        item.Address,
				AssetName: assetB,
				AssetAmt:  item.RetainedAssetB.String(),
				Reason:    SettlementReasonRefund,
			})
		}
	}
	return out
}

func applyCloseItems(contract Contract, state *TemplateRuntimeState, plan *SettlementPlan, height int64, deployer string) bool {
	if state == nil || plan == nil || state.ClosedForContract(contract) {
		return false
	}
	for i := range state.Items {
		closeItem := &state.Items[i]
		if closeItem.Finished() || closeItem.Reason != InvokeReasonNormal ||
			closeItem.OrderType != OrderTypeClose || closeItem.Height > height {
			continue
		}
		addSettlementInputs(plan, closeItem)
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, closeItem.ID)
		if closeItem.Address != deployer {
			closeItem.Reason = InvokeReasonInvalid
			closeItem.Done = ItemStatusClosedDirectly
			return true
		}
		for j := range state.Items {
			item := &state.Items[j]
			if item.ID == closeItem.ID || item.Finished() || item.Height > height {
				continue
			}
			if item.Reason != InvokeReasonNormal {
				continue
			}
			transfer := refundTransfer(item)
			if transfer.AssetAmt != "" || transfer.SatValue != 0 {
				plan.Transfers = append(plan.Transfers, transfer)
			}
			item.Reason = InvokeReasonRefund
			item.Done = ItemStatusRefunded
			item.OutAmt = nil
			if transfer.AssetAmt != "" {
				item.OutAmt = parseDecimalOrZero(transfer.AssetAmt)
			}
			item.OutValue = transfer.SatValue
			item.RemainingAmt = nil
			item.RemainingValue = 0
			addSettlementInputs(plan, item)
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		}
		if amm, ok := contract.(*AMMContract); ok {
			appendAMMLPCloseTransfers(state, plan, closeItem, amm.AssetName)
			clearAMMClosedPool(state)
		}
		closeItem.Done = ItemStatusDealt
		markRunningClosed(contract, state)
		return true
	}
	return false
}

func markRunningClosed(contract Contract, state *TemplateRuntimeState) {
	if state == nil {
		return
	}
	switch contract.(type) {
	case *LimitOrderContract:
		state.LimitOrderData().Closed = true
	case *AMMContract:
		state.AMMData().Closed = true
	case *ExchangeContract:
		state.ExchangeData().Closed = true
	case *AutopayContract:
		state.AutopayData().Closed = true
	}
}

func clearAMMClosedPool(state *TemplateRuntimeState) {
	if state == nil {
		return
	}
	running := state.AMMData()
	running.AssetAInPool = nil
	running.AssetBInPool = nil
	running.K = nil
	running.TradingReady = false
	running.TotalLPTAmt = nil
	running.LPBalances = nil
	running.LPCosts = nil
}

func appendAMMLPCloseTransfers(state *TemplateRuntimeState, plan *SettlementPlan, item *InvokeItem, assetName string) {
	if state == nil || plan == nil {
		return
	}
	running := state.AMMData()
	if running.TotalLPTAmt == nil || running.TotalLPTAmt.Sign() <= 0 {
		return
	}
	poolAsset := running.AssetAInPool
	if poolAsset == nil {
		poolAsset = parseDecimalOrZero("0")
	}
	poolGas := decimalInt64(running.AssetBInPool)
	addresses := make([]string, 0, len(running.LPBalances))
	totalLP := parseDecimalOrZero("0")
	for address, balance := range running.LPBalances {
		if address == "" || balance == nil || balance.Sign() <= 0 {
			continue
		}
		addresses = append(addresses, address)
		totalLP = totalLP.AddAlignPrecision(balance)
	}
	sort.Strings(addresses)
	remainingAsset := poolAsset.Clone()
	remainingGas := poolGas
	for i, address := range addresses {
		balance := running.LPBalances[address]
		assetOut := remainingAsset.Clone()
		gasOut := remainingGas
		if i != len(addresses)-1 {
			ratio := scommon.DecimalDiv(balance, totalLP)
			assetOut = decimalMulAssetRatio(poolAsset, ratio)
			gasOut = proportionalInt64(poolGas, balance, totalLP)
			remainingAsset = scommon.DecimalSub(remainingAsset, assetOut)
			remainingGas -= gasOut
		}
		if assetOut.Sign() <= 0 && gasOut <= 0 {
			continue
		}
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        address,
			AssetName: assetName,
			AssetAmt:  decimalString(assetOut),
			SatValue:  gasOut,
			Reason:    SettlementReasonRefund,
		})
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
		assetAmt = decimalString(item.RemainingAmt)
	default:
		outAmt := item.OutAmt
		if outAmt == nil {
			outAmt = parseDecimalOrZero("0")
		}
		remainingAmt := item.RemainingAmt
		if remainingAmt == nil {
			remainingAmt = parseDecimalOrZero("0")
		}
		assetAmt = decimalString(scommon.DecimalAdd(outAmt, remainingAmt))
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

func markItemRefunded(item *InvokeItem) SettlementTransfer {
	transfer := refundTransfer(item)
	item.Done = ItemStatusRefunded
	item.OutAmt = nil
	if transfer.AssetAmt != "" {
		item.OutAmt = parseDecimalOrZero(transfer.AssetAmt)
	}
	item.OutValue = transfer.SatValue
	item.RemainingAmt = nil
	item.RemainingValue = 0
	return transfer
}

func appendAMMRefundTransfer(plan *SettlementPlan, item *InvokeItem, transfer SettlementTransfer) {
	if plan == nil || item == nil {
		return
	}
	if transfer.AssetAmt == "" && transfer.SatValue == 0 {
		return
	}
	plan.Transfers = append(plan.Transfers, transfer)
	plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
	addSettlementInputs(plan, item)
}

func recomputeRunningData(contract Contract, state *TemplateRuntimeState) {
	if state == nil {
		return
	}
	state.recomputeActivePools(contract)
}

func recomputeRunningDataPreserveGas(contract Contract, state *TemplateRuntimeState) {
	if state == nil {
		return
	}
	gasBalance := state.GasBalanceForContract(contract)
	recomputeRunningData(contract, state)
	switch contract.(type) {
	case *LimitOrderContract:
		state.LimitOrderData().GasBalance = gasBalance
	case *AMMContract:
		state.AMMData().GasBalance = gasBalance
	case *ExchangeContract:
		state.ExchangeData().GasBalance = gasBalance
	case *AutopayContract:
		state.AutopayData().GasBalance = gasBalance
	}
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
			if item.RemainingAmt != nil && item.RemainingAmt.Sign() > 0 {
				ids = append(ids, i)
			}
		}
	}
	return ids
}

func ammSettlementK(poolAsset *scommon.Decimal, poolGas int64) *scommon.Decimal {
	if poolAsset == nil || poolAsset.Sign() <= 0 || poolGas <= 0 {
		return nil
	}
	return scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas))
}

func settleAMMBuy(item *InvokeItem, poolAsset *scommon.Decimal, poolGas int64, availableAsset *scommon.Decimal, k *scommon.Decimal,
	assetPrecision contractframework.AssetPrecisionResolver) (SettlementDeal, []SettlementTransfer, bool, error) {

	if poolAsset.Sign() <= 0 || poolGas <= 0 || availableAsset == nil || availableAsset.Sign() <= 0 ||
		k == nil || k.Sign() <= 0 || item.RemainingValue <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, nil, false, nil
	}
	maxInputValue := item.RemainingValue
	minAsset := item.ExpectedAmt
	if minAsset == nil {
		minAsset = parseDecimalOrZero("0")
	}
	inputValue := maxInputValue
	calculatedOut, err := calculateAMMBuyOutput(inputValue, poolAsset, poolGas, k)
	if err != nil {
		return SettlementDeal{}, nil, false, err
	}
	outAsset := normalizeAMMSwapAssetOutput(item.AssetName, calculatedOut, availableAsset, assetPrecision)
	if outAsset == nil || outAsset.Sign() <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		transfer := markItemRefunded(item)
		return SettlementDeal{}, []SettlementTransfer{transfer}, false, nil
	}
	if minAsset.Sign() > 0 && outAsset.Cmp(minAsset) < 0 {
		item.Reason = InvokeReasonSlippageProtect
		transfer := markItemRefunded(item)
		return SettlementDeal{}, []SettlementTransfer{transfer}, false, nil
	}
	item.OutAmt = outAsset
	item.OutValue = inputValue
	item.RemainingValue = 0
	item.Done = ItemStatusDealt
	unitPrice := decimalString(scommon.DecimalDiv(realSwapValue(inputValue), outAsset))
	deal := SettlementDeal{
		BuyItemID: item.ID,
		AssetAmt:  decimalString(item.OutAmt),
		SatValue:  inputValue,
		UnitPrice: unitPrice,
	}
	transfers := []SettlementTransfer{{
		ItemID:    item.ID,
		To:        item.Address,
		AssetName: item.AssetName,
		AssetAmt:  decimalString(item.OutAmt),
		Reason:    SettlementReasonDeal,
	}}
	return deal, transfers, true, nil
}

func calculateAMMBuyOutput(inputValue int64, poolAsset *scommon.Decimal, poolGas int64, k *scommon.Decimal) (*scommon.Decimal, error) {
	realSwapValue := realSwapValue(inputValue)
	if realSwapValue.Sign() == 0 {
		return parseDecimalOrZero("0"), nil
	}
	newPoolGas := scommon.DecimalAdd(scommon.NewDecimal(poolGas, 3), realSwapValue)
	newPoolAsset := scommon.DecimalDiv(k, newPoolGas)
	if newPoolAsset == nil {
		return nil, fmt.Errorf("failed to calculate AMM buy output")
	}
	outAsset := scommon.DecimalSub(poolAsset, newPoolAsset)
	if outAsset.Sign() <= 0 {
		return parseDecimalOrZero("0"), nil
	}
	return outAsset, nil
}

func firstSettlementTransfer(transfers []SettlementTransfer) SettlementTransfer {
	if len(transfers) == 0 {
		return SettlementTransfer{}
	}
	return transfers[0]
}

func settleAMMSell(item *InvokeItem, poolAsset *scommon.Decimal, poolGas int64, availableGas int64, k *scommon.Decimal) (SettlementDeal, SettlementTransfer, bool, error) {
	inAsset := item.RemainingAmt
	if inAsset == nil {
		inAsset = parseDecimalOrZero("0")
	}
	if inAsset.Sign() <= 0 || poolAsset.Sign() <= 0 || poolGas <= 0 || availableGas <= 0 ||
		k == nil || k.Sign() <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return SettlementDeal{}, SettlementTransfer{}, false, nil
	}
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
	if outGas <= 0 || outGas > availableGas {
		item.Reason = InvokeReasonNoEnoughAsset
		transfer := markItemRefunded(item)
		return SettlementDeal{}, transfer, false, nil
	}
	minGas := item.ExpectedAmt
	if minGas == nil {
		minGas = parseDecimalOrZero("0")
	}
	if minGas.Sign() > 0 && outGas < minGas.Int64() {
		item.Reason = InvokeReasonSlippageProtect
		transfer := markItemRefunded(item)
		return SettlementDeal{}, transfer, false, nil
	}
	item.OutValue = outGas
	item.RemainingAmt = nil
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

func settlementAssetOutputNonZero(assetName string, amount *scommon.Decimal,
	assetPrecision contractframework.AssetPrecisionResolver) bool {

	if amount == nil || amount.Sign() <= 0 {
		return false
	}
	policy := contractframework.AssetPrecisionPolicy{
		Fallback: MaxPriceDivisibility,
		Resolve:  assetPrecision,
	}
	normalized := policy.Normalize(assetName, amount.Clone())
	return normalized != nil && normalized.Sign() > 0
}

func normalizeAMMSwapAssetOutput(assetName string, amount *scommon.Decimal, poolAsset *scommon.Decimal,
	assetPrecision contractframework.AssetPrecisionResolver) *scommon.Decimal {

	if amount == nil || amount.Sign() <= 0 || poolAsset == nil || poolAsset.Sign() <= 0 {
		return nil
	}
	policy := contractframework.AssetPrecisionPolicy{
		Fallback: MaxPriceDivisibility,
		Resolve:  assetPrecision,
	}
	normalized := policy.Normalize(assetName, amount.Clone())
	if normalized == nil || normalized.Sign() <= 0 || normalized.Cmp(poolAsset) > 0 {
		return nil
	}
	return normalized
}

func calcSwapFee(value int64) int64 {
	return SwapInvokeFee + calcSwapServiceFee(value)
}

func calcSwapServiceFee(value int64) int64 {
	if value <= 0 {
		return 0
	}
	// Split the quotient and remainder so value*ratio cannot overflow.
	return value/1000*SwapServiceFeeRatio + value%1000*SwapServiceFeeRatio/1000
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
		return parseDecimalOrZero("0")
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

func reserveAMMLiquidity(addAsset *scommon.Decimal, addGas int64, poolAsset *scommon.Decimal, poolGas int64, running AMMRunningData) (*scommon.Decimal, int64, *scommon.Decimal, int64) {
	if addAsset == nil || addAsset.Sign() <= 0 || addGas <= 0 {
		return parseDecimalOrZero("0"), 0, parseDecimalOrZero("0"), 0
	}
	if poolAsset == nil || poolAsset.Sign() <= 0 || poolGas <= 0 || !running.TradingReady {
		return addAsset, addGas, parseDecimalOrZero("0"), 0
	}
	price := ammPoolPrice(poolAsset, poolGas, running)
	if price == nil || price.Sign() <= 0 {
		return addAsset, addGas, parseDecimalOrZero("0"), 0
	}
	reserveGas := addGas
	reserveAsset := scommon.DecimalDiv(scommon.NewDecimal(addGas, MaxPriceDivisibility), price)
	if reserveAsset == nil {
		return parseDecimalOrZero("0"), 0, addAsset, addGas
	}
	reserveAsset = reserveAsset.NewPrecision(addAsset.Precision)
	if reserveAsset.Cmp(addAsset) > 0 {
		reserveAsset = addAsset
		reserveGas = scommon.DecimalMul(addAsset, price).Ceil()
		if reserveGas > addGas {
			reserveGas = addGas
		}
	}
	leftAsset := scommon.DecimalSub(addAsset, reserveAsset)
	leftGas := addGas - reserveGas
	if leftGas < 0 {
		leftGas = 0
	}
	return reserveAsset, reserveGas, leftAsset, leftGas
}

func ammPoolPrice(poolAsset *scommon.Decimal, poolGas int64, running AMMRunningData) *scommon.Decimal {
	if poolAsset != nil && poolAsset.Sign() > 0 && poolGas > 0 {
		return scommon.DecimalDiv(
			scommon.NewDecimal(poolGas, MaxPriceDivisibility),
			poolAsset.NewPrecision(MaxPriceDivisibility),
		)
	}
	requiredAsset := running.RequiredAssetA
	if requiredAsset == nil {
		requiredAsset = parseDecimalOrZero("0")
	}
	requiredAssetB := running.RequiredAssetB
	if requiredAssetB == nil {
		requiredAssetB = parseDecimalOrZero("0")
	}
	if requiredAsset.Sign() > 0 && requiredAssetB.Sign() > 0 {
		return scommon.DecimalDiv(
			requiredAssetB.NewPrecision(MaxPriceDivisibility),
			requiredAsset.NewPrecision(MaxPriceDivisibility),
		)
	}
	return parseDecimalOrZero("0")
}

func ammLiquidityCost(asset *scommon.Decimal, gas int64) (int64, error) {
	if asset == nil || asset.Sign() <= 0 || gas <= 0 {
		return gas, nil
	}
	cost, overflow := contractframework.AddInt64(gas, gas)
	if overflow {
		return 0, fmt.Errorf("AMM liquidity cost overflows int64")
	}
	return cost, nil
}

func splitAMMRemoveLiquidity(asset *scommon.Decimal, gas int64, depositValue int64) (*scommon.Decimal, int64, *scommon.Decimal, int64, error) {
	if asset == nil {
		asset = parseDecimalOrZero("0")
	}
	totalValue, overflow := contractframework.AddInt64(gas, gas)
	if overflow {
		return nil, 0, nil, 0, fmt.Errorf("AMM remove liquidity value overflows int64")
	}
	profit := totalValue - depositValue
	if profit <= 0 || totalValue <= 0 {
		return asset, gas, parseDecimalOrZero("0"), 0, nil
	}
	foundationProfitBig := new(big.Int).Mul(big.NewInt(profit), big.NewInt(100-AMMProfitShareLP))
	foundationProfitBig.Div(foundationProfitBig, big.NewInt(100))
	foundationProfit := foundationProfitBig.Int64()
	if foundationProfit <= 0 {
		return asset, gas, parseDecimalOrZero("0"), 0, nil
	}
	ratio := scommon.NewDecimal(foundationProfit, MaxPriceDivisibility).Div(scommon.NewDecimal(totalValue, MaxPriceDivisibility))
	foundationAsset := decimalMulAssetRatio(asset, ratio)
	foundationGasBig := new(big.Int).Mul(big.NewInt(gas), big.NewInt(foundationProfit))
	foundationGasBig.Div(foundationGasBig, big.NewInt(totalValue))
	foundationGas := foundationGasBig.Int64()
	if foundationGas > gas {
		foundationGas = gas
	}
	lpAsset := scommon.DecimalSub(asset, foundationAsset)
	lpGas := gas - foundationGas
	if lpGas < 0 {
		lpGas = 0
	}
	return lpAsset, lpGas, foundationAsset, foundationGas, nil
}

func proportionalInt64(value int64, part, total *scommon.Decimal) int64 {
	if value <= 0 || part == nil || total == nil || part.Sign() <= 0 || total.Sign() <= 0 {
		return 0
	}
	return scommon.DecimalMul(scommon.NewDefaultDecimal(value), scommon.DecimalDiv(part, total)).Floor()
}

func decimalMulAssetRatio(asset, ratio *scommon.Decimal) *scommon.Decimal {
	if asset == nil || ratio == nil {
		return parseDecimalOrZero("0")
	}
	return scommon.DecimalMul(asset, ratio)
}

func ammPoolEmpty(r AMMRunningData) bool {
	assetA := r.AssetAInPool
	if assetA == nil {
		assetA = parseDecimalOrZero("0")
	}
	assetB := r.AssetBInPool
	if assetB == nil {
		assetB = parseDecimalOrZero("0")
	}
	return assetA.Sign() <= 0 || assetB.Sign() <= 0
}

func minDecimal(a, b *scommon.Decimal) *scommon.Decimal {
	if a == nil {
		return parseDecimalOrZero("0")
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
	scaled := new(big.Int).Mul(big.NewInt(value), big.NewInt(1000-SwapServiceFeeRatio))
	scaled.Mul(scaled, big.NewInt(1000))
	return (&scommon.Decimal{Precision: 3, Value: scaled}).Div(scommon.NewDecimal(1000, 3))
}

func realSwapAmt(amt *scommon.Decimal) *scommon.Decimal {
	if amt == nil {
		return nil
	}
	return scommon.DecimalMulV2(amt, scommon.NewDecimal(1000-SwapServiceFeeRatio, amt.Precision+3)).
		Div(scommon.NewDecimal(1000, amt.Precision+3))
}

func recomputeRunningDataPreservePool(contract Contract, state *TemplateRuntimeState, assetA *scommon.Decimal, assetB *scommon.Decimal) {
	running := state.AMMData()
	requiredAssetA := running.RequiredAssetA
	requiredAssetB := running.RequiredAssetB
	requiredK := running.RequiredK
	k := running.K
	ready := running.TradingReady
	gasBalance := running.GasBalance
	totalLPT := running.TotalLPTAmt
	lpBalances := cloneLPBalances(running.LPBalances)
	lpCosts := cloneLPCosts(running.LPCosts)
	recomputeRunningData(contract, state)
	running = state.AMMData()
	running.AssetAInPool = assetA
	running.AssetBInPool = assetB
	running.RequiredAssetA = requiredAssetA
	running.RequiredAssetB = requiredAssetB
	running.RequiredK = requiredK
	running.K = k
	running.TradingReady = ready
	running.GasBalance = gasBalance
	running.TotalLPTAmt = totalLPT
	running.LPBalances = lpBalances
	running.LPCosts = lpCosts
}

func cloneLPBalances(in map[string]*scommon.Decimal) map[string]*scommon.Decimal {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*scommon.Decimal, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = parseDecimalOrZero("0")
			continue
		}
		out[k] = v.Clone()
	}
	return out
}

func cloneLPCosts(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
