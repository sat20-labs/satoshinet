package framework

import (
	"bytes"
	"fmt"
	"math"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultVerifyRequest struct {
	ResultTxs []*wire.MsgTx
	Prefix    string
}

type ResultVerifier func(resultTx *wire.MsgTx, settled []ExecutionRecord) error

type PendingResultVerifyRequest struct {
	Label    string
	ResultTx *wire.MsgTx
	Payload  contract.ResultPayload
	Pending  []ExecutionRecord
	Verify   ResultVerifier
}

func ResultPayloadFromTx(tx *wire.MsgTx, label string) (contract.ResultPayload, error) {
	if tx == nil {
		return contract.ResultPayload{}, fmt.Errorf("missing result transaction")
	}
	var payload *contract.ResultPayload
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return contract.ResultPayload{}, fmt.Errorf("nil output %d", i)
		}
		if !txscript.IsUnspendable(txOut.PkScript) {
			continue
		}
		p, err := contract.ReadResultNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if payload != nil {
			return contract.ResultPayload{}, fmt.Errorf("multiple %s RESULT payloads", label)
		}
		payload = &p
	}
	if payload == nil {
		return contract.ResultPayload{}, fmt.Errorf("missing %s RESULT payload", label)
	}
	return *payload, nil
}

func VerifyResultAgainstPending(req PendingResultVerifyRequest) ([]ExecutionRecord, error) {
	count := int(req.Payload.ResultCount)
	if count == 0 {
		return req.Pending, fmt.Errorf("result count is zero")
	}
	if count > len(req.Pending) {
		return req.Pending, fmt.Errorf("result count %d exceeds pending executions %d", count, len(req.Pending))
	}
	settled := req.Pending[:count]
	if err := ValidateResultStatus(req.Payload, settled); err != nil {
		return req.Pending, err
	}
	if err := ValidateResultFundingInputs(req.ResultTx, settled, req.Label); err != nil {
		return req.Pending, err
	}
	if req.Verify != nil {
		if err := req.Verify(req.ResultTx, CloneExecutionRecords(settled)); err != nil {
			return req.Pending, err
		}
	}
	return req.Pending[count:], nil
}

func ValidateResultStatus(result contract.ResultPayload, settled []ExecutionRecord) error {
	if len(settled) == 1 && result.Status != settled[0].Status {
		return fmt.Errorf("result status %d does not match execution status %d",
			result.Status, settled[0].Status)
	}
	return nil
}

func ValidateResultFundingInputs(tx *wire.MsgTx, settled []ExecutionRecord, label string) error {
	if tx == nil {
		return fmt.Errorf("missing result transaction")
	}
	inputs := make(map[OutPoint]struct{}, len(tx.TxIn))
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			continue
		}
		inputs[WireOutPointToFramework(txIn.PreviousOutPoint)] = struct{}{}
	}
	if label == "" {
		label = "CONTRACT_RESULT"
	}
	for _, record := range settled {
		for _, funding := range record.FundingInputs {
			if _, ok := inputs[funding]; !ok {
				return fmt.Errorf("%s missing execution funding input %s", label, funding)
			}
		}
	}
	return nil
}

type SingleResultTxVerifyRequest struct {
	Label        string
	ResultTxs    []*wire.MsgTx
	Expectations []ResultPlan
	Verify       func(*wire.MsgTx, []ResultPlan) error
}

type CanonicalResultVerifyRequest struct {
	Label        string
	ResultTx     *wire.MsgTx
	Status       contract.ResultStatus
	Plans        []ResultPlan
	GasAssetName string
	Resolve      ResultOutputResolver
	PlanCount    func(ResultPlan) int
	UseInputUTXO bool
	CheckPayload bool
}

