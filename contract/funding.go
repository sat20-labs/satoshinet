package contract

import (
	"bytes"
	"fmt"
	"sort"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type FundingValidation struct {
	RequireFunding bool
	ValidateAmount func(indexercommon.Decimal) error
}

func ValidateFundingTxOut(funding wire.TxOut, opts FundingValidation) error {
	if funding.Value < 0 {
		return fmt.Errorf("funding value must not be negative")
	}
	if opts.RequireFunding && funding.Value == 0 && len(funding.Assets) == 0 {
		return fmt.Errorf("funding must contain satoshi or asset amount")
	}
	for _, asset := range funding.Assets {
		assetName := asset.Name.String()
		if assetName == "" || assetName == SatoshiAssetName {
			return fmt.Errorf("invalid funding asset name %q", assetName)
		}
		if asset.Amount.Value == nil {
			return fmt.Errorf("funding asset %s amount is nil", assetName)
		}
		if asset.Amount.IsZero() {
			return fmt.Errorf("funding asset %s amount is zero", assetName)
		}
		if asset.Amount.Sign() < 0 {
			return fmt.Errorf("funding asset %s amount is negative", assetName)
		}
		if opts.ValidateAmount != nil {
			if err := opts.ValidateAmount(asset.Amount); err != nil {
				return fmt.Errorf("funding asset %s amount: %w", assetName, err)
			}
		}
	}
	return nil
}

type FundingUTXO struct {
	OutPoint wire.OutPoint
	OutValue wire.TxOut
	Height   int64
	SortKey  string
}

func (u FundingUTXO) Clone() FundingUTXO {
	return FundingUTXO{
		OutPoint: u.OutPoint,
		OutValue: cloneTxOut(&u.OutValue),
		Height:   u.Height,
		SortKey:  u.SortKey,
	}
}

type FundingSelectionRequest struct {
	Available        []FundingUTXO
	RequiredValue    int64
	RequiredAssets   wire.TxAssets
	ChangePkScript   []byte
	RequiredPkScript []byte
	IsSpendable      func(FundingUTXO) bool
}

type FundingSelection struct {
	Inputs       []FundingUTXO
	TotalValue   int64
	TotalAssets  wire.TxAssets
	ChangeOutput *wire.TxOut
}

func (s FundingSelection) InputOutPoints() []wire.OutPoint {
	out := make([]wire.OutPoint, 0, len(s.Inputs))
	for _, input := range s.Inputs {
		out = append(out, input.OutPoint)
	}
	return out
}

func (s FundingSelection) WitnessUtxos() []*wire.TxOut {
	out := make([]*wire.TxOut, 0, len(s.Inputs))
	for _, input := range s.Inputs {
		cp := cloneTxOut(&input.OutValue)
		out = append(out, &cp)
	}
	return out
}

func SelectFundingUTXOs(req FundingSelectionRequest) (FundingSelection, error) {
	if req.RequiredValue < 0 {
		return FundingSelection{}, fmt.Errorf("required value must not be negative")
	}
	requiredAssets := req.RequiredAssets.Clone()
	if err := validateRequiredAssets(requiredAssets); err != nil {
		return FundingSelection{}, err
	}

	candidates := filterFundingCandidates(req)
	sortFundingCandidates(candidates)

	selected := make([]FundingUTXO, 0)
	selectedOutpoints := make(map[wire.OutPoint]struct{})
	totalValue := int64(0)
	totalAssets := wire.TxAssets{}
	for !fundingSatisfied(totalValue, totalAssets, req.RequiredValue, requiredAssets) {
		progress := false
		for _, utxo := range candidates {
			if _, ok := selectedOutpoints[utxo.OutPoint]; ok {
				continue
			}
			if !fundingContributes(utxo, totalValue, totalAssets, req.RequiredValue, requiredAssets) {
				continue
			}
			if utxo.OutValue.Value < 0 || totalValue+utxo.OutValue.Value < totalValue {
				return FundingSelection{}, fmt.Errorf("funding value overflows")
			}
			totalValue += utxo.OutValue.Value
			if err := totalAssets.Merge(utxo.OutValue.Assets); err != nil {
				return FundingSelection{}, err
			}
			selectedOutpoints[utxo.OutPoint] = struct{}{}
			selected = append(selected, utxo.Clone())
			progress = true
			break
		}
		if !progress {
			break
		}
	}
	if !fundingSatisfied(totalValue, totalAssets, req.RequiredValue, requiredAssets) {
		return FundingSelection{}, fmt.Errorf("no enough contract funding UTXO")
	}

	changeValue := totalValue - req.RequiredValue
	changeAssets := totalAssets.Clone()
	if err := changeAssets.Split(requiredAssets); err != nil {
		return FundingSelection{}, err
	}
	changeOutput, err := fundingChangeOutput(changeValue, changeAssets, req.ChangePkScript)
	if err != nil {
		return FundingSelection{}, err
	}

	return FundingSelection{
		Inputs:       selected,
		TotalValue:   totalValue,
		TotalAssets:  totalAssets.Clone(),
		ChangeOutput: changeOutput,
	}, nil
}

func filterFundingCandidates(req FundingSelectionRequest) []FundingUTXO {
	out := make([]FundingUTXO, 0, len(req.Available))
	seen := make(map[wire.OutPoint]struct{})
	for _, utxo := range req.Available {
		if _, ok := seen[utxo.OutPoint]; ok {
			continue
		}
		seen[utxo.OutPoint] = struct{}{}
		if len(req.RequiredPkScript) != 0 && !bytes.Equal(utxo.OutValue.PkScript, req.RequiredPkScript) {
			continue
		}
		if req.IsSpendable != nil && !req.IsSpendable(utxo) {
			continue
		}
		out = append(out, utxo.Clone())
	}
	return out
}

func sortFundingCandidates(utxos []FundingUTXO) {
	sort.SliceStable(utxos, func(i, j int) bool {
		if utxos[i].Height != utxos[j].Height {
			return utxos[i].Height < utxos[j].Height
		}
		if utxos[i].OutValue.Value != utxos[j].OutValue.Value {
			return utxos[i].OutValue.Value < utxos[j].OutValue.Value
		}
		left := utxos[i].SortKey
		if left == "" {
			left = utxos[i].OutPoint.String()
		}
		right := utxos[j].SortKey
		if right == "" {
			right = utxos[j].OutPoint.String()
		}
		return left < right
	})
}

func fundingSatisfied(value int64, assets wire.TxAssets, requiredValue int64, requiredAssets wire.TxAssets) bool {
	if value < requiredValue {
		return false
	}
	for _, required := range requiredAssets {
		current, err := assets.Find(&required.Name)
		if err != nil || current.Amount.Cmp(&required.Amount) < 0 {
			return false
		}
	}
	return true
}

func fundingContributes(utxo FundingUTXO, totalValue int64, totalAssets wire.TxAssets, requiredValue int64, requiredAssets wire.TxAssets) bool {
	for _, required := range requiredAssets {
		current := indexercommon.NewDefaultDecimal(0)
		if asset, err := totalAssets.Find(&required.Name); err == nil {
			current = asset.Amount.Clone()
		}
		if current.Cmp(&required.Amount) >= 0 {
			continue
		}
		asset, err := utxo.OutValue.Assets.Find(&required.Name)
		if err == nil && asset.Amount.Sign() > 0 {
			return true
		}
	}
	if totalValue < requiredValue && utxo.OutValue.Value > 0 && fundingAssetsSatisfied(totalAssets, requiredAssets) {
		return true
	}
	return len(requiredAssets) == 0 && requiredValue == 0
}

func fundingAssetsSatisfied(assets wire.TxAssets, requiredAssets wire.TxAssets) bool {
	for _, required := range requiredAssets {
		current, err := assets.Find(&required.Name)
		if err != nil || current.Amount.Cmp(&required.Amount) < 0 {
			return false
		}
	}
	return true
}

func fundingChangeOutput(value int64, assets wire.TxAssets, pkScript []byte) (*wire.TxOut, error) {
	if value < 0 {
		return nil, fmt.Errorf("funding value is insufficient")
	}
	if len(assets) == 0 && value == 0 {
		return nil, nil
	}
	if len(pkScript) == 0 {
		return nil, fmt.Errorf("missing funding change script")
	}
	if value < assets.GetBindingSatAmout() {
		return nil, fmt.Errorf("funding change value is too small for bound assets")
	}
	return wire.NewTxOut(value, assets, cloneBytes(pkScript)), nil
}

func validateRequiredAssets(assets wire.TxAssets) error {
	for _, asset := range assets {
		if asset.Name.String() == "" || asset.Name.String() == SatoshiAssetName {
			return fmt.Errorf("invalid funding asset name %q", asset.Name.String())
		}
		if asset.Amount.Sign() <= 0 {
			return fmt.Errorf("funding asset %s amount must be positive", asset.Name.String())
		}
	}
	return nil
}

func cloneTxOut(output *wire.TxOut) wire.TxOut {
	if output == nil {
		return wire.TxOut{}
	}
	return wire.TxOut{
		Value:    output.Value,
		Assets:   output.Assets.Clone(),
		PkScript: cloneBytes(output.PkScript),
	}
}
