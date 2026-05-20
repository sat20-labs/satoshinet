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
	cfg := GasConfig{GasAssetName: "gas", FixedGasPrice: 2, ResultPackingFee: 5}
	if got := cfg.RequiredInvokeFunding(10, true); got != 25 {
		t.Fatalf("got %d want 25", got)
	}
	if got := cfg.RequiredInvokeFunding(10, false); got != 20 {
		t.Fatalf("got %d want 20", got)
	}
}

func TestGasConfigRejectsFeeOverflow(t *testing.T) {
	cfg := GasConfig{GasAssetName: "gas", FixedGasPrice: math.MaxUint64}
	_, err := cfg.CheckedCallFee(2)
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
