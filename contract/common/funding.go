package common

import (
	"fmt"

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
