package framework

import (
	"fmt"
	"math/big"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	DefaultDeployerProfitBPS  = contract.DeployerProfitBPS
	DefaultBootstrapProfitBPS = contract.BootstrapProfitBPS
	TotalProfitBPS            = contract.TotalProfitBPS
)

type ResultSurplusMode byte

const (
	ResultSurplusToBootstrap ResultSurplusMode = iota
	ResultSurplusAsProfit
	ResultSurplusToContract
)

type ManagedResultAugmentRequest struct {
	Plan ResultPlan
	// View is the physical asset set currently held by the contract address.
	View ResultPlanUTXOView
	// ManagedAssets is the asset set the contract still considers managed when
	// the result plan is built. The default augmenter caps it by the physical
	// contract-address remainder after planned settlement outputs and gas.
	ManagedAssets ResultOutput
	// ManagedRemainder is kept for older callers. New code should pass
	// ManagedAssets to make the physical-vs-managed boundary explicit.
	ManagedRemainder ResultOutput
	GasAssetName     string
	GasFee           *scommon.Decimal
	Precision        AssetPrecisionPolicy
	ManagedGasPaid   bool
	DeployerAddress  string
	BootstrapAddress string
	ManagedMode      ResultSurplusMode
	SurplusMode      ResultSurplusMode
}

func AugmentResultPlanWithManagedState(req ManagedResultAugmentRequest) (ResultPlan, error) {
	out := CloneResultPlan(req.Plan)
	out.Inputs = append([]OutPoint(nil), req.View.Inputs...)
	out.InputUTXOs = make([]UTXO, len(req.View.UTXOs))
	for i := range req.View.UTXOs {
		out.InputUTXOs[i] = req.View.UTXOs[i].Clone()
	}

	outputs := NormalizeResultOutputsPrecision(out.Outputs, req.Precision)
	feeOutputs := NormalizeResultOutputsPrecision(out.FeeOutputs, req.Precision)
	gasRefundOutputs, err := resultGasRefundOutputs(out, req.View, req.GasAssetName, req.Precision)
	if err != nil {
		return ResultPlan{}, err
	}
	outputs = append(outputs, gasRefundOutputs...)
	plannedSpends := append(CloneResultOutputs(outputs), feeOutputs...)
	remainingValue, remainingAssets, err := physicalRemainderAfterOutputs(req.View, plannedSpends, req.GasAssetName, req.GasFee)
	if err != nil {
		return ResultPlan{}, err
	}

	managedAssets := requestManagedAssets(req)
	if !req.ManagedGasPaid {
		_, err := deductGasFeeFromRetain(&managedAssets, req.GasAssetName, req.GasFee)
		if err != nil {
			return ResultPlan{}, err
		}
	}
	managedAssets = capResultOutputByAvailable(managedAssets, remainingValue, remainingAssets, req.Precision)
	if !ResultOutputIsZero(managedAssets) {
		split, err := splitSurplusOutput(managedAssets, managedSurplusRequest(req))
		if err != nil {
			return ResultPlan{}, err
		}
		outputs = append(outputs, split...)
	}

	outputValue, err := resultOutputsValue(outputs)
	if err != nil {
		return ResultPlan{}, err
	}
	feeOutputValue, err := resultOutputsValue(feeOutputs)
	if err != nil {
		return ResultPlan{}, err
	}
	spentValue, overflow := AddInt64(outputValue, feeOutputValue)
	if overflow {
		return ResultPlan{}, fmt.Errorf("contract result output value overflows int64")
	}
	if req.GasAssetName == contract.SatoshiAssetName && req.GasFee != nil && req.GasFee.Sign() > 0 {
		feeValue, err := DecimalToInt64(*req.GasFee)
		if err != nil {
			return ResultPlan{}, err
		}
		spentValue, overflow = AddInt64(spentValue, feeValue)
		if overflow {
			return ResultPlan{}, fmt.Errorf("contract result output and fee value overflows int64")
		}
	}
	if spentValue > req.View.Value {
		return ResultPlan{}, fmt.Errorf("contract result outputs spend %d sats but only %d sats are available",
			spentValue, req.View.Value)
	}

	surplusValue := req.View.Value - spentValue
	surplusAssets := req.View.Assets.Clone()
	spentAssets, err := resultOutputsAssets(append(CloneResultOutputs(outputs), feeOutputs...))
	if err != nil {
		return ResultPlan{}, err
	}
	if len(spentAssets) != 0 {
		if err := surplusAssets.Split(spentAssets); err != nil {
			return ResultPlan{}, fmt.Errorf("contract result outputs exceed available assets: %w, outputs=%v available=%v",
				err, spentAssets, req.View.Assets)
		}
	}
	if req.GasAssetName != "" && req.GasAssetName != contract.SatoshiAssetName &&
		req.GasFee != nil && req.GasFee.Sign() > 0 {

		gasAssets, err := NewAssetSet(req.GasAssetName, req.GasFee)
		if err != nil {
			return ResultPlan{}, err
		}
		if err := surplusAssets.Split(gasAssets); err != nil {
			return ResultPlan{}, fmt.Errorf("insufficient gas asset for result fee: %w", err)
		}
	}

	surplus := ResultOutput{
		To:     surplusRecipient(req),
		Value:  surplusValue,
		Assets: NormalizeAssetSetPrecision(surplusAssets, req.Precision),
	}
	split, err := splitSurplusOutput(surplus, req)
	if err != nil {
		return ResultPlan{}, err
	}
	outputs = append(outputs, split...)
	out.Outputs, err = CompactResultOutputs(outputs)
	if err != nil {
		return ResultPlan{}, err
	}
	out.Inputs = UniqueOutPoints(out.Inputs)
	return out, nil
}

