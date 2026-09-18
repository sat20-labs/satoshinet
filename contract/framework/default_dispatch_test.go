package framework

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDefaultInvokeDispatchUsesRegisteredModules(t *testing.T) {
	const fourth ModuleType = 4
	modules := []Module{
		stubModule{name: "fourth", moduleType: fourth, priority: 4},
		stubModule{name: "agent", moduleType: ModuleAgent, priority: 3},
		stubModule{name: "evm", moduleType: ModuleEVM, priority: 2},
		stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{})
	for _, typ := range []ModuleType{fourth, ModuleTemplate, ModuleEVM, ModuleAgent, ModuleTemplate, fourth} {
		addr := testContractAddress(t, typ, byte(typ))
		script, err := contract.ContractPkScript(addr)
		require.NoError(t, err)
		tx.AddTxOut(wire.NewTxOut(1, nil, script))
	}
	split, err := SplitBlockContractTxs(SplitRequest{Txs: []*wire.MsgTx{tx}, Modules: modules})
	require.NoError(t, err)
	require.Len(t, split.WorkTxs, 4)
	require.Len(t, split.WorkOutputs, 6)
	for _, module := range modules {
		require.Equal(t, []*wire.MsgTx{tx}, split.WorkTxs[module.Type()], "one dispatch per module regardless of output count")
	}
}

func TestDefaultInvokeResultOutputsDoNotBecomeCalls(t *testing.T) {
	addr := testContractAddress(t, ModuleEVM, 1)
	script, err := contract.ContractPkScript(addr)
	require.NoError(t, err)
	work := wire.NewMsgTx(2)
	work.AddTxIn(&wire.TxIn{})
	work.AddTxOut(wire.NewTxOut(5, nil, script))
	result := testResultTx(t, wire.OutPoint{Hash: work.TxHash(), Index: 0})
	other := testContractAddress(t, ModuleTemplate, 2)
	otherScript, err := contract.ContractPkScript(other)
	require.NoError(t, err)
	result.AddTxOut(wire.NewTxOut(5, nil, otherScript))
	split, err := SplitBlockContractTxs(SplitRequest{
		Txs: []*wire.MsgTx{work, result},
		Modules: []Module{
			stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
			stubModule{name: "evm", moduleType: ModuleEVM, priority: 2},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []*wire.MsgTx{work}, split.WorkTxs[ModuleEVM])
	require.Empty(t, split.WorkTxs[ModuleTemplate])
	require.Equal(t, []*wire.MsgTx{result}, split.ResultTxs[ModuleEVM])
}
