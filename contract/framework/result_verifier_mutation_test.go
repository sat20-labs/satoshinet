package framework

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestVerifyCanonicalResultTxRejectsTransactionMutations(t *testing.T) {
	inputs := []OutPoint{
		{TxID: "0100000000000000000000000000000000000000000000000000000000000000", Vout: 1},
		{TxID: "0200000000000000000000000000000000000000000000000000000000000000", Vout: 2},
	}
	plan := ResultPlan{Contract: "test", Inputs: inputs}
	valid, err := BuildResultTx(ResultTxBuildRequest{
		Status:      contract.ResultStatusSuccess,
		ResultCount: 1,
		Plans:       []ResultPlan{plan},
	}, ResultTxBuildOptions{})
	require.NoError(t, err)
	verify := func(tx *wire.MsgTx) error {
		return VerifyCanonicalResultTx(CanonicalResultVerifyRequest{
			Label:        "test",
			ResultTx:     tx,
			Status:       contract.ResultStatusSuccess,
			Plans:        []ResultPlan{plan},
			CheckPayload: true,
		})
	}
	require.NoError(t, verify(valid))

	mutations := map[string]func(*wire.MsgTx){
		"extra input": func(tx *wire.MsgTx) {
			tx.TxIn = append(tx.TxIn, tx.TxIn[0])
		},
		"input order": func(tx *wire.MsgTx) {
			tx.TxIn[0], tx.TxIn[1] = tx.TxIn[1], tx.TxIn[0]
		},
		"duplicate input": func(tx *wire.MsgTx) {
			tx.TxIn[1].PreviousOutPoint = tx.TxIn[0].PreviousOutPoint
		},
		"version":    func(tx *wire.MsgTx) { tx.Version++ },
		"locktime":   func(tx *wire.MsgTx) { tx.LockTime = 1 },
		"sequence":   func(tx *wire.MsgTx) { tx.TxIn[0].Sequence-- },
		"script sig": func(tx *wire.MsgTx) { tx.TxIn[0].SignatureScript = []byte{1} },
		"witness":    func(tx *wire.MsgTx) { tx.TxIn[0].Witness = wire.TxWitness{[]byte{1}} },
		"extra op return": func(tx *wire.MsgTx) {
			tx.TxOut = append([]*wire.TxOut{wire.NewTxOut(0, nil, []byte{0x6a})}, tx.TxOut...)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			tx := valid.Copy()
			mutate(tx)
			require.Error(t, verify(tx))
		})
	}
}

func TestAggregateResultStatus(t *testing.T) {
	tests := []struct {
		name   string
		status []contract.ResultStatus
		expect contract.ResultStatus
	}{
		{name: "success", status: []contract.ResultStatus{contract.ResultStatusSuccess}, expect: contract.ResultStatusSuccess},
		{name: "revert", status: []contract.ResultStatus{contract.ResultStatusSuccess, contract.ResultStatusRevert}, expect: contract.ResultStatusRevert},
		{name: "out of gas", status: []contract.ResultStatus{contract.ResultStatusRevert, contract.ResultStatusOutOfGas}, expect: contract.ResultStatusOutOfGas},
		{name: "invalid", status: []contract.ResultStatus{contract.ResultStatusOutOfGas, contract.ResultStatusInvalid}, expect: contract.ResultStatusInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := make([]ExecutionRecord, len(test.status))
			for i, status := range test.status {
				records[i].Status = status
			}
			require.Equal(t, test.expect, AggregateResultStatus(records))
		})
	}
}

func TestValidateResultStatusRejectsErrorDigest(t *testing.T) {
	payload := contract.ResultPayload{
		Status:       contract.ResultStatusSuccess,
		ResultCount:  1,
		HasErrorInfo: true,
		ErrorDigest:  [32]byte{1},
	}
	err := ValidateResultStatus(payload, []ExecutionRecord{{Status: contract.ResultStatusSuccess}})
	require.ErrorContains(t, err, "error digest")
}

