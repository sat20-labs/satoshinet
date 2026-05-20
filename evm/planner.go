package evm

import (
	"errors"
	"fmt"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultPlanRequest struct {
	Contract               ContractAddress
	Available              []UTXO
	Intents                []AssetIntent
	GasAssetName           string
	GasFee                 uint64
	RequiredGasFundingUTXO []OutPoint
}

type ResultPlan struct {
	Inputs  []UTXO
	Outputs []ResultOutput
}

func BuildCanonicalResultPlan(req ResultPlanRequest) (ResultPlan, error) {
	if req.GasAssetName == "" {
		return ResultPlan{}, ErrInvalidAsset
	}
	if err := validateIntentContracts(req.Contract, req.Intents); err != nil {
		return ResultPlan{}, err
	}

	requiredByAsset := make(map[string]*scommon.Decimal)
	if req.GasFee > 0 {
		requiredByAsset[req.GasAssetName] = scommon.NewDefaultDecimal(int64(req.GasFee))
	}
	for _, intent := range req.Intents {
		if intent.AssetName == "" || intent.Amount == nil {
			return ResultPlan{}, ErrInvalidAsset
		}
		if existing, ok := requiredByAsset[intent.AssetName]; ok {
			requiredByAsset[intent.AssetName] = existing.AddAlignPrecision(intent.Amount)
		} else {
			requiredByAsset[intent.AssetName] = intent.Amount.Clone()
		}
	}

	assets := sortedAssetNames(requiredByAsset, req.GasAssetName)
	inputs := make([]UTXO, 0)
	selected := make(map[OutPoint]UTXO)
	for _, assetName := range assets {
		required := requiredByAsset[assetName]
		if required == nil || required.IsZero() {
			continue
		}
		covered, err := totalSelectedAsset(selected, assetName)
		if err != nil {
			return ResultPlan{}, err
		}
		if covered.Cmp(required) >= 0 {
			continue
		}
		available := filterUnselectedUTXOs(req.Available, selected)
		selection, err := SelectCanonicalInputs(CanonicalSelectionRequest{
			Contract:      req.Contract,
			AssetName:     assetName,
			Required:      required.SubAlignPrecision(covered),
			Available:     available,
			RequiredFirst: requiredFirstForAsset(assetName, req.GasAssetName, req.RequiredGasFundingUTXO),
		})
		if err != nil {
			return ResultPlan{}, err
		}
		for _, input := range selection.Inputs {
			if _, ok := selected[input.OutPoint]; ok {
				continue
			}
			selected[input.OutPoint] = input
			inputs = append(inputs, input)
		}
	}
	changeBuilder := newResultOutputAccumulator()
	spentByAsset, err := totalSelectedAssets(selected)
	if err != nil {
		return ResultPlan{}, err
	}
	for assetName, spent := range spentByAsset {
		required := requiredByAsset[assetName]
		if required == nil || spent.Cmp(required) <= 0 {
			continue
		}
		if err := changeBuilder.Add(req.Contract.MustEncode(), assetName, spent.SubAlignPrecision(required), nil); err != nil {
			return ResultPlan{}, err
		}
	}

	intentBuilder := newResultOutputAccumulator()
	outputs := make([]ResultOutput, 0, len(req.Intents))
	for _, intent := range req.Intents {
		if len(intent.ExtraData) != 0 {
			output, err := resultOutputWithAsset(intent.To, intent.AssetName, intent.Amount)
			if err != nil {
				return ResultPlan{}, err
			}
			output.ExtraData = cloneBytes(intent.ExtraData)
			outputs = append(outputs, output)
			continue
		}
		if err := intentBuilder.Add(intent.To, intent.AssetName, intent.Amount, nil); err != nil {
			return ResultPlan{}, err
		}
	}
	outputs = append(outputs, intentBuilder.Outputs()...)
	outputs = append(outputs, changeBuilder.Outputs()...)
	return ResultPlan{Inputs: inputs, Outputs: outputs}, nil
}

func validateIntentContracts(contract ContractAddress, intents []AssetIntent) error {
	for _, intent := range intents {
		if !intent.From.Equal(contract) {
			return errors.New("result plan cannot mix contracts")
		}
	}
	return nil
}

func sortedAssetNames(required map[string]*scommon.Decimal, gasAssetName string) []string {
	assets := make([]string, 0, len(required))
	for assetName := range required {
		assets = append(assets, assetName)
	}
	sort.Strings(assets)
	if gasAssetName != "" {
		for i, assetName := range assets {
			if assetName == gasAssetName {
				copy(assets[1:i+1], assets[0:i])
				assets[0] = assetName
				break
			}
		}
	}
	return assets
}

func requiredFirstForAsset(assetName, gasAssetName string, funding []OutPoint) []OutPoint {
	if assetName != gasAssetName {
		return nil
	}
	return funding
}

func addUint64(a, b uint64) (uint64, bool) {
	sum := a + b
	return sum, sum < a
}

type resultOutputAccumulator struct {
	order []string
	items map[string]ResultOutput
}

func newResultOutputAccumulator() *resultOutputAccumulator {
	return &resultOutputAccumulator{
		order: make([]string, 0),
		items: make(map[string]ResultOutput),
	}
}

func (a *resultOutputAccumulator) Add(to, assetName string, amount *scommon.Decimal, extraData []byte) error {
	if amount == nil || amount.IsZero() {
		return nil
	}
	key := to + "\x00" + string(extraData)
	output, ok := a.items[key]
	if !ok {
		output = ResultOutput{
			To:        to,
			ExtraData: cloneBytes(extraData),
		}
		a.order = append(a.order, key)
	}
	if err := output.AddAsset(assetName, amount); err != nil {
		return err
	}
	a.items[key] = output
	return nil
}

func (a *resultOutputAccumulator) Outputs() []ResultOutput {
	if len(a.order) == 0 {
		return nil
	}
	outputs := make([]ResultOutput, 0, len(a.order))
	for _, key := range a.order {
		outputs = append(outputs, normalizeResultOutput(a.items[key]))
	}
	return outputs
}

func resultOutputAssetsEqual(left, right wire.TxAssets) bool {
	return (&left).Equal(right)
}

func resultOutputHasSingleAsset(output ResultOutput, assetName string, amount *scommon.Decimal) bool {
	if assetName == SatoshiAssetName {
		sats, err := decimalToUint64(*amount)
		return err == nil && output.Value == sats && len(output.Assets) == 0
	}
	if output.Value != 0 || len(output.Assets) != 1 {
		return false
	}
	return output.Assets[0].Name.String() == assetName &&
		output.Assets[0].Amount.Cmp(amount) == 0
}

func filterUnselectedUTXOs(utxos []UTXO, selected map[OutPoint]UTXO) []UTXO {
	available := make([]UTXO, 0, len(utxos))
	for _, u := range utxos {
		if _, ok := selected[u.OutPoint]; ok {
			continue
		}
		available = append(available, u)
	}
	return available
}

func totalSelectedAsset(selected map[OutPoint]UTXO, assetName string) (*scommon.Decimal, error) {
	total := zeroDecimal()
	for _, u := range selected {
		amount, err := u.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(amount)
	}
	return total, nil
}

func totalSelectedAssets(selected map[OutPoint]UTXO) (map[string]*scommon.Decimal, error) {
	total := make(map[string]*scommon.Decimal)
	for _, u := range selected {
		if total[SatoshiAssetName] == nil {
			total[SatoshiAssetName] = zeroDecimal()
		}
		sats, err := decimalFromUint64(u.Value)
		if err != nil {
			return nil, fmt.Errorf("asset %s amount: %w", SatoshiAssetName, err)
		}
		total[SatoshiAssetName] = total[SatoshiAssetName].AddAlignPrecision(sats)
		for _, asset := range u.Assets {
			if total[asset.Name.String()] == nil {
				total[asset.Name.String()] = zeroDecimal()
			}
			total[asset.Name.String()] = total[asset.Name.String()].AddAlignPrecision(&asset.Amount)
		}
	}
	return total, nil
}