func resultGasRefundOutputs(plan ResultPlan, view ResultPlanUTXOView, gasAssetName string,
	policy AssetPrecisionPolicy) ([]ResultOutput, error) {

	if len(plan.GasRefunds) == 0 {
		return nil, nil
	}
	contractAddr, err := contract.DecodeContractAddress(plan.Contract)
	if err != nil {
		return nil, err
	}
	outputs := make([]ResultOutput, 0, len(plan.GasRefunds))
	for _, refund := range plan.GasRefunds {
		intent, err := ResultGasRefundIntent(refund, contractAddr, view.UTXOs, gasAssetName)
		if err != nil {
			return nil, err
		}
		if intent == nil {
			continue
		}
		output, err := ResultOutputWithAsset(intent.To, intent.AssetName, intent.Amount)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, NormalizeResultOutputPrecision(output, policy))
	}
	return outputs, nil
}

func requestManagedAssets(req ManagedResultAugmentRequest) ResultOutput {
	if !ResultOutputIsZero(req.ManagedAssets) {
		return NormalizeResultOutput(req.ManagedAssets)
	}
	return NormalizeResultOutput(req.ManagedRemainder)
}

func physicalRemainderAfterOutputs(view ResultPlanUTXOView, outputs []ResultOutput, gasAssetName string,
	gasFee *scommon.Decimal) (int64, wire.TxAssets, error) {

	remainingValue := view.Value
	spentValue, err := resultOutputsValue(outputs)
	if err != nil {
		return 0, nil, err
	}
	if gasAssetName == contract.SatoshiAssetName && gasFee != nil && gasFee.Sign() > 0 {
		feeValue, err := DecimalToInt64(*gasFee)
		if err != nil {
			return 0, nil, err
		}
		var overflow bool
		spentValue, overflow = AddInt64(spentValue, feeValue)
		if overflow {
			return 0, nil, fmt.Errorf("result output and fee value overflows int64")
		}
	}
	if spentValue > remainingValue {
		return 0, nil, fmt.Errorf("contract result outputs spend %d sats but only %d sats are available",
			spentValue, view.Value)
	}
	remainingValue -= spentValue

	remainingAssets := view.Assets.Clone()
	spentAssets, err := resultOutputsAssets(outputs)
	if err != nil {
		return 0, nil, err
	}
	if len(spentAssets) != 0 {
		if err := remainingAssets.Split(spentAssets); err != nil {
			return 0, nil, fmt.Errorf("contract result outputs exceed available assets: %w, outputs=%v available=%v",
				err, spentAssets, view.Assets)
		}
	}
	if gasAssetName != "" && gasAssetName != contract.SatoshiAssetName &&
		gasFee != nil && gasFee.Sign() > 0 {

		gasAssets, err := NewAssetSet(gasAssetName, gasFee)
		if err != nil {
			return 0, nil, err
		}
		if err := remainingAssets.Split(gasAssets); err != nil {
			return 0, nil, fmt.Errorf("insufficient gas asset for result fee: %w", err)
		}
	}
	return remainingValue, normalizeAssets(remainingAssets), nil
}

