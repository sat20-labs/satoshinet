package agent

import contractframework "github.com/sat20-labs/satoshinet/contract/framework"

func agentSettlementResultOptions() contractframework.SettlementResultOptions {
	return contractframework.SettlementResultOptions{
		SatoshiAssetName:      SatoshiAssetName,
		MaxPrecision:          MaxPredictionDecimalPrecision,
		InvalidAsset:          ErrInvalidAsset,
		RequireIntegerSatoshi: true,
		IntegerSatoshiError:   "satoshi output amount must be integer",
		MissingPlanError:      "missing prediction settlement plan",
	}
}

func AugmentResultPlans(plans []ResultPlan, contractUTXOs ContractUTXOProvider) ([]ResultPlan, error) {
	out := contractframework.CloneResultPlans(plans)
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
		poolAsset := contractframework.ResultPlanAssetName(out[i])
		poolAmount := contractframework.ZeroDecimal()
		out[i].Inputs = view.Inputs
		for _, utxo := range view.UTXOs {
			if poolAsset != "" {
				amount, err := utxo.AssetAmount(poolAsset)
				if err != nil {
					return nil, err
				}
				poolAmount = poolAmount.AddAlignPrecision(amount)
			}
		}
		if poolAsset != "" {
			scaled, err := contractframework.ScaleResultOutputsToPool(out[i].Outputs,
				poolAsset, poolAmount, agentSettlementResultOptions())
			if err != nil {
				return nil, err
			}
			out[i].Outputs = scaled
		}
		out[i].Inputs = contractframework.UniqueOutPoints(out[i].Inputs)
	}
	return out, nil
}
