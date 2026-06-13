package template

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultOutput struct {
	To        string        `json:"to"`
	Value     int64         `json:"value,omitempty"`
	AssetName string        `json:"assetName,omitempty"`
	AssetAmt  string        `json:"assetAmt,omitempty"`
	Assets    wire.TxAssets `json:"assets,omitempty"`
}

type ResultPlan struct {
	Contract string           `json:"contract"`
	Height   int64            `json:"height"`
	ItemIDs  []int64          `json:"itemIds,omitempty"`
	GasFee   *scommon.Decimal `json:"gasFee,omitempty"`
	Inputs   []OutPoint       `json:"inputs,omitempty"`
	Outputs  []ResultOutput   `json:"outputs,omitempty"`
}

type UTXO struct {
	OutPoint OutPoint
	Contract ContractAddress
	Value    int64
	Assets   wire.TxAssets
	Height   int64
}

func (u UTXO) Clone() UTXO {
	return UTXO{
		OutPoint: u.OutPoint,
		Contract: u.Contract,
		Value:    u.Value,
		Assets:   u.Assets.Clone(),
		Height:   u.Height,
	}
}

type ContractUTXOProvider func(contract ContractAddress) ([]UTXO, error)

type ResultRecipientScriptResolver func(output ResultOutput) ([]byte, error)
type ResultOutputResolver func(resultTx *wire.MsgTx) ([]ResultOutput, error)

func ContractUTXOProviderWithTxOutputs(base ContractUTXOProvider, txs []*wire.MsgTx, prefix string) ContractUTXOProvider {
	current := make(map[string][]UTXO)
	resolver := StandardContractScriptResolver(prefix)
	for _, tx := range txs {
		parsed, err := ParseTx(tx, resolver)
		if err != nil {
			continue
		}
		for _, output := range parsed.ContractOutputs {
			contract := output.Contract
			current[contract.EncodeAddress()] = append(current[contract.EncodeAddress()], UTXO{
				OutPoint: output.OutPoint,
				Contract: contract,
				Value:    output.Value,
				Assets:   output.Assets.Clone(),
			})
		}
		defaultOutputs, err := contractcommon.FindDefaultInvokeOutputs(tx, prefix, ContractTypeTemplate)
		if err != nil {
			continue
		}
		for _, output := range defaultOutputs {
			contract := output.Contract
			current[contract.EncodeAddress()] = append(current[contract.EncodeAddress()], UTXO{
				OutPoint: OutPoint{TxID: output.TxID, Vout: output.Vout},
				Contract: contract,
				Value:    output.Value,
				Assets:   output.Assets.Clone(),
			})
		}
	}
	return func(contract ContractAddress) ([]UTXO, error) {
		out := make([]UTXO, 0)
		seen := make(map[OutPoint]struct{})
		if base != nil {
			utxos, err := base(contract)
			if err != nil {
				return nil, err
			}
			for _, utxo := range utxos {
				if _, ok := seen[utxo.OutPoint]; ok {
					continue
				}
				seen[utxo.OutPoint] = struct{}{}
				out = append(out, utxo.Clone())
			}
		}
		for _, utxo := range current[contract.EncodeAddress()] {
			if _, ok := seen[utxo.OutPoint]; ok {
				continue
			}
			seen[utxo.OutPoint] = struct{}{}
			out = append(out, utxo.Clone())
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].OutPoint.TxID != out[j].OutPoint.TxID {
				return out[i].OutPoint.TxID < out[j].OutPoint.TxID
			}
			return out[i].OutPoint.Vout < out[j].OutPoint.Vout
		})
		return out, nil
	}
}

type ResultTxBuildRequest struct {
	Status        ResultStatus
	Plans         []ResultPlan
	ResolveScript ResultRecipientScriptResolver
}

type CanonicalResultVerifier struct {
	ResolveOutput ResultOutputResolver
}