func capResultOutputByAvailable(output ResultOutput, availableValue int64, availableAssets wire.TxAssets,
	policy AssetPrecisionPolicy) ResultOutput {

	output = NormalizeResultOutput(output)
	if policy.Enabled() {
		output.Assets = NormalizeManagedAssetSetPrecision(output.Assets, policy)
	}
	if output.Value > availableValue {
		output.Value = availableValue
	}
	if len(output.Assets) == 0 || len(availableAssets) == 0 {
		output.Assets = nil
		return output
	}
	assets := make(wire.TxAssets, 0, len(output.Assets))
	for _, asset := range output.Assets {
		available, err := availableAssets.Find(&asset.Name)
		if err != nil || available == nil || available.Amount.Sign() <= 0 {
			continue
		}
		amount := asset.Amount.Clone()
		if amount.Cmp(&available.Amount) > 0 {
			amount = available.Amount.Clone()
		}
		if amount.Sign() <= 0 {
			continue
		}
		next := asset
		next.Amount = *amount
		next.BindingSat = available.BindingSat
		assets = append(assets, next)
	}
	output.Assets = NormalizeAssetSetPrecision(assets, policy)
	return output
}

func managedSurplusRequest(req ManagedResultAugmentRequest) ManagedResultAugmentRequest {
	out := req
	mode := req.ManagedMode
	if mode == ResultSurplusToBootstrap {
		mode = ResultSurplusToContract
	}
	out.SurplusMode = mode
	return out
}

