package framework

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
)

type BusinessFundingSummary struct {
	PlainSat int64
	Assets   map[string]*scommon.Decimal
}

func SummarizeBusinessFunding(outputs []ContractOutput, gasAssetName string) (BusinessFundingSummary, error) {
	summary := BusinessFundingSummary{Assets: make(map[string]*scommon.Decimal)}
	for _, output := range outputs {
		plain := output.PlainValue()
		if plain > 0 && gasAssetName != contract.SatoshiAssetName {
			next, overflow := AddInt64(summary.PlainSat, plain)
			if overflow {
				return BusinessFundingSummary{}, errors.New("plain sat funding overflows")
			}
			summary.PlainSat = next
		}
		for _, asset := range output.TxAssets() {
			name := asset.Name.String()
			if name == "" || name == gasAssetName || asset.Amount.Sign() <= 0 {
				continue
			}
			amount := asset.Amount.Clone()
			if existing := summary.Assets[name]; existing != nil {
				summary.Assets[name] = existing.AddAlignPrecision(amount)
			} else {
				summary.Assets[name] = amount
			}
		}
	}
	return summary, nil
}

func (s BusinessFundingSummary) AssetAmount(assetName string) *scommon.Decimal {
	if assetName == contract.SatoshiAssetName {
		return scommon.NewDefaultDecimal(s.PlainSat)
	}
	if amount := s.Assets[assetName]; amount != nil {
		return amount.Clone()
	}
	return ZeroDecimal()
}

func (s BusinessFundingSummary) SingleAssetAmount() *scommon.Decimal {
	for _, amount := range s.Assets {
		return amount.Clone()
	}
	return nil
}

func (s BusinessFundingSummary) RequirePlainSat(amount int64, label string) error {
	if label == "" {
		label = "funding"
	}
	if amount <= 0 {
		return fmt.Errorf("%s requires positive plain sat amount", label)
	}
	if s.PlainSat < amount {
		return fmt.Errorf("%s plain sat funding %d is less than declared amount %d", label, s.PlainSat, amount)
	}
	return nil
}

func (s BusinessFundingSummary) RequireAsset(assetName string, amount *scommon.Decimal, label string) error {
	if label == "" {
		label = "funding"
	}
	if assetName == "" {
		return fmt.Errorf("%s requires asset name", label)
	}
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("%s requires positive asset amount", label)
	}
	got := s.AssetAmount(assetName)
	if got.Cmp(amount) < 0 {
		return fmt.Errorf("%s asset %s amount %s is less than declared amount %s",
			label, assetName, got.String(), amount.String())
	}
	return nil
}

func (s BusinessFundingSummary) SingleBusinessAmount() (*scommon.Decimal, error) {
	if s.PlainSat > 0 && len(s.Assets) == 0 {
		return scommon.NewDefaultDecimal(s.PlainSat), nil
	}
	if s.PlainSat == 0 && len(s.Assets) == 1 {
		return s.SingleAssetAmount(), nil
	}
	return nil, errors.New("requires exactly one business funding asset")
}

func NonGasFundingRefundIntents(contractAddr contract.ContractAddress, outputs []ContractOutput,
	gasAssetName string, recipient string) ([]AssetIntent, error) {

	if recipient == "" {
		return nil, nil
	}
	intents := make([]AssetIntent, 0)
	for _, output := range outputs {
		if plain := output.PlainValue(); plain > 0 && gasAssetName != contract.SatoshiAssetName {
			intents = append(intents, AssetIntent{
				From:      contractAddr,
				To:        recipient,
				AssetName: contract.SatoshiAssetName,
				Amount:    scommon.NewDefaultDecimal(plain),
			})
		}
		for _, asset := range output.TxAssets() {
			name := asset.Name.String()
			if name == "" || name == gasAssetName || asset.Amount.Sign() <= 0 {
				continue
			}
			intents = append(intents, AssetIntent{
				From:      contractAddr,
				To:        recipient,
				AssetName: name,
				Amount:    asset.Amount.Clone(),
			})
		}
	}
	return intents, nil
}
