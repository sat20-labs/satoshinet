package framework

import (
	"errors"
	"fmt"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

var ErrInsufficientFunds = errors.New("insufficient funds")

type ResultPlanRequest struct {
	Contract               contract.ContractAddress
	Available              []UTXO
	Intents                []AssetIntent
	GasAssetName           string
	GasFee                 *scommon.Decimal
	RequiredGasFundingUTXO []OutPoint
	Precision              AssetPrecisionPolicy
}

type CanonicalSelectionRequest struct {
	Contract      contract.ContractAddress
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

type CanonicalResultPlanner struct {
	GasConfig GasConfig
	UTXOs     ContractUTXOProvider
	Precision AssetPrecisionPolicy
}

func SelectCanonicalInputs(req CanonicalSelectionRequest) (CanonicalSelection, error) {
	if req.AssetName == "" || req.Required == nil {
		return CanonicalSelection{}, ErrInvalidAsset
	}

	selected := make([]UTXO, 0)
	total := ZeroDecimal()
	used := make(map[string]struct{})

	for _, out := range req.RequiredFirst {
		u, ok := FindUTXO(req.Available, out)
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

func FindUTXO(utxos []UTXO, out OutPoint) (UTXO, bool) {
	for _, u := range utxos {
		if u.OutPoint == out {
			return u, true
		}
	}
	return UTXO{}, false
}

func BuildCanonicalResultPlan(req ResultPlanRequest) (ResultPlan, error) {
	if req.GasAssetName == "" {
		return ResultPlan{}, ErrInvalidAsset
	}
	if err := validateIntentContracts(req.Contract, req.Intents); err != nil {
		return ResultPlan{}, err
	}

	requiredByAsset := make(map[string]*scommon.Decimal)
	if req.GasFee != nil && req.GasFee.Sign() > 0 {
		requiredByAsset[req.GasAssetName] = req.Precision.Normalize(req.GasAssetName, req.GasFee.Clone())
	}
	for _, intent := range req.Intents {
		if intent.AssetName == "" || intent.Amount == nil {
			return ResultPlan{}, ErrInvalidAsset
		}
		amount := req.Precision.Normalize(intent.AssetName, intent.Amount.Clone())
		if amount == nil || amount.Sign() <= 0 {
			continue
		}
		if existing, ok := requiredByAsset[intent.AssetName]; ok {
			requiredByAsset[intent.AssetName] = existing.AddAlignPrecision(amount)
		} else {
			requiredByAsset[intent.AssetName] = amount
		}
	}

	assets := sortedAssetNames(requiredByAsset, req.GasAssetName)
	inputs := make([]UTXO, 0)
	selected := make(map[OutPoint]UTXO)
	for _, out := range req.RequiredGasFundingUTXO {
		u, ok := FindUTXO(req.Available, out)
		if !ok {
			return ResultPlan{}, fmt.Errorf("required input %s not available", out)
		}
		if !u.Contract.Equal(req.Contract) {
			return ResultPlan{}, fmt.Errorf("required input %s belongs to a different contract", out)
		}
		if _, exists := selected[u.OutPoint]; exists {
			continue
		}
		selected[u.OutPoint] = u
		inputs = append(inputs, u)
	}
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
			Contract:  req.Contract,
			AssetName: assetName,
			Required:  required.SubAlignPrecision(covered),
			Available: available,
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
		if required == nil {
			required = ZeroDecimal()
		}
		if spent.Cmp(required) <= 0 {
			continue
		}
		if err := changeBuilder.Add(req.Contract.MustEncode(), assetName, spent.SubAlignPrecision(required), nil); err != nil {
			return ResultPlan{}, err
		}
	}

	intentBuilder := newResultOutputAccumulator()
	outputs := make([]ResultOutput, 0, len(req.Intents))
	for _, intent := range req.Intents {
		amount := req.Precision.Normalize(intent.AssetName, intent.Amount.Clone())
		if amount == nil || amount.Sign() <= 0 {
			continue
		}
		if len(intent.ExtraData) != 0 {
			output, err := ResultOutputWithAsset(intent.To, intent.AssetName, amount)
			if err != nil {
				return ResultPlan{}, err
			}
			output.ExtraData = CloneBytes(intent.ExtraData)
			outputs = append(outputs, output)
			continue
		}
		if err := intentBuilder.Add(intent.To, intent.AssetName, amount, nil); err != nil {
			return ResultPlan{}, err
		}
	}
	outputs = append(outputs, intentBuilder.Outputs()...)
	outputs = append(outputs, changeBuilder.Outputs()...)
	outputs = NormalizeResultOutputsPrecision(outputs, req.Precision)
	return ResultPlan{InputUTXOs: inputs, Outputs: outputs}, nil
}

func (p CanonicalResultPlanner) BuildPlans(settled []ExecutionRecord) ([]ResultPlan, error) {
	if len(settled) == 0 {
		return nil, nil
	}

	groups := make([]canonicalRecordGroup, 0)
	groupIndex := make(map[string]int)
	for _, record := range settled {
		if !record.RequiresResult {
			continue
		}
		if len(record.AssetIntents) == 0 && len(record.FundingInputs) == 0 && record.GasUsed == 0 {
			continue
		}
		key := ContractResultKey(record.Contract)
		index, ok := groupIndex[key]
		if !ok {
			index = len(groups)
			groupIndex[key] = index
			groups = append(groups, canonicalRecordGroup{Contract: record.Contract})
		}
		groups[index].Records = append(groups[index].Records, record)
	}
	if len(groups) == 0 {
		return nil, nil
	}
	if p.UTXOs == nil {
		return nil, fmt.Errorf("missing contract UTXO provider")
	}
	if p.GasConfig.GasAssetName == "" {
		return nil, fmt.Errorf("missing gas asset name")
	}

	plans := make([]ResultPlan, 0, len(groups))
	for _, group := range groups {
		available, err := p.UTXOs(group.Contract)
		if err != nil {
			return nil, err
		}
		intents := make([]AssetIntent, 0)
		var gasFee *scommon.Decimal
		funding := make([]OutPoint, 0)
		for _, record := range group.Records {
			intents = append(intents, record.AssetIntents...)
			if record.ResultFeeMode != ResultFeeModePlainTxFee {
				callFee, err := p.GasConfig.CheckedCallFeeDecimalAtHeight(
					ResultExecutionGas(p.GasConfig, record),
					uint64(record.Height),
				)
				if err != nil {
					return nil, err
				}
				resultFee, err := p.GasConfig.CheckedResultBaseFee(uint64(record.Height))
				if err != nil {
					return nil, err
				}
				recordGasFee := DecimalAddAllowNil(callFee, resultFee)
				gasFee = DecimalAddAllowNil(gasFee, recordGasFee)
				refund, err := RecordGasRefund(record, available, p.GasConfig.GasAssetName, recordGasFee)
				if err != nil {
					return nil, err
				}
				if refund != nil {
					intents = append(intents, *refund)
				}
			}
			funding = append(funding, record.FundingInputs...)
		}
		plan, err := BuildCanonicalResultPlan(ResultPlanRequest{
			Contract:               group.Contract,
			Available:              available,
			Intents:                intents,
			GasAssetName:           p.GasConfig.GasAssetName,
			GasFee:                 gasFee,
			RequiredGasFundingUTXO: funding,
			Precision:              p.Precision,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func ContractResultKey(contractAddr contract.ContractAddress) string {
	hash := contract.ContractAddressHash(contractAddr)
	return fmt.Sprintf("%d:%d:%x", contractAddr.Version(), contractAddr.ContractType(), hash[:])
}

func ResultExecutionGas(cfg GasConfig, record ExecutionRecord) int64 {
	baseGas := cfg.BaseGasForKind(record.Kind)
	switch record.Kind {
	case ExecutionKindDeploy, ExecutionKindInvoke:
		if record.GasUsed <= baseGas {
			return 0
		}
		return record.GasUsed - baseGas
	case ExecutionKindTrigger:
		return record.GasUsed
	default:
		return record.GasUsed
	}
}

func RecordGasRefund(record ExecutionRecord, available []UTXO, gasAssetName string,
	recordGasFee *scommon.Decimal) (*AssetIntent, error) {

	return ResultGasRefundIntent(ResultGasRefund{
		CallID: record.CallID,
		To:     record.GasRefundRecipient,
		Inputs: append([]OutPoint(nil), record.FundingInputs...),
		GasFee: CloneDecimal(recordGasFee),
	}, record.Contract, available, gasAssetName)
}

func ResultGasRefundIntent(refund ResultGasRefund, contractAddr contract.ContractAddress, available []UTXO,
	gasAssetName string) (*AssetIntent, error) {

	if refund.To == "" || gasAssetName == "" {
		return nil, nil
	}
	fundingGas, err := TotalOutpointAsset(available, refund.Inputs, gasAssetName)
	if err != nil {
		return nil, err
	}
	if fundingGas == nil || fundingGas.Sign() == 0 {
		return nil, nil
	}
	gasFee := refund.GasFee
	if gasFee == nil {
		gasFee = ZeroDecimal()
	}
	if fundingGas.Cmp(gasFee) <= 0 {
		return nil, nil
	}
	return &AssetIntent{
		CallID:    refund.CallID,
		From:      contractAddr,
		To:        refund.To,
		AssetName: gasAssetName,
		Amount:    fundingGas.SubAlignPrecision(gasFee),
	}, nil
}

func TotalOutpointAsset(available []UTXO, outpoints []OutPoint, assetName string) (*scommon.Decimal, error) {
	total := ZeroDecimal()
	for _, outpoint := range outpoints {
		utxo, ok := FindUTXO(available, outpoint)
		if !ok {
			return nil, fmt.Errorf("required input %s not available", outpoint)
		}
		amount, err := utxo.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(amount)
	}
	return total, nil
}

func DecimalAddAllowNil(a, b *scommon.Decimal) *scommon.Decimal {
	if b == nil || b.Sign() == 0 {
		return a
	}
	if a == nil {
		return b.Clone()
	}
	return a.AddAlignPrecision(b)
}

func validateIntentContracts(contractAddr contract.ContractAddress, intents []AssetIntent) error {
	for _, intent := range intents {
		if !intent.From.Equal(contractAddr) {
			return errors.New("result plan cannot mix contracts")
		}
	}
	return nil
}

type canonicalRecordGroup struct {
	Contract contract.ContractAddress
	Records  []ExecutionRecord
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

func AddUint64(a, b uint64) (uint64, bool) {
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
			ExtraData: CloneBytes(extraData),
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
		outputs = append(outputs, NormalizeResultOutput(a.items[key]))
	}
	return outputs
}

func ResultOutputAssetsEqual(left, right wire.TxAssets) bool {
	return (&left).Equal(right)
}

func ResultOutputHasSingleAsset(output ResultOutput, assetName string, amount *scommon.Decimal) bool {
	if assetName == contract.SatoshiAssetName {
		sats, err := DecimalToInt64(*amount)
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
	total := ZeroDecimal()
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
		if total[contract.SatoshiAssetName] == nil {
			total[contract.SatoshiAssetName] = ZeroDecimal()
		}
		sats, err := u.AssetAmount(contract.SatoshiAssetName)
		if err != nil {
			return nil, fmt.Errorf("asset %s amount: %w", contract.SatoshiAssetName, err)
		}
		total[contract.SatoshiAssetName] = total[contract.SatoshiAssetName].AddAlignPrecision(sats)
		for _, asset := range u.TxAssets() {
			if total[asset.Name.String()] == nil {
				total[asset.Name.String()] = ZeroDecimal()
			}
			total[asset.Name.String()] = total[asset.Name.String()].AddAlignPrecision(&asset.Amount)
		}
	}
	return total, nil
}