func DeriveInvokeCallID(invokeTxID string, vout uint32, contract ContractAddress) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("template-invoke:%s:%d:%s", invokeTxID, vout, encoded))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func BuildSettlementResultPlans(plans []*SettlementPlan, records []ExecutionRecord) ([]ResultPlan, error) {
	inputsByItem := make(map[int64][]OutPoint)
	feesByItem := make(map[int64]*scommon.Decimal)
	for _, record := range records {
		for _, itemID := range record.ItemIDs {
			inputsByItem[itemID] = append(inputsByItem[itemID], record.FundingInputs...)
			feesByItem[itemID] = decimalAddAllowNil(feesByItem[itemID], record.GasFee)
		}
	}

	out := make([]ResultPlan, 0, len(plans))
	for _, plan := range plans {
		if !settlementPlanHasChanges(plan) {
			continue
		}
		resultPlan, err := BuildSettlementResultPlan(plan, inputsByItem)
		if err != nil {
			return nil, err
		}
		for _, itemID := range resultPlan.ItemIDs {
			resultPlan.GasFee = decimalAddAllowNil(resultPlan.GasFee, feesByItem[itemID])
		}
		out = append(out, resultPlan)
	}
	return out, nil
}

func cloneResultPlans(plans []ResultPlan) []ResultPlan {
	out := make([]ResultPlan, len(plans))
	for i := range plans {
		out[i] = cloneResultPlan(plans[i])
	}
	return out
}

func cloneResultPlan(plan ResultPlan) ResultPlan {
	out := plan
	out.GasFee = plan.GasFee.Clone()
	out.ItemIDs = append([]int64(nil), plan.ItemIDs...)
	out.Inputs = append([]OutPoint(nil), plan.Inputs...)
	out.Outputs = make([]ResultOutput, len(plan.Outputs))
	for i := range plan.Outputs {
		out.Outputs[i] = plan.Outputs[i]
		out.Outputs[i].Assets = plan.Outputs[i].Assets.Clone()
	}
	return out
}

func BuildSettlementResultPlan(plan *SettlementPlan, inputsByItem map[int64][]OutPoint) (ResultPlan, error) {
	if plan == nil {
		return ResultPlan{}, fmt.Errorf("missing settlement plan")
	}
	out := ResultPlan{
		Contract: plan.Contract,
		Height:   plan.Height,
		ItemIDs:  append([]int64(nil), plan.ItemIDs...),
		Inputs:   append([]OutPoint(nil), plan.Inputs...),
	}
	for _, itemID := range plan.ItemIDs {
		out.Inputs = append(out.Inputs, inputsByItem[itemID]...)
	}
	out.Inputs = uniqueOutPoints(out.Inputs)
	for _, transfer := range plan.Transfers {
		output, err := resultOutputFromTransfer(transfer)
		if err != nil {
			return ResultPlan{}, err
		}
		if !resultOutputIsZero(output) {
			out.Outputs = append(out.Outputs, output)
		}
	}
	return out, nil
}

func AugmentResultPlans(plans []ResultPlan, store *RuntimeStore, gasConfig GasConfig, contractUTXOs ContractUTXOProvider) ([]ResultPlan, error) {
	gasConfig = gasConfig.normalized()
	out := cloneResultPlans(plans)
	for i := range out {
		contract, err := contractcommon.DecodeContractAddress(out[i].Contract)
		if err != nil {
			return nil, err
		}
		if contractUTXOs != nil {
			utxos, err := contractUTXOs(contract)
			if err != nil {
				return nil, err
			}
			availableAssets := wire.TxAssets(nil)
			availableValue := int64(0)
			out[i].Inputs = nil
			for _, utxo := range utxos {
				if !utxo.Contract.Equal(contract) {
					continue
				}
				out[i].Inputs = append(out[i].Inputs, utxo.OutPoint)
				availableValue += utxo.Value
				if len(utxo.Assets) != 0 {
					if err := availableAssets.Merge(utxo.Assets); err != nil {
						return nil, err
					}
				}
			}
			closed, deployer, err := closedContractChangeRecipient(contract, store)
			if err != nil {
				return nil, err
			}
			if closed {
				out[i].Outputs = capClosedResultOutputsByAvailable(out[i].Outputs, availableAssets, availableValue, gasConfig.GasAssetName, out[i].GasFee)
			}
			change, err := contractChangeOutput(contract, store, gasConfig, availableAssets)
			if err != nil {
				return nil, err
			}
			change.Assets, err = resultAssetsChange(availableAssets, out[i].Outputs, gasConfig.GasAssetName, out[i].GasFee)
			if err != nil {
				return nil, err
			}
			change.Value = availableValue - resultOutputsValue(out[i].Outputs)
			if change.Value < 0 {
				return nil, fmt.Errorf("template result outputs spend %d sats but only %d sats are available", resultOutputsValue(out[i].Outputs), availableValue)
			}
			if !resultOutputIsZero(change) {
				if closed {
					out[i].Outputs = append(out[i].Outputs,
						splitClosedProfitChange(change, deployer, gasConfig.BootstrapAddress)...)
				} else {
					out[i].Outputs = append(out[i].Outputs, change)
				}
			}
		} else {
			change, err := contractChangeOutput(contract, store, gasConfig, nil)
			if err != nil {
				return nil, err
			}
			if !resultOutputIsZero(change) {
				out[i].Outputs = append(out[i].Outputs, change)
			}
		}
		out[i].Inputs = uniqueOutPoints(out[i].Inputs)
	}
	return out, nil
}

