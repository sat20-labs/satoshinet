package framework

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type stubModule struct {
	name       string
	moduleType ModuleType
	priority   int
}

func (m stubModule) Name() string     { return m.name }
func (m stubModule) Type() ModuleType { return m.moduleType }
func (m stubModule) Priority() int    { return m.priority }

func (m stubModule) ClassifyTx(tx *wire.MsgTx, prefix string) (TxClass, bool, error) {
	txType, found, err := contract.ClassifyTxPayloadType(tx)
	if err != nil || !found {
		return TxClass{}, false, err
	}
	if txType != contract.TxTypeDeploy && txType != contract.TxTypeInvoke {
		return TxClass{}, false, nil
	}
	for _, txOut := range tx.TxOut {
		contractAddr, ok, err := contract.ParseContractPkScript(txOut.PkScript, prefix)
		if err != nil {
			return TxClass{}, false, err
		}
		if ok && ModuleType(contractAddr.ContractType()) == m.moduleType {
			return TxClass{
				ContractType: m.moduleType,
				TxType:       txType,
				Priority:     m.priority,
			}, true, nil
		}
	}
	return TxClass{}, false, nil
}

func (m stubModule) ExecuteWorkBlock(req WorkExecutionRequest) (ExecutionResult, error) {
	return ExecutionResult{ModuleType: m.moduleType}, nil
}

func (m stubModule) BuildResultTxs(req ResultBuildRequest, exec ExecutionResult) (ResultBuildResult, error) {
	return ResultBuildResult{}, nil
}

func (m stubModule) VerifyResultTxs(req ResultVerifyRequest, exec ExecutionResult) error {
	return nil
}

func (m stubModule) StateRoot(exec ExecutionResult) [32]byte {
	return exec.StateRoot
}

type stubUTXOView map[wire.OutPoint]contract.ContractAddress

func (v stubUTXOView) LookupContractAddress(outpoint wire.OutPoint,
	prefix string) (contract.ContractAddress, bool, error) {

	contractAddr, ok := v[outpoint]
	return contractAddr, ok, nil
}

func TestSplitBlockContractTxsClassifiesWorkAndDefaultInvokes(t *testing.T) {
	templateAddr := testContractAddress(t, ModuleTemplate, 1)
	evmAddr := testContractAddress(t, ModuleEVM, 2)
	deployTx := testWorkTx(t, contract.TxTypeDeploy, templateAddr)
	invokeTx := testWorkTx(t, contract.TxTypeInvoke, evmAddr)
	defaultTx := wire.NewMsgTx(2)
	defaultTx.AddTxOut(testContractTxOut(t, templateAddr))
	defaultTx.AddTxOut(testContractTxOut(t, evmAddr))

	split, err := SplitBlockContractTxs(SplitRequest{
		Txs:     []*wire.MsgTx{deployTx, invokeTx, defaultTx},
		Prefix:  contract.TestnetContractPrefix,
		Modules: testModules(),
	})
	require.NoError(t, err)
	require.Equal(t, []*wire.MsgTx{deployTx, defaultTx}, split.WorkTxs[ModuleTemplate])
	require.Equal(t, []*wire.MsgTx{invokeTx, defaultTx}, split.WorkTxs[ModuleEVM])
	require.Empty(t, split.WorkTxs[ModuleAgent])
	require.Len(t, split.WorkOutputs, 4)
}

func TestSplitBlockContractTxsInfersResultOwnershipFromParentUTXO(t *testing.T) {
	for _, tc := range []struct {
		name       string
		moduleType ModuleType
	}{
		{name: "template", moduleType: ModuleTemplate},
		{name: "evm", moduleType: ModuleEVM},
		{name: "agent", moduleType: ModuleAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prevOut := wire.OutPoint{Hash: chainhash.Hash{byte(tc.moduleType)}, Index: 7}
			resultTx := testResultTx(t, prevOut)
			split, err := SplitBlockContractTxs(SplitRequest{
				Txs:        []*wire.MsgTx{resultTx},
				Prefix:     contract.TestnetContractPrefix,
				Modules:    testModules(),
				ParentView: stubUTXOView{prevOut: testContractAddress(t, tc.moduleType, byte(tc.moduleType))},
			})
			require.NoError(t, err)
			require.Equal(t, []*wire.MsgTx{resultTx}, split.ResultTxs[tc.moduleType])
			require.Empty(t, split.WorkOutputs)
		})
	}
}

