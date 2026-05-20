package evm

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
)

type CanonicalSelectionRequest struct {
	Contract      ContractAddress
	AssetName     string
	Required      *scommon.Decimal
	Available     []UTXO
	RequiredFirst []OutPoint
}

type CanonicalSelection struct {
	Inputs []UTXO
	Total  *scommon.Decimal
	Change *scommon.Decimal
}

func SelectCanonicalInputs(req CanonicalSelectionRequest) (CanonicalSelection, error) {
	if req.AssetName == "" || req.Required == nil {
		return CanonicalSelection{}, ErrInvalidAsset
	}

	selected := make([]UTXO, 0)
	total := zeroDecimal()
	used := make(map[string]struct{})

	for _, out := range req.RequiredFirst {
		u, ok := findUTXO(req.Available, out)
		if !ok {
			return CanonicalSelection{}, fmt.Errorf("required input %s not available", out)
		}
		if !u.Contract.Equal(req.Contract) {
			return CanonicalSelection{}, fmt.Errorf("required input %s belongs to a different contract", out)
		}
		amount, err := u.AssetAmount(req.AssetName)
		if err != nil {
			return CanonicalSelection{}, err
		}
		if amount.IsZero() {
			return CanonicalSelection{}, fmt.Errorf("required input %s asset mismatch", out)
		}
		key := out.String()
		if _, exists := used[key]; exists {
			continue
		}
		used[key] = struct{}{}
		selected = append(selected, u)
		total = total.AddAlignPrecision(amount)
	}

	candidates := make([]UTXO, 0, len(req.Available))
	for _, u := range req.Available {
		if !u.Contract.Equal(req.Contract) {
			continue
		}
		amount, err := u.AssetAmount(req.AssetName)
		if err != nil {
			return CanonicalSelection{}, err
		}
		if amount.IsZero() {
			continue
		}
		if _, exists := used[u.OutPoint.String()]; exists {
			continue
		}
		candidates = append(candidates, u)
	}
	SortUTXOsForCanonicalSelection(candidates)

	for _, u := range candidates {
		if total.Cmp(req.Required) >= 0 {
			break
		}
		amount, err := u.AssetAmount(req.AssetName)
		if err != nil {
			return CanonicalSelection{}, err
		}
		selected = append(selected, u)
		total = total.AddAlignPrecision(amount)
	}
	if total.Cmp(req.Required) < 0 {
		return CanonicalSelection{}, ErrInsufficientFunds
	}
	return CanonicalSelection{
		Inputs: selected,
		Total:  total,
		Change: total.SubAlignPrecision(req.Required),
	}, nil
}

func findUTXO(utxos []UTXO, out OutPoint) (UTXO, bool) {
	for _, u := range utxos {
		if u.OutPoint == out {
			return u, true
		}
	}
	return UTXO{}, false
}