func VerifySingleResultTx(req SingleResultTxVerifyRequest) error {
	plans := CloneResultPlans(req.Expectations)
	if len(plans) == 0 {
		if len(req.ResultTxs) != 0 {
			return fmt.Errorf("unexpected %s RESULT transactions", req.Label)
		}
		return nil
	}
	if len(req.ResultTxs) != 1 {
		return fmt.Errorf("%s result transaction count mismatch: got %d want 1", req.Label, len(req.ResultTxs))
	}
	if req.Verify == nil {
		return fmt.Errorf("missing %s result verifier", req.Label)
	}
	return req.Verify(req.ResultTxs[0], plans)
}

func VerifyCanonicalResultTx(req CanonicalResultVerifyRequest) error {
	if req.CheckPayload {
		payload, err := ResultPayloadFromTx(req.ResultTx, req.Label)
		if err != nil {
			return err
		}
		if payload.Status != req.Status {
			return fmt.Errorf("result status mismatch: got %d want %d", payload.Status, req.Status)
		}
		expectedCount := 0
		for _, plan := range req.Plans {
			expectedCount += canonicalResultPlanCount(plan, req.PlanCount)
		}
		if expectedCount > math.MaxUint16 || payload.ResultCount != uint16(expectedCount) {
			return fmt.Errorf("result count mismatch: got %d want %d", payload.ResultCount, expectedCount)
		}
	}

	expectedInputs := make([]OutPoint, 0)
	expectedUTXOs := make([]UTXO, 0)
	expectedOutputs := make([]ResultOutput, 0)
	for _, plan := range req.Plans {
		if req.UseInputUTXO {
			for _, input := range plan.InputUTXOs {
				expectedInputs = append(expectedInputs, input.OutPoint)
				expectedUTXOs = append(expectedUTXOs, input.Clone())
			}
		} else {
			expectedInputs = append(expectedInputs, plan.Inputs...)
			expectedUTXOs = append(expectedUTXOs, resultPlanInputUTXOs(plan)...)
		}
		expectedOutputs = append(expectedOutputs, plan.Outputs...)
	}
	if len(expectedInputs) != 0 {
		if err := VerifyResultInputsContain(req.ResultTx, expectedInputs); err != nil {
			return err
		}
	}
	if len(expectedUTXOs) != 0 {
		if err := VerifyResultInputCoverage(expectedUTXOs, expectedOutputs,
			resultPlansGasFee(req.Plans), req.GasAssetName); err != nil {
			return err
		}
	}
	if req.Resolve != nil {
		actualOutputs, err := req.Resolve(req.ResultTx)
		if err != nil {
			return err
		}
		if err := VerifyResultOutputs(actualOutputs, expectedOutputs); err != nil {
			return err
		}
	}
	return nil
}

func canonicalResultPlanCount(plan ResultPlan, count func(ResultPlan) int) int {
	if count != nil {
		return count(plan)
	}
	return 1
}

func resultPlanInputUTXOs(plan ResultPlan) []UTXO {
	if len(plan.InputUTXOs) == 0 || len(plan.Inputs) == 0 {
		return nil
	}
	want := make(map[OutPoint]struct{}, len(plan.Inputs))
	for _, input := range plan.Inputs {
		want[input] = struct{}{}
	}
	out := make([]UTXO, 0, len(plan.InputUTXOs))
	seen := make(map[OutPoint]struct{}, len(plan.InputUTXOs))
	for _, utxo := range plan.InputUTXOs {
		if _, ok := want[utxo.OutPoint]; !ok {
			continue
		}
		if _, ok := seen[utxo.OutPoint]; ok {
			continue
		}
		seen[utxo.OutPoint] = struct{}{}
		out = append(out, utxo.Clone())
	}
	return out
}

func resultPlansGasFee(plans []ResultPlan) *scommon.Decimal {
	total := ZeroDecimal()
	for _, plan := range plans {
		if plan.GasFee == nil || plan.GasFee.Sign() == 0 {
			continue
		}
		total = total.AddAlignPrecision(plan.GasFee)
	}
	return total
}

