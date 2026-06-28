package template

import (
	"fmt"
	"math/big"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

func DeriveInvokeCallID(invokeTxID string, vout uint32, contract ContractAddress) string {
	return contractframework.DeriveInvokeCallID("template-invoke", invokeTxID, vout, contract)
}

func BuildSettlementResultPlans(plans []*SettlementPlan, records []ExecutionRecord,
	assetPrecision contractframework.AssetPrecisionResolver) ([]ResultPlan, error) {

	inputsByItem := make(map[int64][]OutPoint)
	feesByItem := make(map[int64]*scommon.Decimal)
	for _, record := range records {
		for _, itemID := range record.ItemIDs {
			inputsByItem[itemID] = append(inputsByItem[itemID], record.FundingInputs...)
			feesByItem[itemID] = decimalAddAllowNil(feesByItem[itemID], record.GasFee)
		}
	}

	return contractframework.BuildSettlementResultPlans(plans, templateSettlementResultOptions(
		inputsByItem, feesByItem, assetPrecision))
}

func AddMissingGasResultPlans(plans []ResultPlan, records []ExecutionRecord) []ResultPlan {
	out := contractframework.CloneResultPlans(plans)
	coveredItems := make(map[int64]struct{})
	planByContract := make(map[string]int)
	for i := range out {
		planByContract[out[i].Contract] = i
		for _, itemID := range out[i].ItemIDs {
			coveredItems[itemID] = struct{}{}
		}
	}
	for _, record := range records {
		if !record.RequiresResult || record.GasFee == nil || record.GasFee.Sign() == 0 {
			continue
		}
		if len(record.ItemIDs) > 0 && executionRecordItemsCovered(record, coveredItems) {
			continue
		}
		contract := record.Contract.MustEncode()
		i, ok := planByContract[contract]
		if !ok {
			i = len(out)
			planByContract[contract] = i
			out = append(out, ResultPlan{Contract: contract, Height: record.Height})
		}
		out[i].GasFee = contractframework.DecimalAddAllowNil(out[i].GasFee, record.GasFee)
		out[i].Inputs = append(out[i].Inputs, record.FundingInputs...)
		out[i].Inputs = contractframework.UniqueOutPoints(out[i].Inputs)
		out[i].ItemIDs = appendMissingItemIDs(out[i].ItemIDs, record.ItemIDs)
	}
	return out
}

func executionRecordItemsCovered(record ExecutionRecord, covered map[int64]struct{}) bool {
	if len(record.ItemIDs) == 0 {
		return false
	}
	for _, itemID := range record.ItemIDs {
		if _, ok := covered[itemID]; !ok {
			return false
		}
	}
	return true
}

func appendMissingItemIDs(ids []int64, more []int64) []int64 {
	for _, id := range more {
		ids = appendPlanItemID(ids, id)
	}
	return ids
}

func BuildSettlementAssetIntentsByItem(plan *SettlementPlan,
	assetPrecision contractframework.AssetPrecisionResolver) (map[int64][]AssetIntent, error) {

	return contractframework.BuildSettlementAssetIntentsByItem(plan, templateSettlementResultOptions(nil, nil, assetPrecision))
}

func templateSettlementResultOptions(inputsByItem map[int64][]OutPoint,
	feesByItem map[int64]*scommon.Decimal,
	assetPrecision contractframework.AssetPrecisionResolver) contractframework.SettlementResultOptions {

	return contractframework.SettlementResultOptions{
		SatoshiAssetName: SatoshiAssetName,
		Precision:        contractframework.AssetPrecisionPolicy{Fallback: MaxPriceDivisibility, Resolve: assetPrecision},
		InvalidAsset:     ErrInvalidAsset,
		InputsByItem:     inputsByItem,
		FeesByItem:       feesByItem,
	}
}

func AugmentResultPlans(plans []ResultPlan, store *RuntimeStore, gasConfig GasConfig,
	contractUTXOs ContractUTXOProvider,
	assetPrecision contractframework.AssetPrecisionResolver) ([]ResultPlan, error) {

	precision := templateSettlementResultOptions(nil, nil, assetPrecision).Precision
	gasConfig = gasConfig.Normalize()
	out := contractframework.CloneResultPlans(plans)
	for i := range out {
		view, err := contractframework.CollectResultPlanUTXOs(out[i], contractUTXOs)
		if err != nil {
			return nil, err
		}
		contract := view.Contract
		if contractUTXOs != nil {
			closed, deployer, err := closedContractChangeRecipient(contract, store)
			if err != nil {
				return nil, err
			}
			retain, err := contractChangeOutput(contract, store, gasConfig, view.Assets)
			if err != nil {
				return nil, err
			}
			mode := contractframework.ResultSurplusToBootstrap
			managedMode := contractframework.ResultSurplusToContract
			if closed {
				mode = contractframework.ResultSurplusAsProfit
				managedMode = contractframework.ResultSurplusAsProfit
			}
			augmented, err := contractframework.AugmentResultPlanWithManagedState(
				contractframework.ManagedResultAugmentRequest{
					Plan:             out[i],
					View:             view,
					ManagedAssets:    retain,
					GasAssetName:     gasConfig.GasAssetName,
					GasFee:           out[i].GasFee,
					Precision:        precision,
					ManagedGasPaid:   templateManagedGasPaid(contract, store, out[i]),
					DeployerAddress:  deployer,
					BootstrapAddress: gasConfig.BootstrapAddress,
					ManagedMode:      managedMode,
					SurplusMode:      mode,
				})
			if err != nil {
				return nil, err
			}
			out[i] = augmented
		} else {
			change, err := contractChangeOutput(contract, store, gasConfig, nil)
			if err != nil {
				return nil, err
			}
			change = contractframework.NormalizeResultOutputPrecision(change, precision)
			if !contractframework.ResultOutputIsZero(change) {
				out[i].Outputs = append(out[i].Outputs, change)
			}
		}
		out[i].Inputs = contractframework.UniqueOutPoints(out[i].Inputs)
	}
	return out, nil
}

func templateManagedGasPaid(contract ContractAddress, store *RuntimeStore, plan ResultPlan) bool {
	if store == nil {
		return false
	}
	runtime, ok := store.Get(contract)
	if !ok || runtime == nil {
		return false
	}
	if _, ok := runtime.Contract().(*ExchangeContract); ok {
		return true
	}
	if len(plan.ItemIDs) == 0 {
		return false
	}
	state, err := runtime.RuntimeState()
	if err != nil {
		return false
	}
	ids := make(map[int64]struct{}, len(plan.ItemIDs))
	for _, id := range plan.ItemIDs {
		ids[id] = struct{}{}
	}
	for i := range state.Items {
		item := &state.Items[i]
		if _, ok := ids[item.ID]; ok && item.Reason == InvokeReasonInvalid {
			return true
		}
	}
	return false
}

func capClosedResultOutputsByAvailable(outputs []ResultOutput, availableAssets wire.TxAssets, availableValue int64, gasAssetName string, gasFee *scommon.Decimal) []ResultOutput {
	out := contractframework.CloneResultOutputs(outputs)
	capResultOutputValuesByAvailable(out, availableValue)
	capResultOutputAssetsByAvailable(out, availableAssets, gasAssetName, gasFee)
	return contractframework.CompactResultOutputs(out)
}

func capResultOutputValuesByAvailable(outputs []ResultOutput, availableValue int64) {
	total := resultOutputsValue(outputs)
	if total <= 0 || availableValue < 0 || total <= availableValue {
		return
	}
	originalValues := make([]int64, len(outputs))
	remaining := availableValue
	for i := range outputs {
		originalValues[i] = outputs[i].Value
		if outputs[i].Value <= 0 {
			outputs[i].Value = 0
			continue
		}
		scaled := new(big.Int).Mul(big.NewInt(outputs[i].Value), big.NewInt(availableValue))
		scaled.Div(scaled, big.NewInt(total))
		outputs[i].Value = scaled.Int64()
		remaining -= outputs[i].Value
	}
	for i := range outputs {
		if remaining <= 0 {
			break
		}
		if originalValues[i] <= 0 {
			continue
		}
		outputs[i].Value++
		remaining--
	}
}

func capResultOutputAssetsByAvailable(outputs []ResultOutput, availableAssets wire.TxAssets, gasAssetName string, gasFee *scommon.Decimal) {
	requested := resultRequestedAssetTotals(outputs)
	if len(requested) == 0 {
		return
	}
	limits := availableAssetLimits(availableAssets, gasAssetName, gasFee)
	for i := range outputs {
		if len(outputs[i].Assets) == 0 {
			continue
		}
		nextAssets := make(wire.TxAssets, 0, len(outputs[i].Assets))
		for _, asset := range outputs[i].Assets {
			key := asset.Name.String()
			limit := limits[key]
			total := requested[key]
			if limit == nil || limit.Sign() <= 0 || total == nil || total.Sign() <= 0 {
				if assetNameKey(outputs[i].AssetName) == key {
					outputs[i].AssetAmt = ""
				}
				continue
			}
			amount := asset.Amount.Clone()
			if total.Cmp(limit) > 0 {
				amount = decimalMulByFraction(amount, limit, total)
			}
			if amount.Sign() <= 0 {
				if assetNameKey(outputs[i].AssetName) == key {
					outputs[i].AssetAmt = ""
				}
				continue
			}
			next := asset
			next.Amount = *amount
			nextAssets = append(nextAssets, next)
			if assetNameKey(outputs[i].AssetName) == key {
				outputs[i].AssetAmt = amount.String()
			}
		}
		if len(nextAssets) == 0 {
			outputs[i].Assets = nil
			outputs[i].AssetName = ""
			outputs[i].AssetAmt = ""
		} else {
			outputs[i].Assets = nextAssets
		}
	}
}

func resultRequestedAssetTotals(outputs []ResultOutput) map[string]*scommon.Decimal {
	totals := make(map[string]*scommon.Decimal)
	for _, output := range outputs {
		for _, asset := range output.Assets {
			key := asset.Name.String()
			totals[key] = decimalAddAllowNil(totals[key], asset.Amount.Clone())
		}
	}
	return totals
}

func decimalMulByFraction(amount, numerator, denominator *scommon.Decimal) *scommon.Decimal {
	if amount == nil || numerator == nil || denominator == nil || denominator.Sign() <= 0 {
		return parseDecimalOrZero("0")
	}
	return scommon.DecimalMulV2(amount, numerator).Div(denominator)
}

func availableAssetLimits(availableAssets wire.TxAssets, gasAssetName string, gasFee *scommon.Decimal) map[string]*scommon.Decimal {
	limits := make(map[string]*scommon.Decimal)
	for _, asset := range availableAssets {
		limits[asset.Name.String()] = asset.Amount.Clone()
	}
	if gasAssetName != "" && gasFee != nil && gasFee.Sign() > 0 {
		limit := limits[assetNameKey(gasAssetName)]
		if limit != nil {
			limit = limit.SubAlignPrecision(gasFee)
			if limit.Sign() < 0 {
				limit = parseDecimalOrZero("0")
			}
			limits[assetNameKey(gasAssetName)] = limit
		}
	}
	return limits
}

func assetNameKey(assetName string) string {
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return assetName
	}
	return name.String()
}

