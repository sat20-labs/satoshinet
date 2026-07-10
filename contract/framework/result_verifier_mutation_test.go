package framework

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
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