func capClosedResultOutputsByAvailable(outputs []ResultOutput, availableAssets wire.TxAssets, availableValue int64, gasAssetName string, gasFee *scommon.Decimal) []ResultOutput {
	out := cloneResultOutputs(outputs)
	capResultOutputValuesByAvailable(out, availableValue)
	capResultOutputAssetsByAvailable(out, availableAssets, gasAssetName, gasFee)
	return compactResultOutputs(out)
}

func cloneResultOutputs(outputs []ResultOutput) []ResultOutput {
	out := make([]ResultOutput, len(outputs))
	for i := range outputs {
		out[i] = outputs[i]
		out[i].Assets = outputs[i].Assets.Clone()
	}
	return out
}

func compactResultOutputs(outputs []ResultOutput) []ResultOutput {
	out := make([]ResultOutput, 0, len(outputs))
	for _, output := range outputs {
		if !resultOutputIsZero(output) {
			out = append(out, output)
		}
	}
	return out
}

func capResultOutputValuesByAvailable(outputs []ResultOutput, availableValue int64) {
	total := resultOutputsValue(outputs)
	if total <= 0 || availableValue < 0 || total <= availableValue {
		return
	}
	originalValues := make([]int64, len(outputs))
	remaining := availableValue
	for i := range outputs {
		originalValues[i] = outputs[i].Value
		if outputs[i].Value <= 0 {
			outputs[i].Value = 0
			continue
		}
		scaled := new(big.Int).Mul(big.NewInt(outputs[i].Value), big.NewInt(availableValue))
		scaled.Div(scaled, big.NewInt(total))
		outputs[i].Value = scaled.Int64()
		remaining -= outputs[i].Value
	}
	for i := range outputs {
		if remaining <= 0 {
			break
		}
		if originalValues[i] <= 0 {
			continue
		}
		outputs[i].Value++
		remaining--
	}
}

func capResultOutputAssetsByAvailable(outputs []ResultOutput, availableAssets wire.TxAssets, gasAssetName string, gasFee *scommon.Decimal) {
	requested := resultRequestedAssetTotals(outputs)
	if len(requested) == 0 {
		return
	}
	limits := availableAssetLimits(availableAssets, gasAssetName, gasFee)
	for i := range outputs {
		if len(outputs[i].Assets) == 0 {
			continue
		}
		nextAssets := make(wire.TxAssets, 0, len(outputs[i].Assets))
		for _, asset := range outputs[i].Assets {
			key := asset.Name.String()
			limit := limits[key]
			total := requested[key]
			if limit == nil || limit.Sign() <= 0 || total == nil || total.Sign() <= 0 {
				if assetNameKey(outputs[i].AssetName) == key {
					outputs[i].AssetAmt = ""
				}
				continue
			}
			amount := asset.Amount.Clone()
			if total.Cmp(limit) > 0 {
				amount = decimalMulByFraction(amount, limit, total)
			}
			if amount.Sign() <= 0 {
				if assetNameKey(outputs[i].AssetName) == key {
					outputs[i].AssetAmt = ""
				}
				continue
			}
			next := asset
			next.Amount = *amount
			nextAssets = append(nextAssets, next)
			if assetNameKey(outputs[i].AssetName) == key {
				outputs[i].AssetAmt = amount.String()
			}
		}
		if len(nextAssets) == 0 {
			outputs[i].Assets = nil
			outputs[i].AssetName = ""
			outputs[i].AssetAmt = ""
		} else {
			outputs[i].Assets = nextAssets
		}
	}
}

