package evm

import (
	"errors"
	"math"
	"testing"
)

func TestSplitGasFunding(t *testing.T) {
	miner, remainder, err := SplitGasFunding(100, 30, 7)
	if err != nil {
		t.Fatal(err)
	}
	if miner != 37 || remainder != 63 {
		t.Fatalf("got miner=%d remainder=%d", miner, remainder)
	}
}

func TestSplitGasFundingInsufficient(t *testing.T) {
	_, _, err := SplitGasFunding(10, 30, 7)
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("got %v want ErrInsufficientFunds", err)
	}
}

func TestGasConfigRequiredInvokeFunding(t *testing.T) {
	cfg := GasConfig{GasAssetName: "gas", ResultBaseGas: 5}
	fee, err := cfg.CheckedRequiredInvokeFundingDecimal(10, true)
	if err != nil {
		t.Fatalf("CheckedRequiredInvokeFundingDecimal failed: %v", err)
	}
	if got := fee.String(); got != "0.015" {
		t.Fatalf("got %s want 0.015", got)
	}
	if got := cfg.RequiredInvokeFunding(10, false); got != 1 {
		t.Fatalf("compat rounded fee got %d want 1", got)
	}
}

func TestGasConfigRejectsFeeOverflow(t *testing.T) {
	cfg := GasConfig{
		GasAssetName:             "gas",
		GasPriceDenominator:      1,
		InitialGasPriceNumerator: math.MaxUint64,
		GasPriceDecayInterval:    1,
		GasPriceDecayNumerator:   1,
		GasPriceDecayDenominator: 1,
		GasPriceFloorNumerator:   1,
	}
	_, err := cfg.CheckedCallFeeAtHeight(1001, 0)
	if err == nil {
		t.Fatal("expected overflow")
	}
}

func TestSplitGasFundingRejectsOverflow(t *testing.T) {
	_, _, err := SplitGasFunding(math.MaxUint64, math.MaxUint64, 1)
	if err == nil {
		t.Fatal("expected overflow")
	}
}
