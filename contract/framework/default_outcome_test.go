package framework

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDefaultOutcomeMustBindExactlyOneFundingOutput(t *testing.T) {
	addr := testContractAddress(t, ModuleEVM, 1)
	other := testContractAddress(t, ModuleEVM, 2)
	call := contract.Tx{
		TxID: "source", Contract: addr, GasLimit: 100,
		Funding: []contract.FundingOutput{{OutPoint: contract.TxOutPoint{TxID: "source", Vout: 3}, Contract: addr}},
	}
	valid := func() ExecutionOutcome {
		return ExecutionOutcome{
			Kind: ExecutionKindInvoke, Type: contract.TxTypeInvoke, TxID: "source", Contract: addr,
			CallID: "call", FundingInputs: []OutPoint{{TxID: "source", Vout: 3}}, RequiresResult: true,
		}
	}
	require.NoError(t, validateDefaultOutcome(call, valid()))
	cases := []struct {
		name   string
		mutate func(*ExecutionOutcome)
	}{
		{"wrong_transaction", func(o *ExecutionOutcome) { o.TxID = "other" }},
		{"wrong_target", func(o *ExecutionOutcome) { o.Contract = other }},
		{"wrong_vout", func(o *ExecutionOutcome) { o.FundingInputs[0].Vout++ }},
		{"foreign_funding", func(o *ExecutionOutcome) { o.FundingInputs[0].TxID = "other" }},
		{"no_funding", func(o *ExecutionOutcome) { o.FundingInputs = nil }},
		{"duplicate_funding", func(o *ExecutionOutcome) { o.FundingInputs = append(o.FundingInputs, o.FundingInputs[0]) }},
		{"no_result", func(o *ExecutionOutcome) { o.RequiresResult = false }},
		{"no_call_id", func(o *ExecutionOutcome) { o.CallID = "" }},
		{"wrong_kind", func(o *ExecutionOutcome) { o.Kind = ExecutionKindDeploy }},
		{"implicit_close", func(o *ExecutionOutcome) { o.CloseContract = true }},
		{"negative_gas", func(o *ExecutionOutcome) { o.GasUsed = -1 }},
		{"gas_over_budget", func(o *ExecutionOutcome) { o.GasUsed = call.GasLimit + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := valid()
			tc.mutate(&outcome)
			require.ErrorIs(t, validateDefaultOutcome(call, outcome), ErrAccountingInvariant)
		})
	}
}

// Unused backend methods are deliberately not supplied: reaching Deploy,
// Invoke, or Finalize from this default-call test would itself be a failure.
type defaultSuccessOnRejectBackend struct {
	Backend
	typ          byte
	balance      contract.ManagedBalance
	defaultCalls int
	rejectCalls  int
}

func (b *defaultSuccessOnRejectBackend) Name() string { return "test" }
func (b *defaultSuccessOnRejectBackend) ContractType() byte { return b.typ }
func (b *defaultSuccessOnRejectBackend) Lifecycle(contract.ContractAddress) (contract.ContractLifecycle, bool, error) {
	return contract.ContractLifecycle{Deployer: "actor", Closed: true}, true, nil
}
func (b *defaultSuccessOnRejectBackend) DefaultInvoke(ExecutionContext, contract.Tx, contract.FundingOutput) (ExecutionOutcome, bool, error) {
	b.defaultCalls++
	return ExecutionOutcome{}, false, nil
}
func (b *defaultSuccessOnRejectBackend) RejectFunding(ctx ExecutionContext, call contract.Tx) (ExecutionOutcome, error) {
	b.rejectCalls++
	funding := call.Funding[0].OutPoint
	return ExecutionOutcome{
		Kind: ExecutionKindInvoke, Type: contract.TxTypeInvoke, TxID: call.TxID, Contract: call.Contract,
		CallID: "bad-success", Status: contract.ResultStatusSuccess, RequiresResult: true,
		FundingInputs: []OutPoint{{TxID: funding.TxID, Vout: funding.Vout}},
	}, nil
}
func (b *defaultSuccessOnRejectBackend) ManagedBalance(contract.ContractAddress) (*contract.ManagedBalance, bool) {
	return &b.balance, true
}

func TestDefaultLifecycleCannotBeOverriddenByBackendRefundResult(t *testing.T) {
	for _, typ := range []ModuleType{ModuleTemplate, ModuleEVM, ModuleAgent, 4} {
		addr := testContractAddress(t, typ, 1)
		script, err := contract.ContractPkScript(addr)
		require.NoError(t, err)
		raw := wire.NewMsgTx(2)
		raw.AddTxIn(&wire.TxIn{})
		raw.AddTxOut(wire.NewTxOut(10, nil, script))
		backend := &defaultSuccessOnRejectBackend{typ: byte(typ)}
		executor := NewExecutor(ExecutorConfig{
			Backend: backend,
			ResolveActor: func(*wire.MsgTx, contract.Tx) (string, error) { return "actor", nil },
		})
		err = executor.ExecuteParsedTx(raw, ParsedTx{})
		require.ErrorIs(t, err, ErrAccountingInvariant)
		require.Zero(t, backend.defaultCalls)
		require.Equal(t, 1, backend.rejectCalls)
		require.True(t, backend.balance.IsZero())
	}
}
