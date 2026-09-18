package agent

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func defaultAgentSettlementResultOptions() contractframework.SettlementResultOptions {
	return agentSettlementResultOptions(nil)
}

func agentSettlementResultOptions(assetPrecision contractframework.AssetPrecisionResolver) contractframework.SettlementResultOptions {
	return contractframework.SettlementResultOptions{
		SatoshiAssetName:      SatoshiAssetName,
		Precision:            contractframework.AssetPrecisionPolicy{Fallback: MaxPredictionDecimalPrecision, Resolve: assetPrecision},
		InvalidAsset:         ErrInvalidAsset,
		RequireIntegerSatoshi: true,
		IntegerSatoshiError:  "satoshi output amount must be integer",
		MissingPlanError:     "missing prediction settlement plan",
	}
}

// The business runtime supplies payouts; the framework owns retention,
// liquidation profit and anomaly recovery. No UTXO ownership is persisted.
func AugmentResultPlans(plans []ResultPlan, contractUTXOs ContractUTXOProvider,
	store *RuntimeStore, assetPrecision contractframework.AssetPrecisionResolver,
	gasAssetName, bootstrapAddress string) ([]ResultPlan, error) {

	out := contractframework.MergeResultPlansByContract(plans)
	opts := agentSettlementResultOptions(assetPrecision)
	for i := range out {
		if out[i].ManagedRemainder != nil {
			continue
		}
		if contractUTXOs == nil {
			return nil, fmt.Errorf("missing agent contract UTXO provider")
		}
		addr, err := DecodeContractAddress(out[i].Contract)
		if err != nil {
			return nil, err
		}
		managed := contractcommon.ManagedBalance{}
		deployer := ""
		closed := false
		if store != nil {
			if runtime, ok := store.Get(addr); ok && runtime != nil {
				managed = runtime.managed.Clone()
				deployer = runtime.deployer
				closed = runtime.state.Closed
			}
		}
		if closed {
			out[i].InputScope = contractframework.ResultInputScopeAllContractUTXOs
		}
		view, err := contractframework.CollectResultPlanUTXOs(out[i], contractUTXOs)
		if err != nil {
			return nil, err
		}
		out[i], err = contractframework.AugmentManagedResultPlan(contractframework.ManagedResultRequest{
			Plan: out[i], View: view, Managed: managed,
			Closed: closed, DeployerAddress: deployer, BootstrapAddress: bootstrapAddress,
			GasAssetName: gasAssetName, Precision: opts.Precision,
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