func resultRequestedAssetTotals(outputs []ResultOutput) map[string]*scommon.Decimal {
	totals := make(map[string]*scommon.Decimal)
	for _, output := range outputs {
		for _, asset := range output.Assets {
			key := asset.Name.String()
			totals[key] = decimalAddAllowNil(totals[key], asset.Amount.Clone())
		}
	}
	return totals
}

func decimalMulByFraction(amount, numerator, denominator *scommon.Decimal) *scommon.Decimal {
	if amount == nil || numerator == nil || denominator == nil || denominator.Sign() <= 0 {
		return parseDecimalOrZero("0")
	}
	return scommon.DecimalMulV2(amount, numerator).Div(denominator)
}

func availableAssetLimits(availableAssets wire.TxAssets, gasAssetName string, gasFee *scommon.Decimal) map[string]*scommon.Decimal {
	limits := make(map[string]*scommon.Decimal)
	for _, asset := range availableAssets {
		limits[asset.Name.String()] = asset.Amount.Clone()
	}
	if gasAssetName != "" && gasFee != nil && gasFee.Sign() > 0 {
		limit := limits[assetNameKey(gasAssetName)]
		if limit != nil {
			limit = limit.SubAlignPrecision(gasFee)
			if limit.Sign() < 0 {
				limit = parseDecimalOrZero("0")
			}
			limits[assetNameKey(gasAssetName)] = limit
		}
	}
	return limits
}

func assetNameKey(assetName string) string {
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return assetName
	}
	return name.String()
}

func closedContractChangeRecipient(contract ContractAddress, store *RuntimeStore) (bool, string, error) {
	if store == nil {
		return false, "", nil
	}
	runtime, ok := store.Get(contract)
	if !ok || runtime == nil {
		return false, "", nil
	}
	state, err := runtime.loadRuntimeState()
	if err != nil {
		return false, "", err
	}
	return state.Running.Closed, runtime.RuntimeBase().Deployer(), nil
}

func splitClosedProfitChange(change ResultOutput, deployer, bootstrap string) []ResultOutput {
	if resultOutputIsZero(change) {
		return nil
	}
	if deployer == "" || bootstrap == "" || deployer == bootstrap {
		change.To = deployer
		return []ResultOutput{change}
	}
	deployerOut := ResultOutput{To: deployer}
	bootstrapOut := ResultOutput{To: bootstrap}
	deployerOut.Value = change.Value * 6 / 10
	bootstrapOut.Value = change.Value - deployerOut.Value
	deployerOut.Assets, bootstrapOut.Assets = splitAssetsByBPS(change.Assets, 6000)
	out := make([]ResultOutput, 0, 2)
	if !resultOutputIsZero(deployerOut) {
		out = append(out, deployerOut)
	}
	if !resultOutputIsZero(bootstrapOut) {
		out = append(out, bootstrapOut)
	}
	return out
}

func splitAssetsByBPS(assets wire.TxAssets, deployerBPS int64) (wire.TxAssets, wire.TxAssets) {
	if len(assets) == 0 {
		return nil, nil
	}
	deployer := make(wire.TxAssets, 0, len(assets))
	bootstrap := make(wire.TxAssets, 0, len(assets))
	for _, asset := range assets {
		deployerAmt := asset.Amount.MulBigInt(big.NewInt(deployerBPS)).DivBigInt(big.NewInt(10000))
		bootstrapAmt := scommon.DecimalSub(asset.Amount.Clone(), deployerAmt)
		if deployerAmt.Sign() > 0 {
			next := asset
			next.Amount = *deployerAmt
			deployer = append(deployer, next)
		}
		if bootstrapAmt.Sign() > 0 {
			next := asset
			next.Amount = *bootstrapAmt
			bootstrap = append(bootstrap, next)
		}
	}
	if len(deployer) == 0 {
		deployer = nil
	}
	if len(bootstrap) == 0 {
		bootstrap = nil
	}
	return deployer, bootstrap
}

