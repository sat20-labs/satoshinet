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

type ResultGasRefund struct {
	CallID             string           `json:"callId,omitempty"`
	To                 string           `json:"to,omitempty"`
	Inputs             []OutPoint       `json:"inputs,omitempty"`
	GasFee             *scommon.Decimal `json:"gasFee,omitempty"`
	RetainedGasFunding *scommon.Decimal `json:"retainedGasFunding,omitempty"`
}

// ResultInputScope specifies which physical inputs are eligible for selection.
// AllContractUTXOs makes the whole address available, without forcing a sweep. A
// refund-only plan can spend just its call funding without rewriting the
// existing contract balance. This is transaction construction, not persisted
// managed/unmanaged UTXO classification.
type ResultInputScope byte

const (
	ResultInputScopeAllContractUTXOs ResultInputScope = iota
	ResultInputScopeExplicit
)

type ResultPlan struct {
	Contract    string            `json:"contract,omitempty"`
	ResultCount int               `json:"resultCount,omitempty"`
	Height      int64             `json:"height,omitempty"`
	ItemIDs     []int64           `json:"itemIds,omitempty"`
	GasFee      *scommon.Decimal  `json:"gasFee,omitempty"`
	SatoshiFee  int64             `json:"satoshiFee,omitempty"`
	GasRefunds  []ResultGasRefund `json:"gasRefunds,omitempty"`
	InputScope  ResultInputScope  `json:"inputScope,omitempty"`
	Inputs      []OutPoint        `json:"inputs,omitempty"`
	CallFunding []OutPoint        `json:"callFunding,omitempty"`
	// RefundFunding is the current failed calls' escrow. It is never added to
	// the persistent managed balance and is used only to construct this Result.
	RefundFunding []OutPoint     `json:"refundFunding,omitempty"`
	InputUTXOs    []UTXO         `json:"inputUtxos,omitempty"`
	FeeOutputs    []ResultOutput `json:"feeOutputs,omitempty"`
	Outputs       []ResultOutput `json:"outputs,omitempty"`
	// Set only by the framework's quantity settlement. This execution-local
	// value is the remainder before profit distribution, not a UTXO registry.
	ManagedRemainder *contract.ManagedBalance `json:"-"`
}

func CloneResultPlans(plans []ResultPlan) []ResultPlan {
	out := make([]ResultPlan, len(plans))
	for i := range plans {
		out[i] = CloneResultPlan(plans[i])
	}
	return out
}

// MergeResultPlansByContract collapses all result work for one contract into a
// single plan while preserving the first-seen contract and output order.
func MergeResultPlansByContract(plans []ResultPlan) []ResultPlan {
	out := make([]ResultPlan, 0, len(plans))
	index := make(map[string]int, len(plans))
	for _, plan := range plans {
		if plan.Contract == "" {
			cloned := CloneResultPlan(plan)
			if cloned.ResultCount == 0 {
				cloned.ResultCount = 1
			}
			out = append(out, cloned)
			continue
		}
		i, ok := index[plan.Contract]
		if !ok {
			index[plan.Contract] = len(out)
			cloned := CloneResultPlan(plan)
			if cloned.ResultCount == 0 {
				cloned.ResultCount = 1
			}
			out = append(out, cloned)
			continue
		}
		merged := &out[i]
		count := plan.ResultCount
		if count == 0 {
			count = 1
		}
		merged.ResultCount += count
		if merged.Height == 0 {
			merged.Height = plan.Height
		}
		merged.ItemIDs = appendUniqueInt64s(merged.ItemIDs, plan.ItemIDs)
		merged.GasFee = DecimalAddAllowNil(merged.GasFee, plan.GasFee)
		merged.SatoshiFee += plan.SatoshiFee
		merged.GasRefunds = append(merged.GasRefunds, CloneResultGasRefunds(plan.GasRefunds)...)
		if merged.InputScope != plan.InputScope {
			merged.InputScope = ResultInputScopeAllContractUTXOs
		}
		merged.Inputs = UniqueOutPoints(append(merged.Inputs, plan.Inputs...))
		merged.CallFunding = UniqueOutPoints(append(merged.CallFunding, plan.CallFunding...))
		merged.RefundFunding = UniqueOutPoints(append(merged.RefundFunding, plan.RefundFunding...))
		merged.InputUTXOs = appendUniqueUTXOs(merged.InputUTXOs, plan.InputUTXOs)
		merged.FeeOutputs = append(merged.FeeOutputs, CloneResultOutputs(plan.FeeOutputs)...)
		merged.Outputs = append(merged.Outputs, CloneResultOutputs(plan.Outputs)...)
		// A merged business plan must be accounted as a whole before it is
		// committed. Finalized per-contract plans are produced after this merge.
		merged.ManagedRemainder = nil
	}
	return out
}