func deductGasFeeFromRetain(retain *ResultOutput, gasAssetName string,
	gasFee *scommon.Decimal) (*scommon.Decimal, error) {

	if retain == nil || gasAssetName == "" || gasFee == nil || gasFee.Sign() <= 0 {
		return CloneDecimal(gasFee), nil
	}
	remaining := gasFee.Clone()
	if gasAssetName == contract.SatoshiAssetName {
		if retain.Value <= 0 {
			return remaining, nil
		}
		feeValue, err := DecimalToInt64(*remaining)
		if err != nil {
			return nil, err
		}
		if retain.Value >= feeValue {
			retain.Value -= feeValue
			return ZeroDecimal(), nil
		}
		remaining = remaining.SubAlignPrecision(scommon.NewDefaultDecimal(retain.Value))
		retain.Value = 0
		return remaining, nil
	}
	gasAssets, err := NewAssetSet(gasAssetName, remaining)
	if err != nil {
		return nil, err
	}
	retainAssets := retain.Assets.Clone()
	if err := retainAssets.Split(gasAssets); err == nil {
		retain.Assets = normalizeAssets(retainAssets)
		return ZeroDecimal(), nil
	}
	name := wire.NewAssetNameFromString(gasAssetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	asset, err := retainAssets.Find(name)
	if err != nil || asset == nil || asset.Amount.Sign() <= 0 {
		return remaining, nil
	}
	remaining = remaining.SubAlignPrecision(asset.Amount.Clone())
	if remaining.Sign() < 0 {
		remaining = ZeroDecimal()
	}
	retainAssets.Split(wire.TxAssets{*asset})
	retain.Assets = normalizeAssets(retainAssets)
	return remaining, nil
}

func surplusRecipient(req ManagedResultAugmentRequest) string {
	switch req.SurplusMode {
	case ResultSurplusToContract:
		return req.Plan.Contract
	case ResultSurplusAsProfit:
		if req.DeployerAddress != "" {
			return req.DeployerAddress
		}
	}
	if req.BootstrapAddress != "" {
		return req.BootstrapAddress
	}
	return req.Plan.Contract
}

func splitSurplusOutput(surplus ResultOutput, req ManagedResultAugmentRequest) ([]ResultOutput, error) {
	surplus = NormalizeResultOutputPrecision(surplus, req.Precision)
	if ResultOutputIsZero(surplus) {
		return nil, nil
	}
	surplus.To = surplusRecipient(req)
	if req.SurplusMode != ResultSurplusAsProfit || req.DeployerAddress == "" ||
		req.BootstrapAddress == "" || req.DeployerAddress == req.BootstrapAddress {

		return []ResultOutput{surplus}, nil
	}

	deployerOut := ResultOutput{To: req.DeployerAddress}
	bootstrapOut := ResultOutput{To: req.BootstrapAddress}
	deployerOut.Assets, bootstrapOut.Assets = SplitAssetsByBPS(surplus.Assets, DefaultDeployerProfitBPS)
	deployerOut = NormalizeResultOutputPrecision(deployerOut, req.Precision)
	bootstrapOut = NormalizeResultOutputPrecision(bootstrapOut, req.Precision)

	deployerCarrier, err := resultAssetCarrierSats(deployerOut.Assets)
	if err != nil {
		return nil, err
	}
	bootstrapCarrier, err := resultAssetCarrierSats(bootstrapOut.Assets)
	if err != nil {
		return nil, err
	}
	totalCarrier, overflow := AddInt64(deployerCarrier, bootstrapCarrier)
	if overflow || totalCarrier > surplus.Value {
		return nil, fmt.Errorf("%w: insufficient carrier sats for profit split: need %d, available %d",
			ErrAccountingInvariant, totalCarrier, surplus.Value)
	}

	plainValue := surplus.Value - totalCarrier
	deployerPlain := splitInt64ByBPS(plainValue, DefaultDeployerProfitBPS)
	bootstrapPlain := plainValue - deployerPlain
	deployerOut.Value = deployerCarrier + deployerPlain
	bootstrapOut.Value = bootstrapCarrier + bootstrapPlain

	out := make([]ResultOutput, 0, 2)
	if !ResultOutputIsZero(deployerOut) {
		out = append(out, deployerOut)
	}
	if !ResultOutputIsZero(bootstrapOut) {
		out = append(out, bootstrapOut)
	}
	return out, nil
}

// SplitContractProfit uses the protocol's single profit policy. Callers must
// supply only profit, after user liabilities and fees have been settled.
func SplitContractProfit(profit ResultOutput, deployer, bootstrap string,
	precision AssetPrecisionPolicy) ([]ResultOutput, error) {

	profit = NormalizeResultOutputPrecision(profit, precision)
	if ResultOutputIsZero(profit) {
		return nil, nil
	}
	if deployer == "" || bootstrap == "" {
		return nil, fmt.Errorf("missing contract profit recipient")
	}
	return splitSurplusOutput(profit, ManagedResultAugmentRequest{
		Precision: precision, DeployerAddress: deployer, BootstrapAddress: bootstrap,
		SurplusMode: ResultSurplusAsProfit,
	})
}

func resultOutputsValue(outputs []ResultOutput) (int64, error) {
	var value int64
	for _, output := range outputs {
		if output.Value < 0 {
			return 0, fmt.Errorf("result output value is negative")
		}
		next, overflow := AddInt64(value, output.Value)
		if overflow {
			return 0, fmt.Errorf("result output value overflows int64")
		}
		value = next
	}
	return value, nil
}

func splitInt64ByBPS(value, bps int64) int64 {
	if value <= 0 || bps <= 0 {
		return 0
	}
	if bps >= TotalProfitBPS {
		return value
	}
	out := new(big.Int).Mul(big.NewInt(value), big.NewInt(bps))
	out.Div(out, big.NewInt(TotalProfitBPS))
	return out.Int64()
}

func resultOutputsAssets(outputs []ResultOutput) (wire.TxAssets, error) {
	var assets wire.TxAssets
	for _, output := range outputs {
		if len(output.Assets) == 0 {
			continue
		}
		if err := assets.Merge(output.Assets); err != nil {
			return nil, err
		}
	}
	return normalizeAssets(assets), nil
}

func normalizeAssets(assets wire.TxAssets) wire.TxAssets {
	return normalizeTxAssets(assets)
}

func SplitAssetsByBPS(assets wire.TxAssets, deployerBPS int64) (wire.TxAssets, wire.TxAssets) {
	if len(assets) == 0 {
		return nil, nil
	}
	if deployerBPS <= 0 {
		return nil, normalizeAssets(assets.Clone())
	}
	if deployerBPS >= TotalProfitBPS {
		return normalizeAssets(assets.Clone()), nil
	}
	deployer := make(wire.TxAssets, 0, len(assets))
	bootstrap := make(wire.TxAssets, 0, len(assets))
	for _, asset := range assets {
		// Split integer smallest units, rounding the deployer's share down.
		// The bootstrap receives the exact remainder, including the final unit.
		units := new(big.Int).Mul(asset.Amount.Value, big.NewInt(deployerBPS))
		units.Quo(units, big.NewInt(TotalProfitBPS))
		remaining := new(big.Int).Sub(asset.Amount.Value, units)
		if units.Sign() > 0 {
			next := asset
			next.Amount = scommon.Decimal{Precision: asset.Amount.Precision, Value: units}
			deployer = append(deployer, next)
		}
		if remaining.Sign() > 0 {
			next := asset
			next.Amount = scommon.Decimal{Precision: asset.Amount.Precision, Value: remaining}
			bootstrap = append(bootstrap, next)
		}
	}
	return normalizeAssets(deployer), normalizeAssets(bootstrap)
}
