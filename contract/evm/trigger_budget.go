package evm

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

// Trigger execution uses the same quantity view as an ordinary call. Earlier
// fees/refunds are already reserved by pendingFeeAssetView; earlier transfer
// intents are subtracted here for admission and by Runtime during execution.
// The returned reserve therefore contains only this trigger's maximum fee.
func (e *Backend) triggerGasBudget(contract ContractAddress, gasLimit int64) (*scommon.Decimal, bool, error) {
	cfg := e.GasConfig.Normalize()
	reserve, err := contractframework.RecordResultGasFee(cfg, e.settlementPrecision, ExecutionRecord{
		Kind: ExecutionKindTrigger, Height: int64(e.Block.Number), GasUsed: gasLimit,
	})
	if err != nil {
		return nil, false, err
	}
	if e.Runtime == nil || e.Runtime.AssetBalances == nil {
		return nil, false, fmt.Errorf("missing managed asset view")
	}
	available, err := (pendingIntentAssetBalanceView{
		Base: e.Runtime.AssetBalances, Prior: e.Runtime.AssetIntents,
	}).AssetBalance(ContractAddressHash(contract), cfg.GasAssetName)
	if err != nil {
		return nil, false, err
	}
	return reserve, available.Cmp(reserve) >= 0, nil
}

type triggerReservedAssetView struct {
	base      AssetBalanceReader
	owner     EVMAddress
	assetName string
	reserve   *scommon.Decimal
}

func (v triggerReservedAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if v.base == nil {
		return nil, fmt.Errorf("missing managed asset view")
	}
	amount, err := v.base.AssetBalance(owner, assetName)
	if err != nil || amount == nil || owner != v.owner || assetName != v.assetName {
		return amount, err
	}
	available := amount.SubAlignPrecision(v.reserve)
	if available.Sign() < 0 {
		return nil, fmt.Errorf("%w: trigger fee exceeds managed balance", contractframework.ErrAccountingInvariant)
	}
	return available, nil
}
