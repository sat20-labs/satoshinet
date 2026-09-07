package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestReviewCloseRemovesOnlyOwnedTriggersAndReverts(t *testing.T) {
	runtime := NewRuntime(nil)
	owner := testContract(t)
	other := runtime.contractAddressFromGeth(GethAddress(mustEVMAddress(t, "0x2222222222222222222222222222222222222222")))
	runtime.SetCode(ContractAddressHash(owner), []byte{0})
	runtime.SetCode(ContractAddressHash(other), []byte{0})
	for _, c := range []ContractAddress{owner, other} {
		for _, id := range []string{"first", "second"} {
			require.NoError(t, runtime.State.RegisterTrigger(Trigger{Contract: c, ID: id, Kind: TriggerAtHeight, Height: 2, GasLimit: DefaultGasConfig().InvokeBaseGas}))
		}
	}
	before := runtime.State.StateRoot()
	snapshot := runtime.State.Snapshot()
	runtime.State.CloseContract(ContractGethAddress(owner))
	require.Len(t, runtime.State.Triggers(), 2)
	for _, trigger := range runtime.State.Triggers() {
		require.True(t, other.Equal(trigger.Contract))
	}
	runtime.State.RevertToSnapshot(snapshot)
	require.Equal(t, before, runtime.State.StateRoot())
	require.Len(t, runtime.State.Triggers(), 4)
	runtime.State.CloseContract(ContractGethAddress(owner))
	_, err := ExecuteBlock(BlockExecutionRequest{Runtime: runtime, Block: testBlockContext(2)})
	require.NoError(t, err, "closed contract must not block the due block")
}

func TestReviewTriggersShareGasBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fundedCalls int64
		blockGas    int64
		wantPending int
	}{
		{"one_call_funded", 1, 0, 1},
		{"both_funded", 2, 0, 0},
		{"block_budget_defers_second", 2, DefaultGasConfig().InvokeBaseGas, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testContract(t)
			runtime := NewRuntime(nil)
			runtime.SetCode(ContractAddressHash(c), []byte{0x5b, 0x60, 0, 0x56}) // consume the complete call budget
			cfg := DefaultGasConfig()
			if tc.blockGas != 0 {
				cfg.MaxGasPerBlock = tc.blockGas
			}
			limit := cfg.InvokeBaseGas
			for _, id := range []string{"first", "second"} {
				require.NoError(t, runtime.State.RegisterTrigger(Trigger{Contract: c, ID: id, Kind: TriggerAtHeight, Height: 2, GasLimit: limit}))
			}
			fee, err := cfg.ContractFundingFee(ExecutionKindTrigger, limit, true, 2)
			require.NoError(t, err)
			amount := fee.Clone()
			if tc.fundedCalls == 2 {
				amount = amount.AddAlignPrecision(fee)
			}
			utxo := mustDecimalUTXO(t, OutPoint{TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, c, cfg.GasAssetName, amount.String(), 1)
			_, err = BuildBlockResultTxs(BlockResultBuildRequest{Runtime: runtime, GasConfig: cfg, Block: testBlockContext(2), ContractUTXOs: func(ContractAddress) ([]UTXO, error) { return []UTXO{utxo}, nil }, ResolveScript: evmTestResultScriptResolver(t, c), ResolveOutput: evmTestResultOutputResolver(c), AssetPrecision: func(string) (int, bool) { return 18, true }})
			require.NoError(t, err)
			require.Len(t, runtime.State.Triggers(), tc.wantPending)
			if tc.wantPending == 1 {
				require.Equal(t, "second", runtime.State.Triggers()[0].ID)
				// A later block with new funding can execute the deferred task.
				utxo.OutPoint.Vout++
				_, err = BuildBlockResultTxs(BlockResultBuildRequest{Runtime: runtime, GasConfig: cfg, Block: testBlockContext(3), ContractUTXOs: func(ContractAddress) ([]UTXO, error) { return []UTXO{utxo}, nil }, ResolveScript: evmTestResultScriptResolver(t, c), ResolveOutput: evmTestResultOutputResolver(c), AssetPrecision: func(string) (int, bool) { return 18, true }})
				require.NoError(t, err)
				require.Empty(t, runtime.State.Triggers())
			}
		})
	}
}

