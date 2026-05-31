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

func TestGasFeeAtHeightRoundsUp(t *testing.T) {
	fee, err := GasFee(3, 1, 2)
	if err != nil {
		t.Fatalf("GasFee failed: %v", err)
	}
	if fee != 2 {
		t.Fatalf("rounded fee got %d want 2", fee)
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