func closedContractChangeRecipient(contract ContractAddress, store *RuntimeStore) (bool, string, error) {
	if store == nil {
		return false, "", nil
	}
	runtime, ok := store.Get(contract)
	if !ok || runtime == nil {
		return false, "", nil
	}
	state, err := runtime.loadRuntimeState()
	if err != nil {
		return false, "", err
	}
	return state.Running.Closed, runtime.RuntimeBase().Deployer(), nil
}

func splitClosedProfitChange(change ResultOutput, deployer, bootstrap string) []ResultOutput {
	if contractframework.ResultOutputIsZero(change) {
		return nil
	}
	if deployer == "" || bootstrap == "" || deployer == bootstrap {
		change.To = deployer
		return []ResultOutput{change}
	}
	deployerOut := ResultOutput{To: deployer}
	bootstrapOut := ResultOutput{To: bootstrap}
	deployerOut.Value = change.Value * 6 / 10
	bootstrapOut.Value = change.Value - deployerOut.Value
	deployerOut.Assets, bootstrapOut.Assets = splitAssetsByBPS(change.Assets, 6000)
	out := make([]ResultOutput, 0, 2)
	if !contractframework.ResultOutputIsZero(deployerOut) {
		out = append(out, deployerOut)
	}
	if !contractframework.ResultOutputIsZero(bootstrapOut) {
		out = append(out, bootstrapOut)
	}
	return out
}

