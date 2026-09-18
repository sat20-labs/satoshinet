package evm

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

// managedFundingAssetView leaves gas escrow under the caller's ownership until
// ClaimCurrentFunding explicitly accepts it. ReadFundingAssetAmount still shows
// how much can be claimed; ReadAssetBalance only shows what may be spent. This
// prevents an intent and the automatic gas refund from spending the same token.
type managedFundingAssetView struct {
	base    AssetBalanceReader
	overlay AssetBalanceReader
	funding *FundingAssetView
	target  EVMAddress
}

func newManagedFundingAssetView(base AssetBalanceReader, funding *FundingAssetView,
	target EVMAddress) AssetBalanceReader {

	return managedFundingAssetView{
		base: base, overlay: NewFundingOverlayAssetBalanceView(base, funding, target),
		funding: funding, target: target,
	}
}

func (v managedFundingAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if v.funding == nil || owner != v.target || assetName == "" || assetName != v.funding.GasAssetName {
		return v.overlay.AssetBalance(owner, assetName)
	}
	available := zeroDecimal()
	if v.base != nil {
		amount, err := v.base.AssetBalance(owner, assetName)
		if err != nil {
			return nil, err
		}
		if amount != nil {
			available = amount.Clone()
		}
		if marker, ok := v.base.(interface{ IncludesFundingOutputs() bool }); ok && marker.IncludesFundingOutputs() {
			totalFunding, err := totalFundingAssetAmount(v.funding, assetName)
			if err != nil {
				return nil, err
			}
			available = available.SubAlignPrecision(totalFunding)
			if available.Sign() < 0 {
				return nil, fmt.Errorf("funding overlay exceeds its base gas balance")
			}
		}
	}
	return available.AddAlignPrecision(v.funding.ClaimedAssetAmount(assetName)), nil
}

// totalFundingAssetAmount returns the physical amount carried by the current
// funding outputs before any gas reservation is deducted. It is used only to
// remove those same outputs from a physical view that already includes them.
func totalFundingAssetAmount(funding *FundingAssetView, assetName string) (*scommon.Decimal, error) {
	if funding == nil {
		return zeroDecimal(), nil
	}
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	total := zeroDecimal()
	if assetName == SatoshiAssetName {
		for _, output := range funding.Outputs {
			if plain := output.PlainValue(); plain > 0 {
				total = total.AddAlignPrecision(scommon.NewDefaultDecimal(plain))
			}
		}
		return total, nil
	}
	for _, output := range funding.Outputs {
		amount, err := output.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		if amount != nil && amount.Sign() > 0 {
			total = total.AddAlignPrecision(amount)
		}
	}
	return total, nil
}

// pendingFeeAssetView reserves earlier executions' fees and gas refunds while
// work transactions are still awaiting the block's canonical Result. Business
// intents are reserved separately by pendingIntentAssetBalanceView in Runtime.
type pendingFeeAssetView struct {
	base    AssetBalanceReader
	backend *Backend
}

func (v pendingFeeAssetView) IncludesFundingOutputs() bool { return false }

func (v pendingFeeAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	amount, err := v.base.AssetBalance(owner, assetName)
	if err != nil {
		return nil, err
	}
	available := contractframework.CloneDecimal(amount)
	if v.backend == nil || assetName != v.backend.GasConfig.Normalize().GasAssetName {
		return available, nil
	}
	if reserved := v.backend.pendingGasReserved[owner]; reserved != nil {
		available = available.SubAlignPrecision(reserved)
	}
	if available.Sign() < 0 {
		return nil, fmt.Errorf("%w: pending fees exceed managed gas", contractframework.ErrAccountingInvariant)
	}
	return available, nil
}

// Reserve fees/refunds once per accepted outcome. The funding transaction is
// already available during execution; no historical UTXO scan is necessary.
func (e *Backend) reservePendingGas(record ExecutionRecord) error {
	if !record.RequiresResult || record.ResultFeeMode != ResultFeeModeGasAsset ||
		(record.Status != ResultStatusSuccess && record.Kind != ExecutionKindTrigger) {
		return nil
	}
	cfg := e.GasConfig.Normalize()
	reserved, err := contractframework.RecordResultGasFee(cfg, e.settlementPrecision, record)
	if err != nil {
		return err
	}
	if record.GasRefundRecipient != "" && len(record.FundingInputs) != 0 {
		var funding []UTXO
		if e.fundingTx == nil {
			return fmt.Errorf("%w: missing current funding transaction", contractframework.ErrAccountingInvariant)
		}
		fundingTxID := record.TxID
		if fundingTxID == "" && len(record.FundingInputs) != 0 {
			fundingTxID = record.FundingInputs[0].TxID
		}
		if fundingTxID == "" {
			return fmt.Errorf("%w: missing current funding txid", contractframework.ErrAccountingInvariant)
		}
		for _, input := range record.FundingInputs {
			if input.TxID != fundingTxID || uint64(input.Vout) >= uint64(len(e.fundingTx.TxOut)) {
				return fmt.Errorf("%w: invalid current funding input", contractframework.ErrAccountingInvariant)
			}
			funding = append(funding, contractframework.UTXOFromTxOutput(input, record.Contract,
				record.Height, e.fundingTx.TxOut[input.Vout]))
		}
		refund, err := contractframework.RecordGasRefund(record, funding, cfg.GasAssetName, reserved)
		if err != nil {
			return err
		}
		if refund != nil {
			reserved = reserved.AddAlignPrecision(refund.Amount)
		}
	}
	if e.pendingGasReserved == nil {
		e.pendingGasReserved = make(map[EVMAddress]*scommon.Decimal)
	}
	owner := ContractAddressHash(record.Contract)
	e.pendingGasReserved[owner] = contractframework.DecimalAddAllowNil(e.pendingGasReserved[owner], reserved)
	return nil
}
