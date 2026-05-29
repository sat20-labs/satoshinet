package evm

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type ContractUTXOProvider func(contract ContractAddress) ([]UTXO, error)
type ResultOutputResolver func(resultTx *wire.MsgTx) ([]ResultOutput, error)

type CanonicalResultVerifier struct {
	GasConfig     GasConfig
	UTXOs         ContractUTXOProvider
	ResolveOutput ResultOutputResolver
}

func (v CanonicalResultVerifier) Verify(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
	plans, err := v.BuildPlans(settled)
	if err != nil {
		return err
	}
	expectedInputs := make([]OutPoint, 0)
	expectedOutputs := make([]ResultOutput, 0)
	for _, plan := range plans {
		for _, input := range plan.Inputs {
			expectedInputs = append(expectedInputs, input.OutPoint)
		}
		expectedOutputs = append(expectedOutputs, plan.Outputs...)
	}
	if len(expectedInputs) > 0 {
		if err := verifyResultInputOrder(resultTx, expectedInputs); err != nil {
			return err
		}
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

func (v CanonicalResultVerifier) BuildPlans(settled []ExecutionRecord) ([]ResultPlan, error) {
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
		key := resultSpendContractKey(record.Contract)
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
	if v.UTXOs == nil {
		return nil, fmt.Errorf("missing contract UTXO provider")
	}
	if v.GasConfig.GasAssetName == "" {
		return nil, fmt.Errorf("missing gas asset name")
	}

	plans := make([]ResultPlan, 0, len(groups))
	for _, group := range groups {
		available, err := v.UTXOs(group.Contract)
		if err != nil {
			return nil, err
		}
		intents := make([]AssetIntent, 0)
		gasFee := uint64(0)
		funding := make([]OutPoint, 0)
		for _, record := range group.Records {
			intents = append(intents, record.AssetIntents...)
			callFee, err := v.GasConfig.CheckedCallFeeAtHeight(
				v.GasConfig.ResultExecutionGas(record),
				record.Height,
			)
			if err != nil {
				return nil, err
			}
			next, overflow := addUint64(gasFee, callFee)
			if overflow {
				return nil, fmt.Errorf("gas fee overflows uint64")
			}
			gasFee = next
			resultFee, err := v.GasConfig.CheckedResultBaseFee(record.Height)
			if err != nil {
				return nil, err
			}
			next, overflow = addUint64(gasFee, resultFee)
			if overflow {
				return nil, fmt.Errorf("result packing fee overflows uint64")
			}
			gasFee = next
			funding = append(funding, record.FundingInputs...)
		}
		plan, err := BuildCanonicalResultPlan(ResultPlanRequest{
			Contract:               group.Contract,
			Available:              available,
			Intents:                intents,
			GasAssetName:           v.GasConfig.GasAssetName,
			GasFee:                 gasFee,
			RequiredGasFundingUTXO: funding,
		})
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

type canonicalRecordGroup struct {
	Contract ContractAddress
	Records  []ExecutionRecord
}

func verifyResultInputOrder(resultTx *wire.MsgTx, expected []OutPoint) error {
	if len(resultTx.TxIn) != len(expected) {
		return fmt.Errorf("result input count mismatch: got %d want %d", len(resultTx.TxIn), len(expected))
	}
	for i, txIn := range resultTx.TxIn {
		actual := WireOutPointToEVM(txIn.PreviousOutPoint)
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
			!resultOutputAssetsEqual(got.Assets, want.Assets) ||
			string(got.ExtraData) != string(want.ExtraData) {
			return fmt.Errorf("result output %d mismatch: got %+v want %+v", i, got, want)
		}
	}
	return nil
}
