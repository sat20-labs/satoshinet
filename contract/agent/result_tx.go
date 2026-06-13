package agent

import (
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
	AssetName string        `json:"asset_name,omitempty"`
	AssetAmt  string        `json:"asset_amt,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	Assets    wire.TxAssets `json:"assets,omitempty"`
}

type ResultPlan struct {
	Contract string           `json:"contract"`
	GasFee   *scommon.Decimal `json:"gas_fee,omitempty"`
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

type CanonicalResultVerifier struct {
	ResolveOutput ResultOutputResolver
}

type ResultTxBuildRequest struct {
	Status        ResultStatus
	Plans         []ResultPlan
	ResolveScript ResultRecipientScriptResolver
}

func ContractUTXOProviderWithTxOutputs(base ContractUTXOProvider, txs []*wire.MsgTx, prefix string) ContractUTXOProvider {
	current := make(map[string][]UTXO)
	resolver := StandardContractScriptResolver(prefix)
	for _, tx := range txs {
		parsed, err := ParseTx(tx, resolver)
		if err != nil {
			continue
		}
		outputs := parsed.ContractOutputs
		if len(outputs) == 0 {
			outputs = contractOutputsFromTx(tx, resolver)
		}
		for _, output := range outputs {
			contract := output.Contract
			current[contract.EncodeAddress()] = append(current[contract.EncodeAddress()], UTXO{
				OutPoint: output.OutPoint,
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

func contractOutputsFromTx(tx *wire.MsgTx, resolver ContractScriptResolver) []ContractOutput {
	if tx == nil || resolver == nil {
		return nil
	}
	txid := tx.TxID()
	outputs := make([]ContractOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		contract, ok, err := resolver(txOut.PkScript)
		if err != nil || !ok || contract.ContractType() != ContractTypeAgent {
			continue
		}
		vout := uint32(i)
		outputs = append(outputs, ContractOutput{
			OutPoint: OutPoint{TxID: txid, Vout: vout},
			Vout:     vout,
			Contract: contract,
			Value:    txOut.Value,
			Assets:   txOut.Assets.Clone(),
			PkScript: cloneBytes(txOut.PkScript),
		})
	}
	return outputs
}

func BuildSettlementResultPlan(plan *PredictionSettlementPlan) (ResultPlan, error) {
	if plan == nil {
		return ResultPlan{}, fmt.Errorf("missing prediction settlement plan")
	}
	result := ResultPlan{
		Contract: plan.Contract,
	}
	for _, transfer := range plan.Transfers {
		output, err := resultOutputFromTransfer(transfer)
		if err != nil {
			return ResultPlan{}, err
		}
		if !resultOutputIsZero(output) {
			result.Outputs = append(result.Outputs, output)
		}
	}
	return result, nil
}

func BuildSettlementResultPlans(plans []*PredictionSettlementPlan) ([]ResultPlan, error) {
	out := make([]ResultPlan, 0, len(plans))
	for _, plan := range plans {
		if plan == nil || len(plan.Transfers) == 0 {
			continue
		}
		result, err := BuildSettlementResultPlan(plan)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}

func AddGasFeesToResultPlans(plans []ResultPlan, records []ExecutionRecord) []ResultPlan {
	out := cloneResultPlans(plans)
	index := make(map[string]int)
	for i, plan := range out {
		index[plan.Contract] = i
	}
	for _, record := range records {
		if !record.RequiresResult || record.GasFee == nil || record.GasFee.Sign() == 0 {
			continue
		}
		contract := record.Contract.MustEncode()
		i, ok := index[contract]
		if !ok {
			i = len(out)
			index[contract] = i
			out = append(out, ResultPlan{Contract: contract})
		}
		out[i].GasFee = decimalAddAllowNil(out[i].GasFee, record.GasFee)
		out[i].Inputs = append(out[i].Inputs, record.FundingInputs...)
		out[i].Inputs = uniqueOutPoints(out[i].Inputs)
	}
	return out
}

func decimalAddAllowNil(a, b *scommon.Decimal) *scommon.Decimal {
	if b == nil || b.Sign() == 0 {
		return a
	}
	if a == nil {
		return b.Clone()
	}
	return a.AddAlignPrecision(b)
}

func AugmentResultPlans(plans []ResultPlan, contractUTXOs ContractUTXOProvider) ([]ResultPlan, error) {
	out := cloneResultPlans(plans)
	for i := range out {
		contract, err := contractcommon.DecodeContractAddress(out[i].Contract)
		if err != nil {
			return nil, err
		}
		if contractUTXOs == nil {
			continue
		}
		if len(out[i].Outputs) == 0 {
			out[i].Inputs = uniqueOutPoints(out[i].Inputs)
			continue
		}
		utxos, err := contractUTXOs(contract)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(utxos, func(i, j int) bool {
			if utxos[i].Height != utxos[j].Height {
				return utxos[i].Height < utxos[j].Height
			}
			if utxos[i].OutPoint.TxID != utxos[j].OutPoint.TxID {
				return utxos[i].OutPoint.TxID < utxos[j].OutPoint.TxID
			}
			return utxos[i].OutPoint.Vout < utxos[j].OutPoint.Vout
		})
		poolAsset := resultPlanAssetName(out[i])
		poolAmount := zeroDecimal()
		out[i].Inputs = nil
		for _, utxo := range utxos {
			if !utxo.Contract.Equal(contract) {
				continue
			}
			out[i].Inputs = append(out[i].Inputs, utxo.OutPoint)
			if poolAsset != "" {
				amount, err := utxoAssetAmount(utxo, poolAsset)
				if err != nil {
					return nil, err
				}
				poolAmount = decimalAdd(poolAmount, amount)
			}
		}
		if poolAsset != "" {
			scaled, err := scaleResultOutputsToPool(out[i].Outputs, poolAsset, poolAmount)
			if err != nil {
				return nil, err
			}
			out[i].Outputs = scaled
		}
		out[i].Inputs = uniqueOutPoints(out[i].Inputs)
	}
	return out, nil
}

func resultPlanAssetName(plan ResultPlan) string {
	for _, output := range plan.Outputs {
		if output.AssetName != "" {
			return output.AssetName
		}
	}
	return ""
}

func utxoAssetAmount(utxo UTXO, assetName string) (*scommon.Decimal, error) {
	if assetName == SatoshiAssetName {
		if utxo.Value < 0 {
			return nil, fmt.Errorf("contract UTXO %s has negative value", utxo.OutPoint)
		}
		return scommon.NewDefaultDecimal(utxo.Value), nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	asset, err := utxo.Assets.Find(name)
	if err != nil || asset == nil {
		return scommon.NewDefaultDecimal(0), nil
	}
	return asset.Amount.Clone(), nil
}

func scaleResultOutputsToPool(outputs []ResultOutput, assetName string, pool *scommon.Decimal) ([]ResultOutput, error) {
	out := cloneResultOutputs(outputs)
	total := zeroDecimal()
	for _, output := range out {
		if output.AssetName != assetName {
			continue
		}
		total = decimalAdd(total, parseDecimalOrZero(output.AssetAmt))
	}
	if pool == nil || pool.Cmp(total) == 0 {
		return out, nil
	}
	if total.Sign() == 0 {
		if pool.Sign() == 0 {
			return out, nil
		}
		return nil, fmt.Errorf("cannot distribute prediction pool without result outputs")
	}
	if pool.Cmp(total) < 0 {
		return nil, fmt.Errorf("prediction result outputs spend %s but only %s is available", total.String(), pool.String())
	}

	extra := scommon.DecimalSub(pool, total)
	precision := pool.Precision
	if total.Precision > precision {
		precision = total.Precision
	}
	for _, output := range out {
		amount := parseDecimalOrZero(output.AssetAmt)
		if amount.Precision > precision {
			precision = amount.Precision
		}
	}

	extraValue := extra.NewPrecision(precision).Value
	totalValue := total.NewPrecision(precision).Value
	if extraValue.Sign() == 0 {
		return out, nil
	}
	shares := make([]*big.Int, len(out))
	sum := big.NewInt(0)
	for i, output := range out {
		if output.AssetName != assetName {
			continue
		}
		amount := parseDecimalOrZero(output.AssetAmt).NewPrecision(precision)
		share := new(big.Int).Mul(extraValue, amount.Value)
		share.Div(share, totalValue)
		shares[i] = share
		sum.Add(sum, share)
	}
	remainder := new(big.Int).Sub(extraValue, sum)
	for i := range out {
		if remainder.Sign() == 0 {
			break
		}
		if out[i].AssetName != assetName {
			continue
		}
		if shares[i] == nil {
			shares[i] = big.NewInt(0)
		}
		shares[i].Add(shares[i], big.NewInt(1))
		remainder.Sub(remainder, big.NewInt(1))
	}

	for i := range out {
		if out[i].AssetName != assetName || shares[i] == nil || shares[i].Sign() == 0 {
			continue
		}
		amount := parseDecimalOrZero(out[i].AssetAmt).NewPrecision(precision)
		amount.Value.Add(amount.Value, shares[i])
		out[i].AssetAmt = amount.String()
		if err := rebuildResultOutputAsset(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func rebuildResultOutputAsset(output *ResultOutput) error {
	if output == nil || output.AssetName == "" {
		return nil
	}
	if output.AssetName == SatoshiAssetName {
		amt, err := scommon.NewDecimalFromString(output.AssetAmt, MaxPredictionDecimalPrecision)
		if err != nil {
			return err
		}
		if amt.Precision != 0 {
			normalized := amt.NewPrecision(0)
			if normalized.NewPrecision(amt.Precision).Cmp(amt) != 0 {
				return fmt.Errorf("satoshi output amount must be integer")
			}
			amt = normalized
			output.AssetAmt = amt.String()
		}
		output.Value = amt.Int64()
		output.Assets = nil
		return nil
	}
	assets, err := newAssetSet(output.AssetName, output.AssetAmt)
	if err != nil {
		return err
	}
	output.Value = 0
	output.Assets = assets
	return nil
}

func BuildResultTx(req ResultTxBuildRequest) (*wire.MsgTx, error) {
	if len(req.Plans) == 0 {
		return nil, fmt.Errorf("missing result plans")
	}
	tx := wire.NewMsgTx(2)
	for _, plan := range req.Plans {
		for _, input := range plan.Inputs {
			outpoint, err := resultWireOutPoint(input)
			if err != nil {
				return nil, err
			}
			tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
		}
		for _, output := range plan.Outputs {
			txOut, err := resultTxOut(output, req.ResolveScript)
			if err != nil {
				return nil, err
			}
			tx.AddTxOut(txOut)
		}
	}
	if len(req.Plans) > math.MaxUint16 {
		return nil, fmt.Errorf("invalid result count %d", len(req.Plans))
	}
	script, err := contractcommon.ResultNullDataScript(ResultPayload{
		Status:      req.Status,
		ResultCount: uint16(len(req.Plans)),
	})
	if err != nil {
		return nil, err
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx, nil
}

func (v CanonicalResultVerifier) Verify(resultTx *wire.MsgTx, expected []ResultPlan, status ResultStatus) error {
	payload, err := ResultPayloadFromTx(resultTx)
	if err != nil {
		return err
	}
	if payload.Status != status {
		return fmt.Errorf("result status mismatch: got %d want %d", payload.Status, status)
	}
	if len(expected) > math.MaxUint16 || payload.ResultCount != uint16(len(expected)) {
		return fmt.Errorf("result count mismatch: got %d want %d", payload.ResultCount, len(expected))
	}
	expectedInputs := make([]OutPoint, 0)
	expectedOutputs := make([]ResultOutput, 0)
	for _, plan := range expected {
		expectedInputs = append(expectedInputs, plan.Inputs...)
		expectedOutputs = append(expectedOutputs, plan.Outputs...)
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
			return ResultPayload{}, fmt.Errorf("multiple agent RESULT payloads")
		}
		payload = &p
	}
	if payload == nil {
		return ResultPayload{}, fmt.Errorf("missing agent RESULT payload")
	}
	return *payload, nil
}

func resultOutputFromTransfer(transfer PredictionSettlementOutput) (ResultOutput, error) {
	if transfer.AssetName == SatoshiAssetName {
		amt, err := scommon.NewDecimalFromString(transfer.AssetAmt, MaxPredictionDecimalPrecision)
		if err != nil {
			return ResultOutput{}, err
		}
		if amt.Precision != 0 {
			normalized := amt.NewPrecision(0)
			if normalized.NewPrecision(amt.Precision).Cmp(amt) != 0 {
				return ResultOutput{}, fmt.Errorf("satoshi output amount must be integer")
			}
			amt = normalized
		}
		return ResultOutput{
			To:        transfer.To,
			Value:     amt.Int64(),
			AssetName: transfer.AssetName,
			AssetAmt:  transfer.AssetAmt,
			Reason:    transfer.Reason,
		}, nil
	}
	assets, err := newAssetSet(transfer.AssetName, transfer.AssetAmt)
	if err != nil {
		return ResultOutput{}, err
	}
	return ResultOutput{
		To:        transfer.To,
		AssetName: transfer.AssetName,
		AssetAmt:  transfer.AssetAmt,
		Reason:    transfer.Reason,
		Assets:    assets,
	}, nil
}

func newAssetSet(assetName, amount string) (wire.TxAssets, error) {
	if assetName == "" || amount == "" || amount == "0" {
		return nil, nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	amt, err := scommon.NewDecimalFromString(amount, MaxPredictionDecimalPrecision)
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

func resultOutputIsZero(output ResultOutput) bool {
	return output.Value == 0 && len(output.Assets) == 0
}

func cloneResultPlans(plans []ResultPlan) []ResultPlan {
	out := make([]ResultPlan, len(plans))
	for i := range plans {
		out[i] = plans[i]
		out[i].GasFee = plans[i].GasFee.Clone()
		out[i].Inputs = append([]OutPoint(nil), plans[i].Inputs...)
		out[i].Outputs = cloneResultOutputs(plans[i].Outputs)
	}
	return out
}

func cloneResultOutputs(outputs []ResultOutput) []ResultOutput {
	out := make([]ResultOutput, len(outputs))
	for i := range outputs {
		out[i] = outputs[i]
		out[i].Assets = outputs[i].Assets.Clone()
	}
	return out
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

func verifyResultInputOrder(tx *wire.MsgTx, expected []OutPoint) error {
	if tx == nil {
		return fmt.Errorf("missing result transaction")
	}
	if len(tx.TxIn) != len(expected) {
		return fmt.Errorf("result input count mismatch: got %d want %d", len(tx.TxIn), len(expected))
	}
	for i, txIn := range tx.TxIn {
		if txIn == nil {
			return fmt.Errorf("nil result input %d", i)
		}
		actual := WireOutPointToAgent(txIn.PreviousOutPoint)
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
			got.Value != want.Value {
			return fmt.Errorf("result output %d mismatch", i)
		}
		if !got.Assets.Equal(want.Assets) {
			return fmt.Errorf("result output %d assets mismatch", i)
		}
	}
	return nil
}
