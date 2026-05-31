package template

import (
	"fmt"
	"strconv"

	scommon "github.com/sat20-labs/indexer/common"
)

func (r *ContractRuntime) settleExchange(height int64, gasConfig GasConfig) (*SettlementPlan, error) {
	contract, ok := r.contract.(*ExchangeContract)
	if !ok {
		return nil, fmt.Errorf("template %s is not exchange", r.TemplateName())
	}
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	gasAssetName := gasConfig.normalized().GasAssetName
	plan := &SettlementPlan{
		Contract: addr.EncodeAddress(),
		Height:   height,
	}
	changed := false
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Height > height {
			continue
		}
		switch item.OrderType {
		case OrderTypeFund:
			if err := applyExchangeGasFee(&state, contract, item, gasAssetName); err != nil {
				return nil, err
			}
			item.Done = ItemStatusDealt
			plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
			addSettlementInputs(plan, item)
			changed = true
		case OrderTypeExchange:
			if err := settleExchangeItem(&state, contract, item, plan, height, r.base.Deployer(), gasAssetName); err != nil {
				return nil, err
			}
			changed = true
		case OrderTypeClose:
			if err := settleExchangeClose(&state, contract, item, plan, r.base.Deployer(), gasAssetName); err != nil {
				return nil, err
			}
			changed = true
		}
	}
	if !changed {
		return plan, nil
	}
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return plan, nil
}

func settleExchangeItem(state *TemplateRuntimeState, contract *ExchangeContract, item *InvokeItem,
	plan *SettlementPlan, height int64, deployer, gasAssetName string) error {

	addSettlementInputs(plan, item)
	plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
	if state.Running.Closed {
		markExchangeRefunded(state, item, plan, contract, gasAssetName)
		return nil
	}
	if err := applyExchangeGasFee(state, contract, item, gasAssetName); err != nil {
		return err
	}
	inputB := item.RemainingAmt
	if inputB == nil {
		inputB = parseDecimalOrZero("0")
	}
	if item.ServiceFee > 0 && gasAssetName == contract.AssetBName {
		inputB = scommon.DecimalSub(inputB, scommon.NewDefaultDecimal(item.ServiceFee))
		if inputB.Sign() < 0 {
			inputB = parseDecimalOrZero("0")
		}
	}
	if inputB.Sign() <= 0 {
		item.Reason = InvokeReasonNoEnoughAsset
		item.Done = ItemStatusClosedDirectly
		return nil
	}
	availableA := state.Running.AssetAInPool
	if availableA == nil {
		availableA = parseDecimalOrZero("0")
	}
	if availableA.Sign() <= 0 {
		markExchangeRefunded(state, item, plan, contract, gasAssetName)
		return nil
	}
	totalDealA := state.Running.TotalDealAssetA
	if totalDealA == nil {
		totalDealA = parseDecimalOrZero("0")
	}
	quote := exchangeQuote(contract, height, totalDealA.String(), availableA, inputB)
	if quote.OutA.Sign() <= 0 || quote.SpentB.Sign() <= 0 {
		markExchangeRefunded(state, item, plan, contract, gasAssetName)
		return nil
	}
	minOut := item.ExpectedAmt
	if minOut == nil {
		minOut = parseDecimalOrZero("0")
	}
	if minOut.Sign() > 0 && quote.OutA.Cmp(minOut) < 0 {
		markExchangeRefunded(state, item, plan, contract, gasAssetName)
		return nil
	}
	refundB := scommon.DecimalSub(inputB, quote.SpentB)
	state.Running.AssetAInPool = scommon.DecimalSub(availableA, quote.OutA)
	if state.Running.TotalDealAssetA == nil {
		state.Running.TotalDealAssetA = parseDecimalOrZero("0")
	}
	if state.Running.TotalDealAssetB == nil {
		state.Running.TotalDealAssetB = parseDecimalOrZero("0")
	}
	state.Running.TotalDealAssetA = scommon.DecimalAdd(state.Running.TotalDealAssetA, quote.OutA)
	state.Running.TotalDealAssetB = scommon.DecimalAdd(state.Running.TotalDealAssetB, quote.SpentB)
	state.Running.TotalDealCount++
	item.OutAmt = quote.OutA
	item.RemainingAmt = nil
	item.Done = ItemStatusDealt
	item.UnitPrice = decimalString(quote.UnitPrice)
	plan.Deals = append(plan.Deals, SettlementDeal{
		BuyItemID: item.ID,
		AssetAmt:  decimalString(item.OutAmt),
		UnitPrice: item.UnitPrice,
	})
	if quote.OutA.Sign() > 0 {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        item.Address,
			AssetName: contract.AssetAName,
			AssetAmt:  decimalString(item.OutAmt),
			Reason:    SettlementReasonDeal,
		})
	}
	if quote.SpentB.Sign() > 0 && deployer != "" {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        deployer,
			AssetName: contract.AssetBName,
			AssetAmt:  decimalString(quote.SpentB),
			Reason:    SettlementReasonDeal,
		})
	}
	if refundB.Sign() > 0 {
		if state.Running.TotalRefundAssetB == nil {
			state.Running.TotalRefundAssetB = parseDecimalOrZero("0")
		}
		state.Running.TotalRefundAssetB = scommon.DecimalAdd(state.Running.TotalRefundAssetB, refundB)
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        item.Address,
			AssetName: contract.AssetBName,
			AssetAmt:  decimalString(refundB),
			Reason:    SettlementReasonRefund,
		})
	}
	return nil
}

