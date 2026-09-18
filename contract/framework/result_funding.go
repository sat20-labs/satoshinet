package framework

import (
	"errors"
		"fmt"
	"math/big"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func resultOutputFromIntent(intent AssetIntent, amount *scommon.Decimal) (ResultOutput, error) {
	output, err := ResultOutputWithAsset(intent.To, intent.AssetName, amount)
	if err != nil || intent.BindingSat == 0 {
		return output, err
	}
	if len(output.Assets) != 1 {
		return ResultOutput{}, fmt.Errorf("binding metadata requires a token refund")
	}
	output.Assets[0].BindingSat = intent.BindingSat
	output.Value, err = resultAssetCarrierSats(output.Assets)
	return output, err
}

// Business intents specify plain sats and token quantities. Restore binding
// metadata and the sats carrying those tokens before physical accounting.
// Outputs already carrying binding metadata are physical outputs, not intents.
func bindResultOutputs(outputs []ResultOutput, available wire.TxAssets) ([]ResultOutput, error) {
	bindings := make(map[wire.AssetName]uint32, len(available))
	for _, asset := range available {
		bindings[asset.Name] = asset.BindingSat
	}
	out := CloneResultOutputs(outputs)
	for i := range out {
		for j := range out[i].Assets {
			asset := &out[i].Assets[j]
			binding, found := bindings[asset.Name]
			if !found {
				return nil, fmt.Errorf("%w: result asset %s is not available", ErrAccountingInvariant, asset.Name.String())
			}
			if asset.BindingSat != 0 {
				if asset.BindingSat != binding {
					return nil, fmt.Errorf("%w: result asset binding mismatch", ErrAccountingInvariant)
				}
				continue
			}
			asset.BindingSat = binding
			carrier, err := resultAssetCarrierSats(wire.TxAssets{*asset})
			if err != nil {
				return nil, err
			}
			value, overflow := AddInt64(out[i].Value, carrier)
			if overflow {
				return nil, fmt.Errorf("result carrier sats overflow")
			}
			out[i].Value = value
		}
	}
	return out, nil
}

// resultAssetCarrierSats follows SatoshiNet L2 partial-binding semantics:

// only complete BindingSat groups occupy sats. A non-zero remainder is

// allowed to remain unbound on L2 and therefore contributes no carrier sat.

func resultAssetCarrierSats(assets wire.TxAssets) (int64, error) {
	total := new(big.Int)
	for _, asset := range assets {
		if asset.BindingSat == 0 || asset.Amount.Sign() == 0 {
			continue
		}
		amount := asset.Amount.NewPrecision(0).Value
		units := new(big.Int).Quo(amount, new(big.Int).SetUint64(uint64(asset.BindingSat)))
		total.Add(total, units)
	}
	if !total.IsInt64() {
		return 0, fmt.Errorf("result carrier sats overflow")
	}
	return total.Int64(), nil
}

// selectResultFunding adapts the SDK's SelectUtxosForAsset_SatsNet/fee selection
// to a pure consensus function: cover each asset, reuse multi-asset inputs, and
// stop once funded. Prefer a single fitting UTXO, then larger inputs to avoid
// sweeping dust. Canonical outpoint order breaks ties and orders the Result.
// Selection never assigns managed/unmanaged ownership to an outpoint.
func selectResultFunding(view ResultPlanUTXOView, outputs []ResultOutput,
	gasAsset string, gasFee *scommon.Decimal, satoshiFee int64, mandatory []OutPoint) (ResultPlanUTXOView, error) {

	value, err := resultOutputsValue(outputs)
	if err != nil {
		return ResultPlanUTXOView{}, err
	}
	assets, err := resultOutputsAssets(outputs)
	if err != nil {
		return ResultPlanUTXOView{}, err
	}
	value, overflow := AddInt64(value, satoshiFee)
	if overflow {
		return ResultPlanUTXOView{}, fmt.Errorf("result funding value overflow")
	}
	if gasFee != nil && gasFee.Sign() > 0 {
		if gasAsset == contract.SatoshiAssetName {
			fee, err := DecimalToInt64(*gasFee)
			if err != nil {
				return ResultPlanUTXOView{}, err
			}
			value, overflow = AddInt64(value, fee)
			if overflow {
				return ResultPlanUTXOView{}, fmt.Errorf("result fee value overflow")
			}
		} else {
			fee, err := NewAssetSet(gasAsset, gasFee)
			if err != nil {
				return ResultPlanUTXOView{}, err
			}
			if err := assets.Merge(fee); err != nil {
				return ResultPlanUTXOView{}, err
			}
		}
	}
	available := append([]UTXO(nil), view.UTXOs...)
	SortUTXOsForCanonicalSelection(available)
	byInput := make(map[OutPoint]UTXO, len(available))
	for _, utxo := range available {
		byInput[utxo.OutPoint] = utxo
	}
	used := make(map[OutPoint]bool)
	total := contract.ManagedBalance{}
	var selected []UTXO
	add := func(utxo UTXO) error {
		if used[utxo.OutPoint] {
			return nil
		}
		if err := total.Credit(utxo.PhysicalValue(), utxo.TxAssets()); err != nil {
			return err
		}
		used[utxo.OutPoint] = true
		selected = append(selected, utxo)
		return nil
	}
	for _, input := range UniqueOutPoints(mandatory) {
		utxo, ok := byInput[input]
		if !ok {
			return ResultPlanUTXOView{}, fmt.Errorf("required input %s not available", input)
		}
		if err := add(utxo); err != nil {
			return ResultPlanUTXOView{}, err
		}
	}
	choose := func(required *scommon.Decimal, amount func(UTXO) (*scommon.Decimal, error)) error {
		type candidate struct {
			utxo   UTXO
			amount *scommon.Decimal
		}
		var candidates []candidate
		for _, utxo := range available {
			if used[utxo.OutPoint] {
				continue
			}
			amt, err := amount(utxo)
			if err != nil {
				return err
			}
			if amt.Sign() > 0 {
				candidates = append(candidates, candidate{utxo, amt})
			}
		}
		// A single sufficient input takes precedence over accumulating dust.
		best := -1
		for i, item := range candidates {
			if item.amount.Cmp(required) >= 0 && (best < 0 || item.amount.Cmp(candidates[best].amount) < 0) {
				best = i
			}
		}
		if best >= 0 {
			return add(candidates[best].utxo)
		}
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].amount.Cmp(candidates[j].amount) > 0 })
		remaining := required.Clone()
		for _, item := range candidates {
			if err := add(item.utxo); err != nil {
				return err
			}
			remaining = remaining.SubAlignPrecision(item.amount)
			if remaining.Sign() <= 0 {
				return nil
			}
		}
		return ErrInsufficientFunds
	}
	for _, asset := range assets {
		name := asset.Name.String()
		current, err := total.AssetAmount(name)
		if err != nil {
			return ResultPlanUTXOView{}, err
		}
		need := asset.Amount.SubAlignPrecision(current)
		if need.Sign() <= 0 {
			continue
		}
		if err := choose(need, func(utxo UTXO) (*scommon.Decimal, error) {
			if utxo.TxOutput != nil {
				for _, held := range utxo.TxOutput.OutValue.Assets {
					if held.Name == asset.Name {
						return held.Amount.Clone(), nil
					}
				}
			}
			return ZeroDecimal(), nil
		}); err != nil {
			return ResultPlanUTXOView{}, fmt.Errorf("result funding %s: %w", name, err)
		}
	}
	changeAssets := total.Assets.Clone()
	if err := changeAssets.Split(assets); err != nil {
		return ResultPlanUTXOView{}, err
	}
	carrier, err := resultAssetCarrierSats(changeAssets)
	if err != nil {
		return ResultPlanUTXOView{}, err
	}
	requiredValue, overflow := AddInt64(value, carrier)
	if overflow {
		return ResultPlanUTXOView{}, fmt.Errorf("result change value overflow")
	}
	covered := func() (bool, error) {
		change := total.Assets.Clone()
		if err := change.Split(assets); err != nil {
			return false, err
		}
		carrier, err := resultAssetCarrierSats(change)
		if err != nil {
			return false, err
		}
		required, overflow := AddInt64(value, carrier)
		if overflow {
			return false, fmt.Errorf("result change value overflow")
		}
		return total.Value >= required, nil
	}
	if requiredValue > total.Value {
		err := choose(scommon.NewDefaultDecimal(requiredValue-total.Value), func(utxo UTXO) (*scommon.Decimal, error) {
			if utxo.TxOutput == nil {
				return ZeroDecimal(), nil
			}
			balance := contract.ManagedBalance{Value: utxo.TxOutput.OutValue.Value, Assets: utxo.TxOutput.OutValue.Assets}
			return balance.AssetAmount(contract.SatoshiAssetName)
		})
		if err != nil && !errors.Is(err, ErrInsufficientFunds) {
			return ResultPlanUTXOView{}, fmt.Errorf("result satoshi funding: %w", err)
		}
	}
	ready, err := covered()
	if err != nil {
		return ResultPlanUTXOView{}, err
	}
	// Adding an asset-bearing input may make the aggregate change cross a
	// BindingSat boundary. Recheck after every additional input.
	for _, utxo := range available {
		if ready {
			break
		}
		if used[utxo.OutPoint] || utxo.TxOutput == nil {
			continue
		}
		balance := contract.ManagedBalance{Value: utxo.TxOutput.OutValue.Value, Assets: utxo.TxOutput.OutValue.Assets}
		plain, err := balance.AssetAmount(contract.SatoshiAssetName)
		if err != nil {
			return ResultPlanUTXOView{}, err
		}
		if plain.Sign() <= 0 {
			continue
		}
		if err := add(utxo); err != nil {
			return ResultPlanUTXOView{}, err
		}
		ready, err = covered()
		if err != nil {
			return ResultPlanUTXOView{}, err
		}
	}
	if !ready {
		return ResultPlanUTXOView{}, fmt.Errorf("result satoshi funding: %w", ErrInsufficientFunds)
	}
	SortUTXOsForCanonicalSelection(selected)
	out := ResultPlanUTXOView{Contract: view.Contract, Value: total.Value, Assets: total.Assets}
	for _, utxo := range selected {
		out.UTXOs = append(out.UTXOs, utxo.Clone())
		out.Inputs = append(out.Inputs, utxo.OutPoint)
	}
	return out, nil
}
