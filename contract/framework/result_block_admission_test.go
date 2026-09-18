package framework

import (
	"errors"
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSingleResultBlockRejectsBadClassificationBeforeExecution(t *testing.T) {
	for _, scenario := range []string{"classifier_error", "malformed_envelope", "nil_transaction", "nil_input", "external_result"} {
		t.Run(scenario, func(t *testing.T) {
			first, second := wire.NewMsgTx(2), wire.NewMsgTx(2)
			first.AddTxIn(&wire.TxIn{})
			second.AddTxIn(&wire.TxIn{})
			first.AddTxOut(wire.NewTxOut(1, nil, []byte{txscript.OP_TRUE}))
			second.AddTxOut(wire.NewTxOut(2, nil, []byte{txscript.OP_TRUE}))
			switch scenario {
			case "malformed_envelope":
				script, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddOp(txscript.OP_16).
					AddInt64(int64(contract.ContentTypeContractInvoke)).Script()
				require.NoError(t, err)
				second.AddTxOut(wire.NewTxOut(0, nil, script))
			case "nil_transaction":
				second = nil
			case "nil_input":
				second.TxIn[0] = nil
			case "external_result":
				script, err := contract.ResultNullDataScript(contract.ResultPayload{ResultCount: 1})
				require.NoError(t, err)
				second.AddTxOut(wire.NewTxOut(0, nil, script))
			}
			executions, finalizations := 0, 0
			_, err := BuildSingleResultTxBlock(SingleResultBlockRequest[int]{
				ModuleName: "test", Txs: []*wire.MsgTx{first, second},
				Classify: func(tx *wire.MsgTx, _ string) (TxOrderInfo, error) {
					if scenario == "classifier_error" && tx == second {
						return TxOrderInfo{}, errors.New("bad call data")
					}
					return TxOrderInfo{IsTemplate: true, Type: contract.TxTypeInvoke}, nil
				},
				IsModule: func(info TxOrderInfo) bool { return info.IsTemplate },
				IsResult: func(info TxOrderInfo) bool { return info.Type == contract.TxTypeResult },
				ExecuteTx: func(*wire.MsgTx) error { executions++; return nil },
				Finalize: func() (int, error) { finalizations++; return 0, nil },
				Plans: func(int) []ResultPlan { return nil },
			})
			require.ErrorIs(t, err, ErrCallAdmission)
			require.Zero(t, executions)
			require.Zero(t, finalizations)
		})
	}
}

func TestSingleResultBlockSkipsOnlyNonMatchingTransactions(t *testing.T) {
	tx := wire.NewMsgTx(2)
	executions, finalizations := 0, 0
	_, err := BuildSingleResultTxBlock(SingleResultBlockRequest[int]{
		ModuleName: "test", Txs: []*wire.MsgTx{tx},
		Classify: func(*wire.MsgTx, string) (TxOrderInfo, error) { return TxOrderInfo{}, nil },
		IsModule: func(info TxOrderInfo) bool { return info.IsTemplate },
		IsResult: func(info TxOrderInfo) bool { return info.Type == contract.TxTypeResult },
		ExecuteTx: func(*wire.MsgTx) error { executions++; return nil },
		Finalize: func() (int, error) { finalizations++; return 0, nil },
		Plans: func(int) []ResultPlan { return nil },
	})
	require.NoError(t, err)
	require.Zero(t, executions)
	require.Equal(t, 1, finalizations)
}
