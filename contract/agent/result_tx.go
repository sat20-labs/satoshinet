package agent

import (
	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func defaultAgentSettlementResultOptions() contractframework.SettlementResultOptions {
	return agentSettlementResultOptions(nil)
}

func agentSettlementResultOptions(assetPrecision contractframework.AssetPrecisionResolver) contractframework.SettlementResultOptions {
	return contractframework.SettlementResultOptions{
		SatoshiAssetName:      SatoshiAssetName,
		Precision:             contractframework.AssetPrecisionPolicy{Fallback: MaxPredictionDecimalPrecision, Resolve: assetPrecision},
		InvalidAsset:          ErrInvalidAsset,
		RequireIntegerSatoshi: true,
		IntegerSatoshiError:   "satoshi output amount must be integer",
		MissingPlanError:      "missing prediction settlement plan",
	}
}

func AugmentResultPlans(plans []ResultPlan, contractUTXOs ContractUTXOProvider,
	store *RuntimeStore, assetPrecision contractframework.AssetPrecisionResolver,
	gasAssetName, bootstrapAddress string) ([]ResultPlan, error) {

	out := contractframework.CloneResultPlans(plans)
	opts := agentSettlementResultOptions(assetPrecision)
	for i := range out {
		closePlan := stripAgentClosePlanMarker(&out[i])
		view, err := contractframework.CollectResultPlanUTXOs(out[i], contractUTXOs)
		if err != nil {
			return nil, err
		}
		if contractUTXOs == nil {
			continue
		}
		if len(out[i].Outputs) == 0 && len(out[i].GasRefunds) == 0 {
			out[i].Inputs = contractframework.UniqueOutPoints(out[i].Inputs)
			continue
		}
		for j := range out[i].Outputs {
			if err := contractframework.RebuildResultOutputAsset(&out[i].Outputs[j], opts); err != nil {
				return nil, err
			}
		}
		retain, deployer := agentManagedRetain(out[i], store, gasAssetName, closePlan)
		augmented, err := contractframework.AugmentResultPlanWithManagedState(
			contractframework.ManagedResultAugmentRequest{
				Plan:             out[i],
				View:             view,
				ManagedAssets:    retain,
				GasAssetName:     gasAssetName,
				GasFee:           out[i].GasFee,
				Precision:        opts.Precision,
				DeployerAddress:  deployer,
				BootstrapAddress: bootstrapAddress,
				ManagedMode:      contractframework.ResultSurplusAsProfit,
				SurplusMode:      contractframework.ResultSurplusToBootstrap,
			})
		if err != nil {
			return nil, err
		}
		out[i] = augmented
	}
	return out, nil
}

func stripAgentClosePlanMarker(plan *ResultPlan) bool {
	if plan == nil {
		return false
	}
	closePlan := false
	outputs := plan.Outputs[:0]
	for _, output := range plan.Outputs {
		if output.Reason == agentClosePlanReason {
			closePlan = true
			continue
		}
		outputs = append(outputs, output)
	}
	plan.Outputs = outputs
	return closePlan
}

func agentManagedRetain(plan ResultPlan, store *RuntimeStore, gasAssetName string, closePlan bool) (ResultOutput, string) {
	if store == nil {
		return ResultOutput{}, ""
	}
	addr, err := DecodeContractAddress(plan.Contract)
	if err != nil {
		return ResultOutput{}, ""
	}
	runtime, ok := store.Get(addr)
	if !ok || runtime == nil {
		return ResultOutput{}, ""
	}
	if closePlan {
		return agentCloseManagedAssets(runtime, gasAssetName), runtime.deployer
	}
	return agentManagedGasAsset(runtime, gasAssetName), runtime.deployer
}

func agentManagedGasAsset(runtime *Runtime, gasAssetName string) ResultOutput {
	if runtime == nil || gasAssetName == "" {
		return ResultOutput{}
	}
	addr := runtime.Address()
	contractAddress := addr.EncodeAddress()
	gasBalance := parseDecimalOrZero(runtime.State().Prediction.GasBalance)
	if gasBalance.Sign() <= 0 {
		return ResultOutput{}
	}
	if gasAssetName == SatoshiAssetName {
		value, err := contractframework.DecimalToInt64(*gasBalance)
		if err != nil {
			return ResultOutput{}
		}
		return ResultOutput{To: contractAddress, Value: value}
	}
	assets, err := contractframework.NewAssetSetWithPrecisionPolicy(gasAssetName, gasBalance.String(),
		agentSettlementResultOptions(runtime.config.AssetPrecision).Precision, ErrInvalidAsset)
	if err != nil {
		return ResultOutput{}
	}
	return ResultOutput{To: contractAddress, Assets: assets}
}

func agentCloseManagedAssets(runtime *Runtime, gasAssetName string) ResultOutput {
	if runtime == nil {
		return ResultOutput{}
	}
	addr := runtime.Address()
	contractAddress := addr.EncodeAddress()
	out := ResultOutput{To: contractAddress}
	if gasAssetName != "" {
		gasBalance := parseDecimalOrZero(runtime.State().Prediction.GasBalance)
		addAgentManagedAmount(&out, contractAddress, gasAssetName, gasBalance, runtime.config.AssetPrecision)
	}
	betAsset := runtime.Contract().BetAsset
	betTotal := runtime.totalBetAmount()
	if betTotal == nil || betTotal.Sign() <= 0 || betAsset == "" {
		return out
	}
	addAgentManagedAmount(&out, contractAddress, betAsset, betTotal, runtime.config.AssetPrecision)
	return out
}

func addAgentManagedAmount(out *ResultOutput, contractAddress, assetName string, amount *scommon.Decimal,
	precision contractframework.AssetPrecisionResolver) {

	if out == nil || amount == nil || amount.Sign() <= 0 {
		return
	}
	out.To = contractAddress
	if assetName == SatoshiAssetName {
		value, err := contractframework.DecimalToInt64(*amount)
		if err == nil {
			out.Value += value
		}
		return
	}
	policy := agentSettlementResultOptions(precision).Precision
	assets, err := contractframework.NewAssetSetWithPrecisionPolicy(assetName, amount.String(), policy, ErrInvalidAsset)
	if err != nil {
		return
	}
	for _, asset := range assets {
		merged := false
		for i := range out.Assets {
			if out.Assets[i].Name.String() == asset.Name.String() {
				next := scommon.DecimalAdd(&out.Assets[i].Amount, &asset.Amount)
				out.Assets[i].Amount = *next
				merged = true
				break
			}
		}
		if !merged {
			out.Assets = append(out.Assets, asset)
		}
	}
}