func TestReviewTriggerBudgetIncludesPendingRefundAndTransfer(t *testing.T) {
	for _, refund := range []bool{false, true} {
		c := testContract(t)
		cfg := DefaultGasConfig()
		limit := cfg.InvokeBaseGas
		fee, err := cfg.ContractFundingFee(ExecutionKindTrigger, limit, true, 2)
		require.NoError(t, err)
		utxo := mustDecimalUTXO(t, OutPoint{TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, c, cfg.GasAssetName, fee.String(), 1)
		backend := NewBackend(BlockExecutionRequest{Runtime: NewRuntime(nil), GasConfig: cfg, Block: testBlockContext(2), ContractUTXOs: func(ContractAddress) ([]UTXO, error) { return []UTXO{utxo}, nil }})
		record := ExecutionRecord{Contract: c, Kind: ExecutionKindInvoke, Height: 2, RequiresResult: true}
		if refund {
			record.FundingInputs = []OutPoint{utxo.OutPoint}
			record.GasRefundRecipient = "tb1qrefund"
		} else {
			record.ResultFeeMode = contractframework.ResultFeeModePlainTxFee
			record.AssetIntents = []AssetIntent{{From: c, To: "tb1qdest", AssetName: cfg.GasAssetName, Amount: fee.Clone()}}
		}
		backend.pending = []ExecutionRecord{record}
		ready, err := backend.triggerHasGasBudget(c, limit)
		require.NoError(t, err)
		require.False(t, ready, "pending refund=%v must reserve its funds", refund)
	}
}

func TestReviewTriggerCannotTransferItsGasReserve(t *testing.T) {
	for _, spendReserve := range []bool{true, false} {
		t.Run(map[bool]string{true: "reject_reserved_funds", false: "allow_surplus"}[spendReserve], func(t *testing.T) {
			c := testContract(t)
			cfg := DefaultGasConfig()
			limit := int64(100000)
			fee, err := cfg.ContractFundingFee(ExecutionKindTrigger, limit, true, 2)
			require.NoError(t, err)
			amount := fee.AddAlignPrecision(scommon.NewDefaultDecimal(1))
			utxo := mustDecimalUTXO(t, OutPoint{TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, c, cfg.GasAssetName, amount.String(), 1)
			runtime := NewRuntime(nil)
			runtime.SetCode(ContractAddressHash(c), callAssetPrecompileCode())
			transfer := "1"
			if spendReserve {
				transfer = amount.String()
			}
			require.NoError(t, runtime.State.RegisterTrigger(Trigger{Contract: c, ID: "spend", Kind: TriggerAtHeight, Height: 2, GasLimit: limit, Calldata: EncodeTransferAssetCall(cfg.GasAssetName, "tb1qdest", transfer, nil)}))
			provider := func(ContractAddress) ([]UTXO, error) { return []UTXO{utxo}, nil }
			backend := NewBackend(BlockExecutionRequest{Runtime: runtime, GasConfig: cfg, Block: testBlockContext(2), ContractUTXOs: provider, ResolveResultScript: func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil }, AssetPrecision: func(string) (int, bool) { return 10, true }})
			require.NoError(t, backend.ExecuteTrigger(runtime.DueTriggerCalls(testBlockContext(2))[0]))
			_, err = (contractframework.CanonicalResultPlanner{GasConfig: cfg, UTXOs: provider, Precision: SettlementPrecision(nil)}).BuildPlans(backend.pending)
			require.NoError(t, err, "a transfer must not consume funds reserved for its execution fee")
			if spendReserve {
				require.Empty(t, runtime.AssetIntents)
			} else {
				require.Len(t, runtime.AssetIntents, 1)
				require.Equal(t, "1", runtime.AssetIntents[0].Amount.String())
			}
		})
	}
}

func TestReviewTriggerBudgetUsesSettlementPrecisionAndContractScope(t *testing.T) {
	c := testContract(t)
	cfg := DefaultGasConfig()
	limit := cfg.InvokeBaseGas
	rawFee, err := cfg.ContractFundingFee(ExecutionKindTrigger, limit, true, 2)
	require.NoError(t, err)
	precision := SettlementPrecision(func(string) (int, bool) { return 0, true })
	fee := precision.NormalizeUp(cfg.GasAssetName, rawFee)
	utxo := mustDecimalUTXO(t, OutPoint{TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, c, cfg.GasAssetName, fee.String(), 1)
	backend := NewBackend(BlockExecutionRequest{Runtime: NewRuntime(nil), GasConfig: cfg, Block: testBlockContext(2), ContractUTXOs: func(ContractAddress) ([]UTXO, error) { return []UTXO{utxo}, nil }, AssetPrecision: precision.Resolve})
	other := backend.Runtime.contractAddressFromGeth(GethAddress(mustEVMAddress(t, "0x2222222222222222222222222222222222222222")))
	backend.pending = []ExecutionRecord{{Contract: other, Kind: ExecutionKindTrigger, Height: 2, GasUsed: limit, RequiresResult: true}}
	ready, err := backend.triggerHasGasBudget(c, limit)
	require.NoError(t, err)
	require.True(t, ready, "another contract must not consume this contract's budget")
	backend.pending[0].Contract = c
	ready, err = backend.triggerHasGasBudget(c, limit)
	require.NoError(t, err)
	require.False(t, ready, "each execution fee must round up just as Result settlement does")
}
