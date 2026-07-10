package main

import (
	"strings"
	"testing"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractnode "github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestAgentConfirmFundingUnavailableError(t *testing.T) {
	err := agentConfirmFundingUnavailableError("tc1pcore", "brc20:f:sgas")
	if got, want := err.Error(), "core node tc1pcore has no spendable brc20:f:sgas UTXO for agent invoke gas"; got != want {
		t.Fatalf("unexpected error: got %q want %q", got, want)
	}
	if strings.Contains(err.Error(), "contract funding") {
		t.Fatalf("error must identify core-node gas funding, got %q", err)
	}
}

func TestAgentConfirmFundingOutput(t *testing.T) {
	gasFee := common.NewDecimal(50, 0)
	gasAssetName := contractnode.DefaultGasConfig().GasAssetName
	output, err := agentConfirmFundingOutput(gasFee, gasAssetName)
	if err != nil {
		t.Fatalf("agentConfirmFundingOutput failed: %v", err)
	}
	if output.Value != 0 {
		t.Fatalf("unexpected sats value: got %d want 0", output.Value)
	}
	if len(output.Assets) != 1 {
		t.Fatalf("unexpected asset count: got %d want 1", len(output.Assets))
	}
	asset := output.Assets[0]
	if got, want := asset.Name.String(), gasAssetName; got != want {
		t.Fatalf("unexpected gas asset: got %s want %s", got, want)
	}
	if asset.Amount.Cmp(gasFee) != 0 {
		t.Fatalf("unexpected gas amount: got %s want %s", asset.Amount.String(), gasFee.String())
	}
}

func TestAgentConfirmFundingOutputAllowsEmptyTopUp(t *testing.T) {
	gasAssetName := contractnode.DefaultGasConfig().GasAssetName
	output, err := agentConfirmFundingOutput(nil, gasAssetName)
	if err != nil {
		t.Fatalf("nil top-up should be accepted: %v", err)
	}
	if output.Value != 0 || len(output.Assets) != 0 {
		t.Fatalf("unexpected nil top-up output: value=%d assets=%v", output.Value, output.Assets)
	}
	output, err = agentConfirmFundingOutput(common.NewDecimal(0, 0), gasAssetName)
	if err != nil {
		t.Fatalf("zero top-up should be accepted: %v", err)
	}
	if output.Value != 0 || len(output.Assets) != 0 {
		t.Fatalf("unexpected zero top-up output: value=%d assets=%v", output.Value, output.Assets)
	}
}

func TestAgentConfirmExternalGasFundingIncludesInvokeFee(t *testing.T) {
	if got, err := agentConfirmExternalGasFunding(nil, nil); err != nil || got != nil {
		t.Fatalf("nil fees should not require external gas funding: got=%v err=%v", got, err)
	}
	invokeFee := common.NewDecimal(100, 0)
	if got, err := agentConfirmExternalGasFunding(invokeFee, nil); err != nil || got == nil || got.Cmp(invokeFee) != 0 {
		t.Fatalf("unexpected invoke-only gas funding: got=%v err=%v want=%s", got, err, invokeFee.String())
	}
	topUp := common.NewDecimal(37, 0)
	want := common.NewDecimal(137, 0)
	got, err := agentConfirmExternalGasFunding(invokeFee, topUp)
	if err != nil || got == nil || got.Cmp(want) != 0 {
		t.Fatalf("unexpected invoke plus top-up gas funding: got=%v err=%v want=%s", got, err, want.String())
	}
}

func TestAgentConfirmFundingRequiresGasForInvokeFee(t *testing.T) {
	changeScript := []byte{0x51}
	gasAssetName := contractnode.DefaultGasConfig().GasAssetName
	plain := testAgentConfirmFundingUTXO(1, 1000, changeScript, nil)
	gas := testAgentConfirmFundingUTXO(2, 0, changeScript, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(gasAssetName),
		Amount: *common.NewDecimal(150, 0),
	}})

	selection, err := selectAgentConfirmFundingFromAvailable(
		[]contractcommon.FundingUTXO{plain, gas},
		common.NewDecimal(100, 0),
		gasAssetName,
		changeScript,
		nil,
	)
	if err != nil {
		t.Fatalf("gas invoke fee selection failed: %v", err)
	}
	if len(selection.Inputs) != 1 {
		t.Fatalf("unexpected input count: got %d want 1", len(selection.Inputs))
	}
	if selection.Inputs[0].OutPoint != gas.OutPoint {
		t.Fatalf("unexpected selected input: got %v want %v", selection.Inputs[0].OutPoint, gas.OutPoint)
	}
	if selection.ChangeOutput == nil {
		t.Fatalf("expected change output")
	}
	asset, err := selection.ChangeOutput.Assets.Find(wire.NewAssetNameFromString(gasAssetName))
	if err != nil {
		t.Fatalf("expected gas change asset: %v", err)
	}
	if got, want := asset.Amount.String(), "50"; got != want {
		t.Fatalf("unexpected gas change: got %s want %s", got, want)
	}
}

func TestAgentConfirmFundingRequiresGasForInvokeFeeAndTopUp(t *testing.T) {
	changeScript := []byte{0x51}
	gasAssetName := contractnode.DefaultGasConfig().GasAssetName
	plain := testAgentConfirmFundingUTXO(1, 1000, changeScript, nil)
	gas := testAgentConfirmFundingUTXO(2, 0, changeScript, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(gasAssetName),
		Amount: *common.NewDecimal(100, 0),
	}})

	selection, err := selectAgentConfirmFundingFromAvailable(
		[]contractcommon.FundingUTXO{plain, gas},
		common.NewDecimal(90, 0),
		gasAssetName,
		changeScript,
		nil,
	)
	if err != nil {
		t.Fatalf("gas top-up selection failed: %v", err)
	}
	if len(selection.Inputs) != 1 {
		t.Fatalf("unexpected input count: got %d want 1", len(selection.Inputs))
	}
	if selection.Inputs[0].OutPoint != gas.OutPoint {
		t.Fatalf("unexpected selected input: got %v want %v", selection.Inputs[0].OutPoint, gas.OutPoint)
	}
	if selection.ChangeOutput == nil {
		t.Fatalf("expected gas change output")
	}
	asset, err := selection.ChangeOutput.Assets.Find(wire.NewAssetNameFromString(gasAssetName))
	if err != nil {
		t.Fatalf("expected gas change asset: %v", err)
	}
	if got, want := asset.Amount.String(), "10"; got != want {
		t.Fatalf("unexpected gas change: got %s want %s", got, want)
	}
}

func testAgentConfirmFundingUTXO(index uint32, value int64, script []byte, assets wire.TxAssets) contractcommon.FundingUTXO {
	var hash chainhash.Hash
	hash[0] = byte(index)
	return contractcommon.FundingUTXO{
		OutPoint: wire.OutPoint{
			Hash:  hash,
			Index: index,
		},
		OutValue: wire.TxOut{
			Value:    value,
			Assets:   assets.Clone(),
			PkScript: append([]byte(nil), script...),
		},
		Height:  int64(index),
		SortKey: hash.String(),
	}
}