// VerifyResultInputsContain checks only that the result spends the inputs the
// execution plan depends on. Extra inputs are allowed because they do not change
// contract semantics; output verification remains strict.
func VerifyResultInputsContain(tx *wire.MsgTx, expected []OutPoint) error {
	if tx == nil {
		return fmt.Errorf("missing result transaction")
	}
	actual := make(map[OutPoint]struct{}, len(tx.TxIn))
	for i, txIn := range tx.TxIn {
		if txIn == nil {
			return fmt.Errorf("nil result input %d", i)
		}
		actual[WireOutPointToFramework(txIn.PreviousOutPoint)] = struct{}{}
	}
	for _, expectedInput := range UniqueOutPoints(expected) {
		if _, ok := actual[expectedInput]; !ok {
			return fmt.Errorf("missing result input %s", expectedInput)
		}
	}
	return nil
}

func VerifyResultInputCoverage(inputs []UTXO, outputs []ResultOutput, gasFee *scommon.Decimal,
	gasAssetName string) error {

	var inputValue int64
	var inputAssets wire.TxAssets
	seen := make(map[OutPoint]struct{}, len(inputs))
	for _, input := range inputs {
		if _, ok := seen[input.OutPoint]; ok {
			continue
		}
		seen[input.OutPoint] = struct{}{}
		nextValue, overflow := AddInt64(inputValue, input.Value)
		if overflow {
			return fmt.Errorf("result input value overflows int64")
		}
		inputValue = nextValue
		if len(input.Assets) != 0 {
			if err := inputAssets.Merge(input.Assets); err != nil {
				return err
			}
		}
	}

	requiredValue := resultOutputsValue(outputs)
	requiredAssets, err := resultOutputsAssets(outputs)
	if err != nil {
		return err
	}
	if gasFee != nil && gasFee.Sign() > 0 && gasAssetName != "" {
		if gasAssetName == contract.SatoshiAssetName {
			feeValue, err := DecimalToInt64(*gasFee)
			if err != nil {
				return err
			}
			nextValue, overflow := AddInt64(requiredValue, feeValue)
			if overflow {
				return fmt.Errorf("result required value overflows int64")
			}
			requiredValue = nextValue
		} else {
			gasAssets, err := NewAssetSet(gasAssetName, gasFee)
			if err != nil {
				return err
			}
			if len(gasAssets) != 0 {
				if err := requiredAssets.Merge(gasAssets); err != nil {
					return err
				}
			}
		}
	}

	if inputValue < requiredValue {
		return fmt.Errorf("result inputs spend %d sats but %d sats are required", inputValue, requiredValue)
	}
	if len(requiredAssets) != 0 {
		availableAssets := inputAssets.Clone()
		if err := availableAssets.Split(requiredAssets); err != nil {
			return fmt.Errorf("result inputs do not cover required assets: %w", err)
		}
	}
	return nil
}

func VerifyResultInputOrder(tx *wire.MsgTx, expected []OutPoint) error {
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
		actual := WireOutPointToFramework(txIn.PreviousOutPoint)
		if actual != expected[i] {
			return fmt.Errorf("result input %d mismatch: got %s want %s", i, actual, expected[i])
		}
	}
	return nil
}

func VerifyResultOutputs(actual, expected []ResultOutput) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("result output count mismatch: got %d want %d", len(actual), len(expected))
	}
	for i := range expected {
		got := NormalizeResultOutput(actual[i])
		want := NormalizeResultOutput(expected[i])
		if got.To != want.To || got.Value != want.Value || !bytes.Equal(got.ExtraData, want.ExtraData) {
			return fmt.Errorf("result output %d mismatch: got %+v want %+v", i, got, want)
		}
		if !got.Assets.Equal(want.Assets) {
			return fmt.Errorf("result output %d assets mismatch: got %+v want %+v", i, got.Assets, want.Assets)
		}
	}
	return nil
}
