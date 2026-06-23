package framework

import (
	"fmt"
	"math/big"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	DefaultDeployerProfitBPS  = int64(6000)
	DefaultBootstrapProfitBPS = int64(4000)
	TotalProfitBPS            = int64(10000)
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
	ManagedGasPaid   bool
	DeployerAddress  string
	BootstrapAddress string
	ManagedMode      ResultSurplusMode
	SurplusMode      ResultSurplusMode
}

func AugmentResultPlanWithManagedState(req ManagedResultAugmentRequest) (ResultPlan, error) {
	out := CloneResultPlan(req.Plan)
	out.Inputs = append([]OutPoint(nil), req.View.Inputs...)
	out.InputUTXOs = nil

	outputs := CloneResultOutputs(out.Outputs)
	remainingValue, remainingAssets, err := physicalRemainderAfterOutputs(req.View, outputs, req.GasAssetName, req.GasFee)
	if err != nil {
		return ResultPlan{}, err
	}

	managedAssets := requestManagedAssets(req)
	if !req.ManagedGasPaid {
		remainingGasFee, err := deductGasFeeFromRetain(&managedAssets, req.GasAssetName, req.GasFee)
		if err != nil {
			return ResultPlan{}, err
		}
		if remainingGasFee != nil && remainingGasFee.Sign() > 0 {
			return ResultPlan{}, fmt.Errorf("insufficient managed gas asset for result fee")
		}
	}
	managedAssets = capResultOutputByAvailable(managedAssets, remainingValue, remainingAssets)
	if !ResultOutputIsZero(managedAssets) {
		outputs = append(outputs, splitSurplusOutput(managedAssets, managedSurplusRequest(req))...)
	}

	spentValue := resultOutputsValue(outputs)
	if req.GasAssetName == contract.SatoshiAssetName && req.GasFee != nil && req.GasFee.Sign() > 0 {
		feeValue, err := DecimalToInt64(*req.GasFee)
		if err != nil {
			return ResultPlan{}, err
		}
		spentValue += feeValue
	}
	if spentValue > req.View.Value {
		return ResultPlan{}, fmt.Errorf("contract result outputs spend %d sats but only %d sats are available",
			spentValue, req.View.Value)
	}

	surplusValue := req.View.Value - spentValue
	surplusAssets := req.View.Assets.Clone()
	spentAssets, err := resultOutputsAssets(outputs)
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
		Assets: normalizeAssets(surplusAssets),
	}
	outputs = append(outputs, splitSurplusOutput(surplus, req)...)
	out.Outputs = CompactResultOutputs(outputs)
	out.Inputs = UniqueOutPoints(out.Inputs)
	return out, nil
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
	spentValue := resultOutputsValue(outputs)
	if gasAssetName == contract.SatoshiAssetName && gasFee != nil && gasFee.Sign() > 0 {
		feeValue, err := DecimalToInt64(*gasFee)
		if err != nil {
			return 0, nil, err
		}
		spentValue += feeValue
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

func capResultOutputByAvailable(output ResultOutput, availableValue int64, availableAssets wire.TxAssets) ResultOutput {
	output = NormalizeResultOutput(output)
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
		assets = append(assets, next)
	}
	output.Assets = normalizeAssets(assets)
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

func splitSurplusOutput(surplus ResultOutput, req ManagedResultAugmentRequest) []ResultOutput {
	surplus = NormalizeResultOutput(surplus)
	if ResultOutputIsZero(surplus) {
		return nil
	}
	if req.SurplusMode != ResultSurplusAsProfit || req.DeployerAddress == "" ||
		req.BootstrapAddress == "" || req.DeployerAddress == req.BootstrapAddress {

		return []ResultOutput{surplus}
	}

	deployerOut := ResultOutput{To: req.DeployerAddress}
	bootstrapOut := ResultOutput{To: req.BootstrapAddress}
	deployerOut.Value = surplus.Value * DefaultDeployerProfitBPS / TotalProfitBPS
	bootstrapOut.Value = surplus.Value - deployerOut.Value
	deployerOut.Assets, bootstrapOut.Assets = SplitAssetsByBPS(surplus.Assets, DefaultDeployerProfitBPS)

	out := make([]ResultOutput, 0, 2)
	if !ResultOutputIsZero(deployerOut) {
		out = append(out, NormalizeResultOutput(deployerOut))
	}
	if !ResultOutputIsZero(bootstrapOut) {
		out = append(out, NormalizeResultOutput(bootstrapOut))
	}
	return out
}

func resultOutputsValue(outputs []ResultOutput) int64 {
	var value int64
	for _, output := range outputs {
		value += output.Value
	}
	return value
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
	if len(assets) == 0 {
		return nil
	}
	builder := scommon.NewTxAssetsBuilder(len(assets))
	builder.AddSlice(assets)
	return builder.Build()
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
		deployerAmt := asset.Amount.MulBigInt(big.NewInt(deployerBPS)).DivBigInt(big.NewInt(TotalProfitBPS))
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
	return normalizeAssets(deployer), normalizeAssets(bootstrap)
}