func resultAssetsChange(available wire.TxAssets, outputs []ResultOutput, gasAssetName string, gasFee *scommon.Decimal) (wire.TxAssets, error) {
	if len(available) == 0 {
		return nil, nil
	}
	spent := wire.TxAssets(nil)
	for _, output := range outputs {
		if len(output.Assets) == 0 {
			continue
		}
		if err := spent.Merge(output.Assets); err != nil {
			return nil, err
		}
	}
	change := available.Clone()
	if err := change.Split(spent); err != nil {
		return nil, err
	}
	if gasFee != nil && gasFee.Sign() > 0 {
		feeAssets, err := newGasAssetSet(gasAssetName, gasFee.String())
		if err != nil {
			return nil, err
		}
		if err := change.Split(feeAssets); err != nil {
			return nil, fmt.Errorf("insufficient gas asset for result fee: %w", err)
		}
	}
	if len(change) == 0 {
		return nil, nil
	}
	return change, nil
}

func resultOutputsValue(outputs []ResultOutput) int64 {
	value := int64(0)
	for _, output := range outputs {
		value += output.Value
	}
	return value
}

func contractChangeOutput(contract ContractAddress, store *RuntimeStore, gasConfig GasConfig, availableAssets wire.TxAssets) (ResultOutput, error) {
	if store == nil {
		return ResultOutput{}, nil
	}
	runtime, ok := store.Get(contract)
	if !ok || runtime == nil {
		return ResultOutput{}, nil
	}
	state, err := runtime.loadRuntimeState()
	if err != nil {
		return ResultOutput{}, err
	}

	assets := wire.TxAssets{}
	assetName := contractAssetName(runtime.Contract())
	if assetName != "" && state.Running.AssetAInPool != nil && state.Running.AssetAInPool.Sign() > 0 {
		poolAssets, err := newAssetSet(assetName, state.Running.AssetAInPool.String())
		if err != nil {
			return ResultOutput{}, err
		}
		if err := assets.Merge(poolAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	if exchange, ok := runtime.Contract().(*ExchangeContract); ok &&
		exchange.AssetBName != "" &&
		exchange.AssetBName != SatoshiAssetName &&
		state.Running.AssetBInPool != nil &&
		state.Running.AssetBInPool.Sign() > 0 {
		poolAssets, err := newAssetSet(exchange.AssetBName, state.Running.AssetBInPool.String())
		if err != nil {
			return ResultOutput{}, err
		}
		if err := assets.Merge(poolAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	gasAssetName := gasConfig.GasAssetName
	if gasAssetName == "" {
		gasAssetName = DefaultGasConfig().GasAssetName
	}
	if state.Running.GasBalance != nil && state.Running.GasBalance.Sign() > 0 {
		gasAssets, err := newGasAssetSet(gasAssetName, state.Running.GasBalance.String())
		if err != nil {
			return ResultOutput{}, err
		}
		if err := assets.Merge(gasAssets); err != nil {
			return ResultOutput{}, err
		}
	}
	if len(assets) == 0 {
		assets = nil
	} else if len(availableAssets) != 0 {
		assets = capAssetsByAvailable(assets, availableAssets)
	}
	to := contract.MustEncode()
	if state.Running.Closed {
		to = runtime.RuntimeBase().Deployer()
	}
	return ResultOutput{
		To:     to,
		Value:  contractChangeValue(runtime.Contract(), state.Running.AssetBInPool),
		Assets: assets,
	}, nil
}

func contractChangeValue(contract Contract, assetB *scommon.Decimal) int64 {
	if exchange, ok := contract.(*ExchangeContract); ok {
		if exchange.AssetBName == SatoshiAssetName {
			return decimalInt64(assetB)
		}
		return 0
	}
	return decimalInt64(assetB)
}

func capAssetsByAvailable(assets, available wire.TxAssets) wire.TxAssets {
	if len(assets) == 0 || len(available) == 0 {
		return assets
	}
	out := make(wire.TxAssets, 0, len(assets))
	for _, asset := range assets {
		availableAsset, err := available.Find(&asset.Name)
		if err != nil || availableAsset == nil {
			continue
		}
		amount := asset.Amount.Clone()
		if amount.Cmp(&availableAsset.Amount) > 0 {
			amount = availableAsset.Amount.Clone()
		}
		if amount.Sign() <= 0 {
			continue
		}
		out = append(out, scommon.AssetInfo{
			Name:       asset.Name,
			Amount:     *amount,
			BindingSat: asset.BindingSat,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func BuildResultTx(req ResultTxBuildRequest) (*wire.MsgTx, error) {
	if len(req.Plans) == 0 {
		return nil, fmt.Errorf("missing result plans")
	}
	tx := wire.NewMsgTx(2)
	resultCount := 0
	for _, plan := range req.Plans {
		resultCount += resultPlanCount(plan)
		for _, input := range plan.Inputs {
			outpoint, err := resultWireOutPoint(input)
			if err != nil {
				return nil, err
			}
			tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
		}
		for _, output := range plan.Outputs {
			if req.ResolveScript == nil {
				return nil, fmt.Errorf("missing result output script resolver")
			}
			txOut, err := resultTxOut(output, req.ResolveScript)
			if err != nil {
				return nil, err
			}
			tx.AddTxOut(txOut)
		}
	}
	if resultCount == 0 || resultCount > math.MaxUint16 {
		return nil, fmt.Errorf("invalid result count %d", resultCount)
	}
	script, err := contractcommon.ResultNullDataScript(ResultPayload{
		Status:      req.Status,
		ResultCount: uint16(resultCount),
	})
	if err != nil {
		return nil, err
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx, nil
}

func BuildCanonicalSettlementResultTx(status ResultStatus, settlementPlans []*SettlementPlan, records []ExecutionRecord, resolve ResultRecipientScriptResolver) (*wire.MsgTx, []ResultPlan, error) {
	plans, err := BuildSettlementResultPlans(settlementPlans, records)
	if err != nil {
		return nil, nil, err
	}
	tx, err := BuildResultTx(ResultTxBuildRequest{
		Status:        status,
		Plans:         plans,
		ResolveScript: resolve,
	})
	if err != nil {
		return nil, nil, err
	}
	return tx, plans, nil
}

func (v CanonicalResultVerifier) Verify(resultTx *wire.MsgTx, expected []ResultPlan, status ResultStatus) error {
	payload, err := ResultPayloadFromTx(resultTx)
	if err != nil {
		return err
	}
	if payload.Status != status {
		return fmt.Errorf("result status mismatch: got %d want %d", payload.Status, status)
	}
	expectedCount := 0
	expectedInputs := make([]OutPoint, 0)
	expectedOutputs := make([]ResultOutput, 0)
	for _, plan := range expected {
		expectedCount += resultPlanCount(plan)
		expectedInputs = append(expectedInputs, plan.Inputs...)
		expectedOutputs = append(expectedOutputs, plan.Outputs...)
	}
	if expectedCount > math.MaxUint16 || payload.ResultCount != uint16(expectedCount) {
		return fmt.Errorf("result count mismatch: got %d want %d", payload.ResultCount, expectedCount)
	}
	if err := verifyResultInputOrder(resultTx, expectedInputs); err != nil {
		return err
	}
	if v.ResolveOutput != nil {
		actualOutputs, err := v.ResolveOutput(resultTx)
		if err != nil {
			return err
		}
		if err := verifyResultOutputs(actualOutputs, expectedOutputs); err != nil {
			return err
		}
	}
	return nil
}

func resultPlanCount(plan ResultPlan) int {
	if len(plan.ItemIDs) != 0 {
		return len(plan.ItemIDs)
	}
	return 1
}

func ResultPayloadFromTx(tx *wire.MsgTx) (ResultPayload, error) {
	if tx == nil {
		return ResultPayload{}, fmt.Errorf("missing result transaction")
	}
	var payload *ResultPayload
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return ResultPayload{}, fmt.Errorf("nil output %d", i)
		}
		if !txscript.IsUnspendable(txOut.PkScript) {
			continue
		}
		p, err := contractcommon.ReadResultNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if payload != nil {
			return ResultPayload{}, fmt.Errorf("multiple template RESULT payloads")
		}
		payload = &p
	}
	if payload == nil {
		return ResultPayload{}, fmt.Errorf("missing template RESULT payload")
	}
	return *payload, nil
}

func resultOutputFromTransfer(transfer SettlementTransfer) (ResultOutput, error) {
	if transfer.AssetName == SatoshiAssetName {
		value := transfer.SatValue
		if transfer.AssetAmt != "" {
			value += decimalInt64(parseDecimalOrZero(transfer.AssetAmt))
		}
		return ResultOutput{
			To:        transfer.To,
			Value:     value,
			AssetName: transfer.AssetName,
			AssetAmt:  transfer.AssetAmt,
		}, nil
	}
	assets, err := newAssetSet(transfer.AssetName, transfer.AssetAmt)
	if err != nil {
		return ResultOutput{}, err
	}
	return ResultOutput{
		To:        transfer.To,
		Value:     transfer.SatValue,
		AssetName: transfer.AssetName,
		AssetAmt:  transfer.AssetAmt,
		Assets:    assets,
	}, nil
}

func newAssetSet(assetName, amount string) (wire.TxAssets, error) {
	return newAssetSetWithPrecision(assetName, amount, MaxPriceDivisibility)
}

func newGasAssetSet(assetName, amount string) (wire.TxAssets, error) {
	return newAssetSetWithPrecision(assetName, amount, contractcommon.GasFeePrecision)
}

func newAssetSetWithPrecision(assetName, amount string, maxPrecision int) (wire.TxAssets, error) {
	if assetName == "" || amount == "" || amount == "0" {
		return nil, nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	amt, err := scommon.NewDecimalFromString(amount, maxPrecision)
	if err != nil {
		return nil, err
	}
	if amt.Sign() <= 0 {
		return nil, nil
	}
	return wire.TxAssets{{
		Name:   *name,
		Amount: *amt,
	}}, nil
}

func resultTxOut(output ResultOutput, resolve ResultRecipientScriptResolver) (*wire.TxOut, error) {
	if output.Value < 0 {
		return nil, fmt.Errorf("negative result output value")
	}
	pkScript, err := resolve(output)
	if err != nil {
		return nil, err
	}
	return wire.NewTxOut(output.Value, output.Assets.Clone(), pkScript), nil
}

func resultWireOutPoint(outpoint OutPoint) (*wire.OutPoint, error) {
	hash, err := chainhash.NewHashFromStr(outpoint.TxID)
	if err != nil {
		return nil, err
	}
	return wire.NewOutPoint(hash, outpoint.Vout), nil
}

func verifyResultInputOrder(resultTx *wire.MsgTx, expected []OutPoint) error {
	if len(resultTx.TxIn) != len(expected) {
		return fmt.Errorf("result input count mismatch: got %d want %d", len(resultTx.TxIn), len(expected))
	}
	for i, txIn := range resultTx.TxIn {
		actual := WireOutPointToTemplate(txIn.PreviousOutPoint)
		if actual != expected[i] {
			return fmt.Errorf("result input %d mismatch: got %s want %s", i, actual, expected[i])
		}
	}
	return nil
}

func verifyResultOutputs(actual, expected []ResultOutput) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("result output count mismatch: got %d want %d", len(actual), len(expected))
	}
	for i := range expected {
		got := normalizeResultOutput(actual[i])
		want := normalizeResultOutput(expected[i])
		if got.To != want.To ||
			got.Value != want.Value ||
			!got.Assets.Equal(want.Assets) {
			return fmt.Errorf("result output %d mismatch: got %+v want %+v", i, got, want)
		}
	}
	return nil
}

func normalizeResultOutput(output ResultOutput) ResultOutput {
	output.Assets = output.Assets.Clone()
	if len(output.Assets) == 0 {
		output.Assets = nil
	}
	return output
}

func uniqueOutPoints(in []OutPoint) []OutPoint {
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

func resultOutputIsZero(output ResultOutput) bool {
	return output.Value == 0 && len(output.Assets) == 0
}

func DeriveDeployCallID(deployTxID string, contract ContractAddress) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("template-deploy:%s:%s", deployTxID, encoded))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}
