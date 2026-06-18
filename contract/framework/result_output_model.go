package framework

import (
	"fmt"
	"math"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractUTXOProvider func(contract contract.ContractAddress) ([]UTXO, error)

type ResultRecipientScriptResolver func(output ResultOutput) ([]byte, error)

type ResultOutputResolver func(resultTx *wire.MsgTx) ([]ResultOutput, error)

type ResultOutput struct {
	To        string        `json:"to"`
	Value     int64         `json:"value,omitempty"`
	AssetName string        `json:"assetName,omitempty"`
	AssetAmt  string        `json:"assetAmt,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	Assets    wire.TxAssets `json:"assets,omitempty"`
	ExtraData []byte        `json:"extraData,omitempty"`
}

type ResultPlan struct {
	Contract   string           `json:"contract,omitempty"`
	Height     int64            `json:"height,omitempty"`
	ItemIDs    []int64          `json:"itemIds,omitempty"`
	GasFee     *scommon.Decimal `json:"gasFee,omitempty"`
	Inputs     []OutPoint       `json:"inputs,omitempty"`
	InputUTXOs []UTXO           `json:"inputUtxos,omitempty"`
	Outputs    []ResultOutput   `json:"outputs,omitempty"`
}

func CloneResultPlans(plans []ResultPlan) []ResultPlan {
	out := make([]ResultPlan, len(plans))
	for i := range plans {
		out[i] = CloneResultPlan(plans[i])
	}
	return out
}

func CloneResultPlan(plan ResultPlan) ResultPlan {
	out := plan
	out.GasFee = CloneDecimal(plan.GasFee)
	out.ItemIDs = append([]int64(nil), plan.ItemIDs...)
	out.Inputs = append([]OutPoint(nil), plan.Inputs...)
	out.InputUTXOs = make([]UTXO, len(plan.InputUTXOs))
	for i := range plan.InputUTXOs {
		out.InputUTXOs[i] = plan.InputUTXOs[i].Clone()
	}
	out.Outputs = make([]ResultOutput, len(plan.Outputs))
	for i := range plan.Outputs {
		out.Outputs[i] = CloneResultOutput(plan.Outputs[i])
	}
	return out
}

func ResultPlansHaveOutputs(plans []ResultPlan) bool {
	for _, plan := range plans {
		if len(plan.Outputs) != 0 {
			return true
		}
	}
	return false
}

type ResultTxBuildRequest struct {
	Status        contract.ResultStatus
	ResultCount   uint16
	Plans         []ResultPlan
	ResolveScript ResultRecipientScriptResolver
}

func ResultOutputWithAsset(to, assetName string, amount *scommon.Decimal) (ResultOutput, error) {
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
	if assetName == contract.SatoshiAssetName {
		sats, err := DecimalToInt64(*amount)
		if err != nil {
			return err
		}
		next, overflow := AddInt64(o.Value, sats)
		if overflow {
			return fmt.Errorf("satoshi output amount overflows int64")
		}
		o.Value = next
		return nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return fmt.Errorf("invalid asset name %q", assetName)
	}
	builder := scommon.NewTxAssetsBuilder(len(o.Assets) + 1)
	builder.AddSlice(o.Assets)
	builder.AddClone(&wire.AssetInfo{
		Name:   *name,
		Amount: *amount.Clone(),
	})
	o.Assets = builder.Build()
	return nil
}

func CloneResultOutput(output ResultOutput) ResultOutput {
	return ResultOutput{
		To:        output.To,
		Value:     output.Value,
		AssetName: output.AssetName,
		AssetAmt:  output.AssetAmt,
		Reason:    output.Reason,
		Assets:    output.Assets.Clone(),
		ExtraData: CloneBytes(output.ExtraData),
	}
}

func CloneResultOutputs(outputs []ResultOutput) []ResultOutput {
	out := make([]ResultOutput, len(outputs))
	for i := range outputs {
		out[i] = CloneResultOutput(outputs[i])
	}
	return out
}

func NormalizeResultOutput(output ResultOutput) ResultOutput {
	normalized := CloneResultOutput(output)
	if len(normalized.Assets) == 0 {
		normalized.Assets = nil
		return normalized
	}
	builder := scommon.NewTxAssetsBuilder(len(normalized.Assets))
	for _, asset := range normalized.Assets {
		builder.AddClone(&asset)
	}
	normalized.Assets = builder.Build()
	return normalized
}

func ResultOutputIsZero(output ResultOutput) bool {
	return output.Value == 0 && len(output.Assets) == 0
}

func CompactResultOutputs(outputs []ResultOutput) []ResultOutput {
	out := make([]ResultOutput, 0, len(outputs))
	for _, output := range outputs {
		if !ResultOutputIsZero(output) {
			out = append(out, output)
		}
	}
	return out
}

func UniqueOutPoints(in []OutPoint) []OutPoint {
	out := make([]OutPoint, 0, len(in))
	seen := make(map[OutPoint]bool)
	for _, input := range in {
		if seen[input] {
			continue
		}
		seen[input] = true
		out = append(out, input)
	}
	return out
}

func DecimalToInt64(amount scommon.Decimal) (int64, error) {
	if err := ValidateAssetDecimal(amount); err != nil {
		return 0, err
	}
	if amount.Precision != 0 {
		return 0, fmt.Errorf("fractional asset amounts are not supported")
	}
	if !amount.Value.IsInt64() {
		return 0, fmt.Errorf("asset amount overflows int64")
	}
	return amount.Value.Int64(), nil
}

func ValidateAssetDecimal(amount scommon.Decimal) error {
	if amount.Value == nil {
		return fmt.Errorf("nil decimal value")
	}
	if amount.Sign() < 0 {
		return fmt.Errorf("negative amount")
	}
	return nil
}

func AddInt64(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, true
	}
	return a + b, false
}
