package evm

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultOutput struct {
	To        string
	Value     uint64
	Assets    wire.TxAssets
	ExtraData []byte
}

func resultOutputWithAsset(to, assetName string, amount *scommon.Decimal) (ResultOutput, error) {
	output := ResultOutput{To: to}
	if err := output.AddAsset(assetName, amount); err != nil {
		return ResultOutput{}, err
	}
	return output, nil
}

func (o *ResultOutput) AddAsset(assetName string, amount *scommon.Decimal) error {
	if amount == nil || amount.IsZero() {
		return nil
	}
	if assetName == SatoshiAssetName {
		sats, err := decimalToUint64(*amount)
		if err != nil {
			return err
		}
		next, overflow := addUint64(o.Value, sats)
		if overflow {
			return fmt.Errorf("satoshi output amount overflows uint64")
		}
		o.Value = next
		return nil
	}
	if wire.NewAssetNameFromString(assetName) == nil {
		return fmt.Errorf("invalid asset name %q", assetName)
	}
	builder := scommon.NewTxAssetsBuilder(len(o.Assets) + 1)
	builder.AddSlice(o.Assets)
	builder.AddClone(&wire.AssetInfo{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *amount.Clone(),
	})
	o.Assets = builder.Build()
	return nil
}

func cloneResultOutput(output ResultOutput) ResultOutput {
	return ResultOutput{
		To:        output.To,
		Value:     output.Value,
		Assets:    output.Assets.Clone(),
		ExtraData: cloneBytes(output.ExtraData),
	}
}

func normalizeResultOutput(output ResultOutput) ResultOutput {
	normalized := cloneResultOutput(output)
	if len(normalized.Assets) == 0 {
		return normalized
	}
	builder := scommon.NewTxAssetsBuilder(len(normalized.Assets))
	for _, asset := range normalized.Assets {
		builder.AddClone(&asset)
	}
	normalized.Assets = builder.Build()
	return normalized
}
