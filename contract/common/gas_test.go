package common

import (
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
)

func TestGasPriceNumeratorAtHeight(t *testing.T) {
	if got := GasPriceNumeratorAtHeight(0); got != InitialGasPriceNumerator {
		t.Fatalf("height 0 price got %d want %d", got, InitialGasPriceNumerator)
	}
	if got := GasPriceNumeratorAtHeight(GasPriceDecayInterval); got != 95000000 {
		t.Fatalf("first decay price got %d want 95000000", got)
	}
	if got := GasPriceNumeratorAtHeight(GasPriceDecayInterval * 1000); got != GasPriceFloorNumerator {
		t.Fatalf("floor price got %d want %d", got, GasPriceFloorNumerator)
	}
}

func TestGasFeeDecimalUsesExecutionScale(t *testing.T) {
	fee, err := GasFeeDecimal(3, GasPriceDenominator, GasPriceDenominator)
	if err != nil {
		t.Fatalf("GasFeeDecimal failed: %v", err)
	}
	if got := fee.String(); got != "0.003" {
		t.Fatalf("decimal fee got %s want 0.003", got)
	}
	rounded, err := GasFeeDecimal(3, GasPriceDenominator, GasPriceDenominator)
	if err != nil {
		t.Fatalf("GasFee failed: %v", err)
	}
	r, _ := DecimalCeilUint64(rounded)
	if r != 1 {
		t.Fatalf("compat rounded fee got %d want 1", rounded)
	}
}

func TestGasAssetNameForNet(t *testing.T) {
	if got := GasAssetNameForNet(wire.MainNet); got != MainnetGasAssetName {
		t.Fatalf("mainnet gas asset got %s want %s", got, MainnetGasAssetName)
	}
	if got := GasAssetNameForNet(wire.TestNet); got != TestnetGasAssetName {
		t.Fatalf("testnet gas asset got %s want %s", got, TestnetGasAssetName)
	}
	if got := GasAssetNameForNet(wire.SimNet); got != TestnetGasAssetName {
		t.Fatalf("simnet gas asset got %s want %s", got, TestnetGasAssetName)
	}
	if got := GasAssetNameAtHeight(wire.MainNet, 100); got != MainnetGasAssetName {
		t.Fatalf("mainnet gas asset at height got %s want %s", got, MainnetGasAssetName)
	}
}