func appendUniqueInt64s(dst, src []int64) []int64 {
	seen := make(map[int64]struct{}, len(dst)+len(src))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range src {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func appendUniqueUTXOs(dst, src []UTXO) []UTXO {
	seen := make(map[OutPoint]struct{}, len(dst)+len(src))
	for _, input := range dst {
		seen[input.OutPoint] = struct{}{}
	}
	for _, input := range src {
		if _, ok := seen[input.OutPoint]; ok {
			continue
		}
		seen[input.OutPoint] = struct{}{}
		dst = append(dst, input.Clone())
	}
	return dst
}

func CloneResultPlan(plan ResultPlan) ResultPlan {
	out := plan
	out.GasFee = CloneDecimal(plan.GasFee)
	out.ItemIDs = append([]int64(nil), plan.ItemIDs...)
	out.GasRefunds = CloneResultGasRefunds(plan.GasRefunds)
	out.Inputs = append([]OutPoint(nil), plan.Inputs...)
	out.CallFunding = append([]OutPoint(nil), plan.CallFunding...)
	out.RefundFunding = append([]OutPoint(nil), plan.RefundFunding...)
	if plan.ManagedRemainder != nil {
		balance := plan.ManagedRemainder.Clone()
		out.ManagedRemainder = &balance
	}
	out.InputUTXOs = make([]UTXO, len(plan.InputUTXOs))
	for i := range plan.InputUTXOs {
		out.InputUTXOs[i] = plan.InputUTXOs[i].Clone()
	}
	out.FeeOutputs = make([]ResultOutput, len(plan.FeeOutputs))
	for i := range plan.FeeOutputs {
		out.FeeOutputs[i] = CloneResultOutput(plan.FeeOutputs[i])
	}
	out.Outputs = make([]ResultOutput, len(plan.Outputs))
	for i := range plan.Outputs {
		out.Outputs[i] = CloneResultOutput(plan.Outputs[i])
	}
	return out
}

func CloneResultGasRefunds(in []ResultGasRefund) []ResultGasRefund {
	out := make([]ResultGasRefund, len(in))
	copy(out, in)
	for i := range out {
		out[i].Inputs = append([]OutPoint(nil), in[i].Inputs...)
		out[i].GasFee = CloneDecimal(in[i].GasFee)
		out[i].RetainedGasFunding = CloneDecimal(in[i].RetainedGasFunding)
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
	normalized.Assets = normalizeTxAssets(normalized.Assets)
	return normalized
}

func NormalizeResultOutputPrecision(output ResultOutput, policy AssetPrecisionPolicy) ResultOutput {
	normalized := NormalizeResultOutput(output)
	if !policy.Enabled() {
		return normalized
	}
	normalized.Assets = NormalizeAssetSetPrecision(normalized.Assets, policy)
	if normalized.AssetName != "" && normalized.AssetName != contract.SatoshiAssetName {
		name := wire.NewAssetNameFromString(normalized.AssetName)
		if name != nil {
			asset, err := normalized.Assets.Find(name)
			if err == nil && asset != nil && asset.Amount.Sign() > 0 {
				normalized.AssetAmt = asset.Amount.String()
			} else {
				normalized.AssetAmt = ""
			}
		}
	}
	return normalized
}

func NormalizeResultOutputsPrecision(outputs []ResultOutput, policy AssetPrecisionPolicy) []ResultOutput {
	if len(outputs) == 0 {
		return nil
	}
	out := make([]ResultOutput, 0, len(outputs))
	for _, output := range outputs {
		normalized := NormalizeResultOutputPrecision(output, policy)
		if ResultOutputIsZero(normalized) {
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func ResultOutputIsZero(output ResultOutput) bool {
	return output.Value == 0 && len(output.Assets) == 0
}

func CompactResultOutputs(outputs []ResultOutput) ([]ResultOutput, error) {
	out := make([]ResultOutput, 0, len(outputs))
	indices := make(map[string][]int)
	for _, output := range outputs {
		output = NormalizeResultOutput(output)
		if ResultOutputIsZero(output) {
			continue
		}
		key, ok := compactResultOutputKey(output)
		if !ok {
			out = append(out, output)
			continue
		}

		merged := false
		for _, existing := range indices[key] {
			candidate := CloneResultOutput(out[existing])
			value, overflow := AddInt64(candidate.Value, output.Value)
			if overflow {
				return nil, fmt.Errorf("compacted result output value overflows int64")
			}
			candidate.Value = value
			if len(output.Assets) != 0 {
				builder := scommon.NewTxAssetsBuilder(len(candidate.Assets) + len(output.Assets))
				builder.AddSlice(candidate.Assets)
				builder.AddSlice(output.Assets)
				candidate.Assets = builder.Build()
				for _, asset := range candidate.Assets {
					if err := ValidateAssetDecimal(asset.Amount); err != nil {
						return nil, fmt.Errorf("compacted result asset %s: %w", asset.Name.String(), err)
					}
				}
			}
			carrier, err := resultAssetCarrierSats(candidate.Assets)
			if err != nil {
				return nil, err
			}
			if candidate.Value < carrier {
				continue
			}
			out[existing] = candidate
			merged = true
			break
		}
		if merged {
			continue
		}
		indices[key] = append(indices[key], len(out))
		out = append(out, output)
	}
	return out, nil
}


func compactResultOutputKey(output ResultOutput) (string, bool) {
	if len(output.ExtraData) != 0 || output.AssetName != "" || output.AssetAmt != "" || output.Reason != "" {
		return "", false
	}
	return output.To, true
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
	if err := amount.Validate(); err != nil {
		return err
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