func TestSplitBlockContractTxsRejectsInvalidResultOwnership(t *testing.T) {
	t.Run("no contract input", func(t *testing.T) {
		prevOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
		_, err := SplitBlockContractTxs(SplitRequest{
			Txs:        []*wire.MsgTx{testResultTx(t, prevOut)},
			Prefix:     contract.TestnetContractPrefix,
			Modules:    testModules(),
			ParentView: stubUTXOView{},
		})
		require.ErrorContains(t, err, "spends no contract UTXO")
	})

	t.Run("mixed contract modules", func(t *testing.T) {
		templateOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
		evmOut := wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}
		_, err := SplitBlockContractTxs(SplitRequest{
			Txs:     []*wire.MsgTx{testResultTx(t, templateOut, evmOut)},
			Prefix:  contract.TestnetContractPrefix,
			Modules: testModules(),
			ParentView: stubUTXOView{
				templateOut: testContractAddress(t, ModuleTemplate, 1),
				evmOut:      testContractAddress(t, ModuleEVM, 2),
			},
		})
		require.ErrorContains(t, err, "multiple modules")
	})
}

func TestSplitBlockContractTxsInfersResultOwnershipFromSameBlockWorkOutput(t *testing.T) {
	evmAddr := testContractAddress(t, ModuleEVM, 9)
	workTx := testWorkTx(t, contract.TxTypeInvoke, evmAddr)
	workOut := wire.OutPoint{Hash: workTx.TxHash(), Index: 0}
	resultTx := testResultTx(t, workOut)
	resultTx.AddTxOut(testContractTxOut(t, testContractAddress(t, ModuleAgent, 8)))

	split, err := SplitBlockContractTxs(SplitRequest{
		Txs:     []*wire.MsgTx{workTx, resultTx},
		Prefix:  contract.TestnetContractPrefix,
		Modules: testModules(),
	})
	require.NoError(t, err)
	require.Equal(t, []*wire.MsgTx{resultTx}, split.ResultTxs[ModuleEVM])
	require.Contains(t, split.WorkOutputs, workOut)
	require.NotContains(t, split.WorkOutputs, wire.OutPoint{Hash: resultTx.TxHash(), Index: 1})
}

func testModules() []Module {
	return []Module{
		stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
		stubModule{name: "evm", moduleType: ModuleEVM, priority: 2},
		stubModule{name: "agent", moduleType: ModuleAgent, priority: 3},
	}
}

func testWorkTx(t *testing.T, txType contract.TxType, contractAddr contract.ContractAddress) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}})
	tx.AddTxOut(testContractTxOut(t, contractAddr))
	var script []byte
	var err error
	switch txType {
	case contract.TxTypeDeploy:
		script, err = contract.DeployNullDataScript(contract.DeployPayload{GasLimit: 1, DeployNonce: 1})
	case contract.TxTypeInvoke:
		script, err = contract.InvokeNullDataScript(contract.InvokePayload{GasLimit: 1, CallNonce: 1})
	default:
		t.Fatalf("unsupported work tx type %v", txType)
	}
	require.NoError(t, err)
	tx.AddTxOut(&wire.TxOut{PkScript: script})
	return tx
}

func testResultTx(t *testing.T, inputs ...wire.OutPoint) *wire.MsgTx {
	t.Helper()
	script, err := contract.ResultNullDataScript(contract.ResultPayload{
		Status:      contract.ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	for _, input := range inputs {
		tx.AddTxIn(&wire.TxIn{PreviousOutPoint: input})
	}
	tx.AddTxOut(&wire.TxOut{PkScript: script})
	return tx
}

func testContractTxOut(t *testing.T, contractAddr contract.ContractAddress) *wire.TxOut {
	t.Helper()
	script, err := contract.ContractPkScript(contractAddr)
	require.NoError(t, err)
	return &wire.TxOut{Value: 1, PkScript: script}
}

func testContractAddress(t *testing.T, moduleType ModuleType, seed byte) contract.ContractAddress {
	t.Helper()
	hash := make([]byte, 20)
	for i := range hash {
		hash[i] = seed
	}
	addr, err := contract.NewContractAddressFromHash(
		contract.TestnetContractPrefix, contract.AddressVersionV1, byte(moduleType), hash)
	require.NoError(t, err)
	return addr
}
