package evm

import (
	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

// triggerGasBudget reserves settlement fees and refunds in addition to the next
// call's maximum fee. Runtime already subtracts pending explicit asset intents.
func (e *Backend) triggerGasBudget(contract ContractAddress, gasLimit int64) (*scommon.Decimal, bool, error) {
	if e.ContractUTXOs == nil {
		return nil, true, nil
	}
	cfg := e.GasConfig.Normalize()
	precision := e.settlementPrecision
	reserve, err := contractframework.RecordResultGasFee(cfg, precision, ExecutionRecord{
		Kind: ExecutionKindTrigger, Height: int64(e.Block.Number), GasUsed: gasLimit,
	})
	if err != nil {
		return nil, false, err
	}
	utxos, err := e.ContractUTXOs(contract)
	if err != nil {
		return nil, false, err
	}
	total := zeroDecimal()
	for _, utxo := range utxos {
		if !utxo.Contract.Equal(contract) {
			continue
		}
		amount, err := utxo.AssetAmount(cfg.GasAssetName)
		if err != nil {
			return nil, false, err
		}
		total = total.AddAlignPrecision(amount)
	}
	spent := zeroDecimal()
	for _, record := range e.pending {
		if !record.Contract.Equal(contract) {
			continue
		}
		fee, err := contractframework.RecordResultGasFee(cfg, precision, record)
		if err != nil {
			return nil, false, err
		}
		reserve = reserve.AddAlignPrecision(fee)
		if record.ResultFeeMode != contractframework.ResultFeeModePlainTxFee {
			refund, err := contractframework.RecordGasRefund(record, utxos, cfg.GasAssetName, fee)
			if err != nil {
				return nil, false, err
			}
			if refund != nil {
				reserve = reserve.AddAlignPrecision(precision.Normalize(cfg.GasAssetName, refund.Amount))
			}
		}
		for _, intent := range record.AssetIntents {
			if intent.From.Equal(contract) && intent.AssetName == cfg.GasAssetName && intent.Amount != nil && intent.Amount.Sign() > 0 {
				spent = spent.AddAlignPrecision(precision.Normalize(cfg.GasAssetName, intent.Amount))
			}
		}
	}
	return reserve, total.Cmp(reserve.AddAlignPrecision(spent)) >= 0, nil
}

type triggerReservedAssetView struct {
	base      AssetBalanceReader
	owner     EVMAddress
	assetName string
	reserve   *scommon.Decimal
}

func (v triggerReservedAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	amount, err := v.base.AssetBalance(owner, assetName)
	if err != nil || amount == nil || owner != v.owner || assetName != v.assetName {
		return amount, err
	}
	available := amount.SubAlignPrecision(v.reserve)
	if available.Sign() < 0 {
		return zeroDecimal(), nil
	}
	return available, nil
}
