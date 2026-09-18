package template

import (
	"fmt"

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

	recordsByContract := make(map[string][]ExecutionRecord)
	for _, record := range records {
		key := record.Contract.MustEncode()
		recordsByContract[key] = append(recordsByContract[key], record)
	}
	var out []ResultPlan
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		inputsByItem := make(map[int64][]OutPoint)
		feesByItem := make(map[int64]*scommon.Decimal)
		gasRefundsByItem := make(map[int64]contractframework.ResultGasRefund)
		for _, record := range recordsByContract[plan.Contract] {
			for _, itemID := range record.ItemIDs {
				inputsByItem[itemID] = append(inputsByItem[itemID], record.FundingInputs...)
				feesByItem[itemID] = decimalAddAllowNil(feesByItem[itemID], record.GasFee)
				if record.ResultFeeMode == contractframework.ResultFeeModeGasAsset {
					if refund := contractframework.ResultGasRefundFromRecord(record); refund.To != "" {
						gasRefundsByItem[itemID] = refund
					}
				}
			}
		}
		built, err := contractframework.BuildSettlementResultPlans([]*SettlementPlan{plan},
			templateSettlementResultOptions(inputsByItem, feesByItem, gasRefundsByItem, assetPrecision))
		if err != nil {
			return nil, err
		}
		out = append(out, built...)
	}
	return contractframework.AttachCallFunding(contractframework.MergeResultPlansByContract(out), records), nil
}

type settlementItemKey struct {
	contract string
	id       int64
}

func AddMissingGasResultPlans(plans []ResultPlan, records []ExecutionRecord) []ResultPlan {
	out := contractframework.MergeResultPlansByContract(plans)
	coveredItems := make(map[settlementItemKey]struct{})
	planByContract := make(map[string]int)
	for i := range out {
		planByContract[out[i].Contract] = i
		for _, itemID := range out[i].ItemIDs {
			coveredItems[settlementItemKey{out[i].Contract, itemID}] = struct{}{}
		}
	}
	for _, record := range records {
		if !record.RequiresResult || executionRecordItemsCovered(record, coveredItems) {
			continue
		}
		address := record.Contract.MustEncode()
		i, ok := planByContract[address]
		if !ok {
			i = len(out)
			planByContract[address] = i
			scope := contractframework.ResultInputScopeAllContractUTXOs
			if record.Status != ResultStatusSuccess && len(record.FundingInputs) != 0 {
				scope = contractframework.ResultInputScopeExplicit
			}
			out = append(out, ResultPlan{Contract: address, Height: record.Height, InputScope: scope})
		}
		if record.Status == ResultStatusSuccess || record.Kind == ExecutionKindTrigger {
			out[i].InputScope = contractframework.ResultInputScopeAllContractUTXOs
		}
		if record.GasFee != nil && record.GasFee.Sign() != 0 {
			out[i].GasFee = contractframework.DecimalAddAllowNil(out[i].GasFee, record.GasFee)
		}
		out[i].Inputs = contractframework.UniqueOutPoints(append(out[i].Inputs, record.FundingInputs...))
		out[i].ItemIDs = appendMissingItemIDs(out[i].ItemIDs, record.ItemIDs)
		if record.ResultFeeMode == contractframework.ResultFeeModeGasAsset {
			if refund := contractframework.ResultGasRefundFromRecord(record); refund.To != "" {
				out[i].GasRefunds = append(out[i].GasRefunds, refund)
			}
		}
	}
	return contractframework.AttachCallFunding(out, records)
}

func executionRecordItemsCovered(record ExecutionRecord, covered map[settlementItemKey]struct{}) bool {
	if len(record.ItemIDs) == 0 {
		return false
	}
	for _, itemID := range record.ItemIDs {
		if _, ok := covered[settlementItemKey{record.Contract.MustEncode(), itemID}]; !ok {
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

	return contractframework.BuildSettlementAssetIntentsByItem(plan,
		templateSettlementResultOptions(nil, nil, nil, assetPrecision))
}

func templateSettlementResultOptions(inputsByItem map[int64][]OutPoint,
	feesByItem map[int64]*scommon.Decimal,
	gasRefundsByItem map[int64]contractframework.ResultGasRefund,
	assetPrecision contractframework.AssetPrecisionResolver) contractframework.SettlementResultOptions {

	return contractframework.SettlementResultOptions{
		SatoshiAssetName: SatoshiAssetName,
		Precision: contractframework.AssetPrecisionPolicy{
			Fallback: MaxPriceDivisibility, Resolve: assetPrecision,
		},
		InvalidAsset: ErrInvalidAsset, InputsByItem: inputsByItem,
		FeesByItem: feesByItem, GasRefundsByItem: gasRefundsByItem,
	}
}

func AugmentResultPlans(plans []ResultPlan, store *RuntimeStore, gasConfig GasConfig,
	provider ContractUTXOProvider,
	assetPrecision contractframework.AssetPrecisionResolver) ([]ResultPlan, error) {

	out := contractframework.MergeResultPlansByContract(plans)
	precision := templateSettlementResultOptions(nil, nil, nil, assetPrecision).Precision
	for i := range out {
		if out[i].ManagedRemainder != nil {
			continue // Finalize already produced this canonical per-contract plan.
		}
		if provider == nil {
			return nil, fmt.Errorf("missing template contract UTXO provider")
		}
		addr, err := DecodeContractAddress(out[i].Contract)
		if err != nil {
			return nil, err
		}
		closed, deployer, err := closedContractChangeRecipient(addr, store)
		if err != nil {
			return nil, err
		}
		cfg := gasConfig.Normalize()
		managed := contractcommon.ManagedBalance{}
		if store != nil {
			if balance, exists := store.ManagedBalance(addr); exists {
				managed = balance.Clone()
			}
			if runtime, ok := store.Get(addr); ok {
				cfg = GasConfigForRuntime(cfg, runtime)
			}
		}
		if closed {
			out[i].InputScope = contractframework.ResultInputScopeAllContractUTXOs
		}
		view, err := contractframework.CollectResultPlanUTXOs(out[i], provider)
		if err != nil {
			return nil, err
		}
		out[i], err = contractframework.AugmentManagedResultPlan(contractframework.ManagedResultRequest{
			Plan: out[i], View: view, Managed: managed,
			Closed: closed, DeployerAddress: deployer, BootstrapAddress: cfg.BootstrapAddress,
			GasAssetName: cfg.GasAssetName, Precision: precision,
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func closedContractChangeRecipient(addr ContractAddress, store *RuntimeStore) (bool, string, error) {
	if store == nil {
		return false, "", nil
	}
	runtime, ok := store.Get(addr)
	if !ok || runtime == nil {
		return false, "", nil
	}
	closed, err := store.ContractClosed(addr)
	return closed, runtime.RuntimeBase().Deployer(), err
}

func BuildCanonicalSettlementResultTx(status ResultStatus, settlementPlans []*SettlementPlan,
	records []ExecutionRecord, resolve ResultRecipientScriptResolver) (*wire.MsgTx, []ResultPlan, error) {

	plans, err := BuildSettlementResultPlans(settlementPlans, records, nil)
	if err != nil {
		return nil, nil, err
	}
	tx, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status: status, Plans: plans, ResolveScript: resolve,
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