func splitAssetsByBPS(assets wire.TxAssets, deployerBPS int64) (wire.TxAssets, wire.TxAssets) {
	if len(assets) == 0 {
		return nil, nil
	}
	deployer := make(wire.TxAssets, 0, len(assets))
	bootstrap := make(wire.TxAssets, 0, len(assets))
	for _, asset := range assets {
		deployerAmt := asset.Amount.MulBigInt(big.NewInt(deployerBPS)).DivBigInt(big.NewInt(10000))
		bootstrapAmt := scommon.DecimalSub(asset.Amount.Clone(), deployerAmt)
		if deployerAmt.Sign() > 0 {
			next := asset
			next.Amount = *deployerAmt
			deployer = append(deployer, next)
		}
		if bootstrapAmt.Sign() > 0 {
			next := asset
			next.Amount = *bootstrapAmt
			bootstrap = append(bootstrap, next)
		}
	}
	if len(deployer) == 0 {
		deployer = nil
	}
	if len(bootstrap) == 0 {
		bootstrap = nil
	}
	return deployer, bootstrap
}

func resultAssetsChange(available wire.TxAssets, outputs []ResultOutput, gasAssetName string, gasFee *scommon.Decimal) (wire.TxAssets, error) {
	if len(available) == 0 {
		return nil, nil
	}
	spent := wire.TxAssets(nil)
	for _, output := range outputs {
		if len(output.Assets) == 0 {
			continue
		}
		if err := spent.Merge(output.Assets); err != nil {
			return nil, err
		}
	}
	change := available.Clone()
	if err := change.Split(spent); err != nil {
		return nil, err
	}
	if gasFee != nil && gasFee.Sign() > 0 {
		feeAssets, err := newGasAssetSet(gasAssetName, gasFee.String())
		if err != nil {
			return nil, err
		}
		if err := change.Split(feeAssets); err != nil {
			return nil, fmt.Errorf("insufficient gas asset for result fee: %w", err)
		}
	}
	if len(change) == 0 {
		return nil, nil
	}
	return change, nil
}

