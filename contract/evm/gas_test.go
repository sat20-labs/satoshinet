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
	cfg := GasConfig{GasAssetName: "gas", InvokeBaseGas: 10, ResultBaseGas: 5}
	fee, err := cfg.CheckedRequiredInvokeFundingDecimal(10, true)
	if err != nil {
		t.Fatalf("CheckedRequiredInvokeFundingDecimal failed: %v", err)
	}
	if got := fee.String(); got != "0.015" {
		t.Fatalf("got %s want 0.015", got)
	}
	if got := cfg.RequiredInvokeFundingDecimal(10, false); got.String() != "0.01" {
		t.Fatalf("compat rounded fee got %s want 0.01", got)
	}
}

func TestGasConfigFundingFeeBreakdown(t *testing.T) {
	cfg := GasConfig{GasAssetName: "gas", FixedGasPrice: 1, InvokeBaseGas: 20, ResultBaseGas: 10}
	base, err := cfg.BaseNetworkFee(ExecutionKindInvoke, 0)
	if err != nil {
		t.Fatalf("BaseNetworkFee failed: %v", err)
	}
	if got := base.String(); got != "0.02" {
		t.Fatalf("base fee got %s want 0.02", got)
	}
	escrow, err := cfg.ExecutionEscrowFee(ExecutionKindInvoke, 100, 0)
	if err != nil {
		t.Fatalf("ExecutionEscrowFee failed: %v", err)
	}
	if got := escrow.String(); got != "0.08" {
		t.Fatalf("execution escrow got %s want 0.08", got)
	}
	funding, err := cfg.ContractFundingFee(ExecutionKindInvoke, 100, true, 0)
	if err != nil {
		t.Fatalf("ContractFundingFee failed: %v", err)
	}
	if got := funding.String(); got != "0.09" {
		t.Fatalf("contract funding got %s want 0.09", got)
	}
	total, err := cfg.TotalUserBudgetFee(ExecutionKindInvoke, 100, true, 0)
	if err != nil {
		t.Fatalf("TotalUserBudgetFee failed: %v", err)
	}
	if got := total.String(); got != "0.11" {
		t.Fatalf("total budget got %s want 0.11", got)
	}
}

func TestDefaultGasConfigSetsExecutionLimits(t *testing.T) {
	cfg := DefaultGasConfig()
	if cfg.MaxGasPerInvoke != 50_000_000 {
		t.Fatalf("MaxGasPerInvoke got %d want 50000000", cfg.MaxGasPerInvoke)
	}
	if cfg.MaxGasPerTrigger != 5_000_000 {
		t.Fatalf("MaxGasPerTrigger got %d want 5000000", cfg.MaxGasPerTrigger)
	}
	if cfg.MaxGasPerTrigger > cfg.MaxGasPerInvoke {
		t.Fatalf("trigger limit %d exceeds invoke limit %d", cfg.MaxGasPerTrigger, cfg.MaxGasPerInvoke)
	}
	if cfg.MaxGasPerInvoke < cfg.DeployBaseGas {
		t.Fatalf("invoke limit %d below deploy base gas %d", cfg.MaxGasPerInvoke, cfg.DeployBaseGas)
	}
}

func TestValidateTriggerGasLimit(t *testing.T) {
	cfg := GasConfig{MaxGasPerTrigger: 10}
	if err := ValidateTriggerGasLimit(10, cfg); err != nil {
		t.Fatalf("valid trigger gas limit failed: %v", err)
	}
	if err := ValidateTriggerGasLimit(0, cfg); err == nil {
		t.Fatal("zero trigger gas limit should fail")
	}
	if err := ValidateTriggerGasLimit(-1, cfg); err == nil {
		t.Fatal("negative trigger gas limit should fail")
	}
	if err := ValidateTriggerGasLimit(11, cfg); err == nil {
		t.Fatal("over-limit trigger gas limit should fail")
	}
}

func TestSplitGasFundingRejectsOverflow(t *testing.T) {
	_, _, err := SplitGasFunding(math.MaxUint64, math.MaxUint64, 1)
	if err == nil {
		t.Fatal("expected overflow")
	}
}
