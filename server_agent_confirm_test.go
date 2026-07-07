package main

import (
	"testing"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractnode "github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/wire"
)

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

func TestAgentConfirmExternalGasFundingUsesOnlyTopUp(t *testing.T) {
	if got := agentConfirmExternalGasFunding(nil); got != nil {
		t.Fatalf("nil top-up should not require external gas funding: %s", got.String())
	}
	if got := agentConfirmExternalGasFunding(common.NewDecimal(0, 0)); got != nil {
		t.Fatalf("zero top-up should not require external gas funding: %s", got.String())
	}
	topUp := common.NewDecimal(37, 0)
	got := agentConfirmExternalGasFunding(topUp)
	if got == nil || got.Cmp(topUp) != 0 {
		t.Fatalf("unexpected external gas funding: got %v want %s", got, topUp.String())
	}
}

func TestAgentConfirmFundingAllowsPlainAnchorWithoutGasTopUp(t *testing.T) {
	changeScript := []byte{0x51}
	plain := testAgentConfirmFundingUTXO(1, 1000, changeScript, nil)

	selection, err := selectAgentConfirmFundingFromAvailable(
		[]contractcommon.FundingUTXO{plain},
		nil,
		contractnode.DefaultGasConfig().GasAssetName,
		changeScript,
		nil,
	)
	if err != nil {
		t.Fatalf("plain anchor should be accepted without gas top-up: %v", err)
	}
	if len(selection.Inputs) != 1 {
		t.Fatalf("unexpected input count: got %d want 1", len(selection.Inputs))
	}
	if selection.Inputs[0].OutPoint != plain.OutPoint {
		t.Fatalf("unexpected selected input: got %v want %v", selection.Inputs[0].OutPoint, plain.OutPoint)
	}
	if selection.ChangeOutput == nil {
		t.Fatalf("expected change output")
	}
	if selection.ChangeOutput.Value != plain.OutValue.Value {
		t.Fatalf("unexpected change value: got %d want %d", selection.ChangeOutput.Value, plain.OutValue.Value)
	}
	if len(selection.ChangeOutput.Assets) != 0 {
		t.Fatalf("plain anchor should not create gas change: %v", selection.ChangeOutput.Assets)
	}
}

func TestAgentConfirmFundingRequiresGasOnlyForTopUp(t *testing.T) {
	changeScript := []byte{0x51}
	gasAssetName := contractnode.DefaultGasConfig().GasAssetName
	plain := testAgentConfirmFundingUTXO(1, 1000, changeScript, nil)
	gas := testAgentConfirmFundingUTXO(2, 0, changeScript, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(gasAssetName),
		Amount: *common.NewDecimal(100, 0),
	}})

	selection, err := selectAgentConfirmFundingFromAvailable(
		[]contractcommon.FundingUTXO{plain, gas},
		common.NewDecimal(50, 0),
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
	if got, want := asset.Amount.String(), "50"; got != want {
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