type exchangeQuoteResult struct {
	OutA      *scommon.Decimal
	SpentB    *scommon.Decimal
	UnitPrice *scommon.Decimal
}

func exchangeQuote(contract *ExchangeContract, height int64, soldA string, availableA, inputB *scommon.Decimal) exchangeQuoteResult {
	outA := parseDecimalOrZero("0")
	spentB := parseDecimalOrZero("0")
	currentSold := parseDecimalOrZero(soldA)
	remainingA := availableA
	remainingB := inputB
	for remainingA.Sign() > 0 && remainingB.Sign() > 0 {
		price := exchangePriceAt(contract, height, currentSold.String())
		if price.Sign() <= 0 {
			break
		}
		tierA := exchangeTierAvailableA(contract, currentSold)
		if tierA == nil || tierA.Cmp(remainingA) > 0 {
			tierA = remainingA
		}
		if tierA.Sign() <= 0 {
			break
		}
		fillA := scommon.DecimalDiv(remainingB, price)
		if fillA == nil || fillA.Sign() <= 0 {
			break
		}
		if fillA.Cmp(tierA) > 0 {
			fillA = tierA
		}
		costB := scommon.DecimalMul(fillA, price)
		if costB.Cmp(remainingB) > 0 {
			costB = remainingB
		}
		if costB.Sign() <= 0 {
			break
		}
		outA = scommon.DecimalAdd(outA, fillA)
		spentB = scommon.DecimalAdd(spentB, costB)
		currentSold = scommon.DecimalAdd(currentSold, fillA)
		remainingA = scommon.DecimalSub(remainingA, fillA)
		remainingB = scommon.DecimalSub(remainingB, costB)
	}
	unitPrice := parseDecimalOrZero("0")
	if outA.Sign() > 0 {
		unitPrice = scommon.DecimalDiv(spentB, outA)
	}
	return exchangeQuoteResult{
		OutA:      outA,
		SpentB:    spentB,
		UnitPrice: unitPrice,
	}
}

func exchangeTierAvailableA(contract *ExchangeContract, soldA *scommon.Decimal) *scommon.Decimal {
	if contract.PriceMode != ExchangePriceModeSoldA {
		return nil
	}
	for _, step := range contract.Steps {
		threshold := parseDecimalOrZero(step.Threshold)
		if threshold.Cmp(soldA) > 0 {
			return scommon.DecimalSub(threshold, soldA)
		}
	}
	return nil
}

