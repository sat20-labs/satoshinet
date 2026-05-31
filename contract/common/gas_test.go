package common

import "testing"

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

func TestGasFeeAtHeightRoundsUp(t *testing.T) {
	fee, err := GasFee(3, 1, 2)
	if err != nil {
		t.Fatalf("GasFee failed: %v", err)
	}
	if fee != 2 {
		t.Fatalf("rounded fee got %d want 2", fee)
	}
}

func TestGasAssetNameForSwitchHeight(t *testing.T) {
	if got := GasAssetNameForSwitchHeight(100, 0); got != LegacyGasAssetName {
		t.Fatalf("disabled switch got %s want %s", got, LegacyGasAssetName)
	}
	if got := GasAssetNameForSwitchHeight(99, 100); got != LegacyGasAssetName {
		t.Fatalf("before switch got %s want %s", got, LegacyGasAssetName)
	}
	if got := GasAssetNameForSwitchHeight(100, 100); got != NewGasAssetName {
		t.Fatalf("at switch got %s want %s", got, NewGasAssetName)
	}
	if got := GasAssetNameForSwitchHeight(101, 100); got != NewGasAssetName {
		t.Fatalf("after switch got %s want %s", got, NewGasAssetName)
	}
}