func resultOutputsValue(outputs []ResultOutput) int64 {
	value := int64(0)
	for _, output := range outputs {
		value += output.Value
	}
	return value
}

func contractChangeOutput(contract ContractAddress, store *RuntimeStore, gasConfig GasConfig, availableAssets wire.TxAssets) (ResultOutput, error) {
	if store == nil {
		return ResultOutput{}, nil
	}
	runtime, ok := store.Get(contract)
	if !ok || runtime == nil {
		return ResultOutput{}, nil
	}
	state, err := runtime.loadRuntimeState()
	if err != nil {
		return ResultOutput{}, err
	}

	assets := wire.TxAssets{}
	assetName := contractAssetName(runtime.Contract())
	if assetName != "" && state.Running.AssetAInPool != nil && state.Running.AssetAInPool.Sign() > 0 {
		poolAssets, err := newAssetSet(assetName, state.Running.AssetAInPool.String())
		if err != nil {
			return ResultOutput{}, err
		}
		if err := assets.Merge(poolAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	if exchange, ok := runtime.Contract().(*ExchangeContract); ok &&
		exchange.AssetBName != "" &&
		exchange.AssetBName != SatoshiAssetName &&
		state.Running.AssetBInPool != nil &&
		state.Running.AssetBInPool.Sign() > 0 {
		poolAssets, err := newAssetSet(exchange.AssetBName, state.Running.AssetBInPool.String())
		if err != nil {
			return ResultOutput{}, err
		}
		if err := assets.Merge(poolAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	openValue, openAssets, err := openOrderManagedAssets(runtime.Contract(), &state)
	if err != nil {
		return ResultOutput{}, err
	}
	if len(openAssets) != 0 {
		if err := assets.Merge(openAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	gasAssetName := gasConfig.GasAssetName
	if gasAssetName == "" {
		gasAssetName = DefaultGasConfig().GasAssetName
	}
	if state.Running.GasBalance != nil && state.Running.GasBalance.Sign() > 0 {
		gasAssets, err := newGasAssetSet(gasAssetName, state.Running.GasBalance.String())
		if err != nil {
			return ResultOutput{}, err
		}
		if err := assets.Merge(gasAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	if len(assets) == 0 {
		assets = nil
	} else if len(availableAssets) != 0 {
		assets = capAssetsByAvailable(assets, availableAssets)
	}
	to := contract.MustEncode()
	if state.Running.Closed {
		to = runtime.RuntimeBase().Deployer()
	}
	return ResultOutput{
		To:     to,
		Value:  contractChangeValue(runtime.Contract(), state.Running.AssetBInPool) + openValue,
		Assets: assets,
	}, nil
}

func openOrderManagedAssets(contract Contract, state *TemplateRuntimeState) (int64, wire.TxAssets, error) {
	if state == nil {
		return 0, nil, nil
	}
	limit, ok := contract.(*LimitOrderContract)
	if !ok {
		return 0, nil, nil
	}
	var value int64
	var assets wire.TxAssets
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal {
			continue
		}
		switch item.OrderType {
		case OrderTypeBuy:
			value += item.RemainingValue
		case OrderTypeSell:
			if item.RemainingAmt == nil || item.RemainingAmt.Sign() <= 0 {
				continue
			}
			poolAssets, err := newAssetSet(limit.AssetName, item.RemainingAmt.String())
			if err != nil {
				return 0, nil, err
			}
			if err := assets.Merge(poolAssets); err != nil {
				return 0, nil, err
			}
		}
	}
	return value, assets, nil
}

func contractChangeValue(contract Contract, assetB *scommon.Decimal) int64 {
	if exchange, ok := contract.(*ExchangeContract); ok {
		if exchange.AssetBName == SatoshiAssetName {
			return decimalInt64(assetB)
		}
		return 0
	}
	return decimalInt64(assetB)
}

func capAssetsByAvailable(assets, available wire.TxAssets) wire.TxAssets {
	if len(assets) == 0 || len(available) == 0 {
		return assets
	}
	out := make(wire.TxAssets, 0, len(assets))
	for _, asset := range assets {
		availableAsset, err := available.Find(&asset.Name)
		if err != nil || availableAsset == nil {
			continue
		}
		amount := asset.Amount.Clone()
		if amount.Cmp(&availableAsset.Amount) > 0 {
			amount = availableAsset.Amount.Clone()
		}
		if amount.Sign() <= 0 {
			continue
		}
		out = append(out, scommon.AssetInfo{
			Name:       asset.Name,
			Amount:     *amount,
			BindingSat: asset.BindingSat,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func BuildCanonicalSettlementResultTx(status ResultStatus, settlementPlans []*SettlementPlan, records []ExecutionRecord, resolve ResultRecipientScriptResolver) (*wire.MsgTx, []ResultPlan, error) {
	plans, err := BuildSettlementResultPlans(settlementPlans, records, nil)
	if err != nil {
		return nil, nil, err
	}
	tx, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status:        status,
		Plans:         plans,
		ResolveScript: resolve,
	}, contractframework.ResultTxBuildOptions{PlanCount: resultPlanCount})
	if err != nil {
		return nil, nil, err
	}
	return tx, plans, nil
}

func resultPlanCount(plan ResultPlan) int {
	if len(plan.ItemIDs) != 0 {
		return len(plan.ItemIDs)
	}
	return 1
}

func newAssetSet(assetName, amount string) (wire.TxAssets, error) {
	return newAssetSetWithPrecision(assetName, amount, MaxPriceDivisibility)
}

func newGasAssetSet(assetName, amount string) (wire.TxAssets, error) {
	return newAssetSetWithPrecision(assetName, amount, contractcommon.GasFeePrecision)
}

func newAssetSetWithPrecision(assetName, amount string, maxPrecision int) (wire.TxAssets, error) {
	return contractframework.NewAssetSetWithPrecision(assetName, amount, maxPrecision, ErrInvalidAsset)
}

func DeriveDeployCallID(deployTxID string, contract ContractAddress) string {
	return contractframework.DeriveDeployCallID("template-deploy", deployTxID, contract)
}