func settleExchangeClose(state *TemplateRuntimeState, contract *ExchangeContract, item *InvokeItem,
	plan *SettlementPlan, deployer, gasAssetName string) error {

	addSettlementInputs(plan, item)
	plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
	if item.Address != deployer {
		item.Reason = InvokeReasonInvalid
		item.Done = ItemStatusClosedDirectly
		return nil
	}
	if err := applyExchangeGasFee(state, contract, item, gasAssetName); err != nil {
		return err
	}
	assetA := state.Running.AssetAInPool
	if assetA == nil {
		assetA = parseDecimalOrZero("0")
	}
	if assetA.Sign() > 0 {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        deployer,
			AssetName: contract.AssetAName,
			AssetAmt:  decimalString(assetA),
			Reason:    SettlementReasonDeal,
		})
	}
	assetB := state.Running.AssetBInPool
	if assetB == nil {
		assetB = parseDecimalOrZero("0")
	}
	if assetB.Sign() > 0 {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        deployer,
			AssetName: contract.AssetBName,
			AssetAmt:  decimalString(assetB),
			Reason:    SettlementReasonDeal,
		})
	}
	inputB := item.RemainingAmt
	if inputB == nil {
		inputB = parseDecimalOrZero("0")
	}
	if gasAssetName == contract.AssetBName && item.ServiceFee > 0 {
		inputB = scommon.DecimalSub(inputB, scommon.NewDefaultDecimal(item.ServiceFee))
		if inputB.Sign() < 0 {
			inputB = parseDecimalOrZero("0")
		}
	}
	if inputB.Sign() > 0 {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        deployer,
			AssetName: contract.AssetBName,
			AssetAmt:  decimalString(inputB),
			Reason:    SettlementReasonDeal,
		})
	}
	state.Running.AssetAInPool = nil
	state.Running.AssetBInPool = nil
	state.Running.Closed = true
	item.Done = ItemStatusDealt
	return nil
}

func markExchangeRefunded(state *TemplateRuntimeState, item *InvokeItem, plan *SettlementPlan, contract *ExchangeContract, gasAssetName string) {
	item.Reason = SettlementReasonRefund
	item.Done = ItemStatusRefunded
	refundB := item.RemainingAmt
	if refundB == nil {
		refundB = parseDecimalOrZero("0")
	}
	if item.ServiceFee > 0 && gasAssetName == contract.AssetBName {
		refundB = scommon.DecimalSub(refundB, scommon.NewDefaultDecimal(item.ServiceFee))
		if refundB.Sign() < 0 {
			refundB = parseDecimalOrZero("0")
		}
	}
	item.OutAmt = refundB
	item.RemainingAmt = nil
	if refundB.Sign() > 0 {
		plan.Transfers = append(plan.Transfers, SettlementTransfer{
			ItemID:    item.ID,
			To:        item.Address,
			AssetName: contract.AssetBName,
			AssetAmt:  decimalString(item.OutAmt),
			Reason:    SettlementReasonRefund,
		})
	}
	if state.Running.TotalRefundAssetB == nil {
		state.Running.TotalRefundAssetB = parseDecimalOrZero("0")
	}
	state.Running.TotalRefundAssetB = scommon.DecimalAdd(state.Running.TotalRefundAssetB, refundB)
}

func applyExchangeGasFee(state *TemplateRuntimeState, contract *ExchangeContract, item *InvokeItem, gasAssetName string) error {
	if item.ServiceFee <= 0 || gasAssetName == "" {
		return nil
	}
	fee := scommon.NewDefaultDecimal(item.ServiceFee)
	switch gasAssetName {
	case contract.AssetAName:
		pool := state.Running.AssetAInPool
		if pool == nil {
			pool = parseDecimalOrZero("0")
		}
		if pool.Cmp(fee) < 0 {
			return fmt.Errorf("insufficient exchange asset A for gas fee")
		}
		state.Running.AssetAInPool = scommon.DecimalSub(pool, fee)
	case contract.AssetBName:
		return nil
	default:
		if state.Running.GasBalance < item.ServiceFee {
			return fmt.Errorf("insufficient exchange gas balance")
		}
		state.Running.GasBalance -= item.ServiceFee
	}
	return nil
}

func exchangePriceAt(contract *ExchangeContract, height int64, soldA string) *scommon.Decimal {
	var selected string
	switch contract.PriceMode {
	case ExchangePriceModeHeight:
		for _, step := range contract.Steps {
			threshold, err := strconv.ParseInt(step.Threshold, 10, 64)
			if err != nil {
				continue
			}
			if height >= threshold {
				selected = step.BPerA
			}
		}
	case ExchangePriceModeSoldA:
		sold := parseDecimalOrZero(soldA)
		for _, step := range contract.Steps {
			threshold := parseDecimalOrZero(step.Threshold)
			if sold.Cmp(threshold) >= 0 {
				selected = step.BPerA
			}
		}
	}
	return parseDecimalOrZero(selected)
}
