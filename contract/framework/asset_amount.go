package framework

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

var ErrInvalidAsset = errors.New("invalid asset")

type AssetPrecisionResolver func(assetName string) (int, bool)

type AssetPrecisionPolicy struct {
	Fallback int
	Resolve  AssetPrecisionResolver
}

func (p AssetPrecisionPolicy) ParsePrecision() int {
	return p.Fallback
}

func (p AssetPrecisionPolicy) AssetPrecision(assetName string) (int, bool) {
	if assetName == "" || p.Resolve == nil {
		return 0, false
	}
	precision, ok := p.Resolve(assetName)
	return precision, ok && precision >= 0
}

func (p AssetPrecisionPolicy) Normalize(assetName string, amount *scommon.Decimal) *scommon.Decimal {
	if amount == nil {
		return nil
	}
	precision, ok := p.AssetPrecision(assetName)
	if !ok || amount.Precision == precision {
		return amount
	}
	return amount.NewPrecision(precision)
}

func OutputAssetAmount(outpoint OutPoint, value int64, assets wire.TxAssets,
	satoshiAssetName, assetName string, invalidAsset error) (*scommon.Decimal, error) {

	if assetName == "" {
		return nil, invalidAsset
	}
	if assetName == satoshiAssetName {
		if value < 0 {
			return nil, fmt.Errorf("contract output %s has negative value", outpoint)
		}
		return scommon.NewDefaultDecimal(value), nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, invalidAsset
	}
	asset, err := assets.Find(name)
	if err != nil || asset == nil {
		return scommon.NewDefaultDecimal(0), nil
	}
	return asset.Amount.Clone(), nil
}

func SatoshiAmountUint64(value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("satoshi amount is negative")
	}
	return uint64(value), nil
}

func NewAssetSetWithPrecision(assetName, amount string, maxPrecision int, invalidAsset error) (wire.TxAssets, error) {
	return NewAssetSetWithAssetPrecision(assetName, amount, maxPrecision, invalidAsset, nil)
}

func NewAssetSetWithAssetPrecision(assetName, amount string, maxPrecision int,
	invalidAsset error, assetPrecision AssetPrecisionResolver) (wire.TxAssets, error) {

	return NewAssetSetWithPrecisionPolicy(assetName, amount, AssetPrecisionPolicy{
		Fallback: maxPrecision,
		Resolve:  assetPrecision,
	}, invalidAsset)
}

func NewAssetSetWithPrecisionPolicy(assetName, amount string, policy AssetPrecisionPolicy,
	invalidAsset error) (wire.TxAssets, error) {

	if assetName == "" || amount == "" || amount == "0" {
		return nil, nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		if invalidAsset != nil {
			return nil, invalidAsset
		}
		return nil, ErrInvalidAsset
	}
	amt, err := scommon.NewDecimalFromString(amount, policy.ParsePrecision())
	if err != nil {
		return nil, err
	}
	amt = policy.Normalize(assetName, amt)
	if amt.Sign() <= 0 {
		return nil, nil
	}
	return wire.TxAssets{{
		Name:   *name,
		Amount: *amt,
	}}, nil
}
