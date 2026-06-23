package agent

import contractframework "github.com/sat20-labs/satoshinet/contract/framework"

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
		view, err := contractframework.CollectResultPlanUTXOs(out[i], contractUTXOs)
		if err != nil {
			return nil, err
		}
		if contractUTXOs == nil {
			continue
		}
		if len(out[i].Outputs) == 0 {
			out[i].Inputs = contractframework.UniqueOutPoints(out[i].Inputs)
			continue
		}
		for j := range out[i].Outputs {
			if err := contractframework.RebuildResultOutputAsset(&out[i].Outputs[j], opts); err != nil {
				return nil, err
			}
		}
		retain, deployer := agentManagedGasRetain(out[i], store, gasAssetName)
		augmented, err := contractframework.AugmentResultPlanWithManagedState(
			contractframework.ManagedResultAugmentRequest{
				Plan:             out[i],
				View:             view,
				ManagedAssets:    retain,
				GasAssetName:     gasAssetName,
				GasFee:           out[i].GasFee,
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

func agentManagedGasRetain(plan ResultPlan, store *RuntimeStore, gasAssetName string) (ResultOutput, string) {
	if store == nil || gasAssetName == "" {
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
	gasBalance := parseDecimalOrZero(runtime.State().Prediction.GasBalance)
	if gasBalance.Sign() <= 0 {
		return ResultOutput{}, runtime.deployer
	}
	if gasAssetName == SatoshiAssetName {
		value, err := contractframework.DecimalToInt64(*gasBalance)
		if err != nil {
			return ResultOutput{}, runtime.deployer
		}
		return ResultOutput{To: plan.Contract, Value: value}, runtime.deployer
	}
	assets, err := contractframework.NewAssetSetWithPrecisionPolicy(gasAssetName, gasBalance.String(),
		agentSettlementResultOptions(runtime.config.AssetPrecision).Precision, ErrInvalidAsset)
	if err != nil {
		return ResultOutput{}, runtime.deployer
	}
	return ResultOutput{To: plan.Contract, Assets: assets}, runtime.deployer
}