func TestVerifyCanonicalResultTxUsesExactSerialization(t *testing.T) {
	input := OutPoint{TxID: "0100000000000000000000000000000000000000000000000000000000000000", Vout: 1}
	plan := ResultPlan{
		Contract: "test",
		Inputs:   []OutPoint{input},
		Outputs:  []ResultOutput{{To: "recipient", Value: 1}},
	}
	resolve := func(ResultOutput) ([]byte, error) { return []byte{txscript.OP_TRUE}, nil }
	valid, err := BuildResultTx(ResultTxBuildRequest{
		Status:        contract.ResultStatusSuccess,
		ResultCount:   1,
		Plans:         []ResultPlan{plan},
		ResolveScript: resolve,
	}, ResultTxBuildOptions{})
	require.NoError(t, err)
	verify := func(tx *wire.MsgTx) error {
		return VerifyCanonicalResultTx(CanonicalResultVerifyRequest{
			Label:         "test",
			ResultTx:      tx,
			Status:        contract.ResultStatusSuccess,
			Plans:         []ResultPlan{plan},
			ResolveScript: resolve,
			CheckPayload:  true,
		})
	}
	require.NoError(t, verify(valid))

	mutated := valid.Copy()
	mutated.TxOut[0].PkScript = []byte{txscript.OP_2}
	require.ErrorContains(t, verify(mutated), "non-canonical")
}

func TestSingleResultPolicyRejectsSemanticScriptSubstitution(t *testing.T) {
	plan := ResultPlan{
		Contract: "test",
		Outputs:  []ResultOutput{{To: "recipient", Value: 1}},
	}
	resolveScript := func(ResultOutput) ([]byte, error) {
		return []byte{txscript.OP_TRUE}, nil
	}
	resolveOutput := func(tx *wire.MsgTx) ([]ResultOutput, error) {
		return []ResultOutput{{To: "recipient", Value: tx.TxOut[0].Value}}, nil
	}
	policy := SingleResultTxPolicy{
		Label:         "test",
		Status:        contract.ResultStatusSuccess,
		ResolveScript: resolveScript,
		ResolveOutput: resolveOutput,
	}
	valid, err := policy.BuildTx([]ResultPlan{plan})
	require.NoError(t, err)
	require.NoError(t, policy.VerifyTx(valid, []ResultPlan{plan}))

	mutated := valid.Copy()
	mutated.TxOut[0].PkScript = []byte{txscript.OP_FALSE}
	require.ErrorContains(t, policy.VerifyTx(mutated, []ResultPlan{plan}), "non-canonical")
}

func TestVerifyCanonicalResultTxRequiresOutputResolver(t *testing.T) {
	tx := wire.NewMsgTx(2)
	input := OutPoint{TxID: "0100000000000000000000000000000000000000000000000000000000000000", Vout: 1}
	wireInput, err := ResultWireOutPoint(input)
	require.NoError(t, err)
	tx.AddTxIn(wire.NewTxIn(wireInput, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, nil, []byte{txscript.OP_TRUE}))

	err = VerifyCanonicalResultTx(CanonicalResultVerifyRequest{
		Label:    "test",
		ResultTx: tx,
		Plans: []ResultPlan{{
			Inputs:  []OutPoint{input},
			Outputs: []ResultOutput{{To: "recipient", Value: 1}},
		}},
	})
	require.ErrorContains(t, err, "output resolver")
}

func TestMergeResultPlansByContractCombinesExplicitInputs(t *testing.T) {
	plans := MergeResultPlansByContract([]ResultPlan{
		{Contract: "tc-one", InputScope: ResultInputScopeExplicit, Inputs: []OutPoint{{TxID: "a", Vout: 0}}},
		{Contract: "tc-one", InputScope: ResultInputScopeExplicit, Inputs: []OutPoint{{TxID: "b", Vout: 1}}},
	})
	require.Len(t, plans, 1)
	require.Equal(t, 2, plans[0].ResultCount)
	require.Equal(t, ResultInputScopeExplicit, plans[0].InputScope)
	require.Equal(t, []OutPoint{{TxID: "a", Vout: 0}, {TxID: "b", Vout: 1}}, plans[0].Inputs)
}
