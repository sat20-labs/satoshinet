package framework

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type recordingModule struct {
	stubModule
	calls       *[]string
	workSeen    *[]*wire.MsgTx
	resultSeen  *[]*wire.MsgTx
	builtResult *wire.MsgTx
	root        [32]byte
}

func (m recordingModule) ExecuteWorkBlock(req WorkExecutionRequest) (ExecutionResult, error) {
	*m.calls = append(*m.calls, m.Name()+":execute")
	*m.workSeen = append(*m.workSeen, req.Txs...)
	return ExecutionResult{ModuleType: m.Type(), StateRoot: m.root}, nil
}

func (m recordingModule) BuildResultTxs(req ResultBuildRequest, exec ExecutionResult) (ResultBuildResult, error) {
	*m.calls = append(*m.calls, m.Name()+":build")
	if m.builtResult == nil {
		return ResultBuildResult{StateRoot: m.root}, nil
	}
	return ResultBuildResult{ResultTxs: []*wire.MsgTx{m.builtResult}, StateRoot: m.root}, nil
}

func (m recordingModule) VerifyResultTxs(req ResultVerifyRequest, exec ExecutionResult) error {
	*m.calls = append(*m.calls, m.Name()+":verify")
	*m.resultSeen = append(*m.resultSeen, req.ResultTxs...)
	return nil
}

func (m recordingModule) StateRoot(exec ExecutionResult) [32]byte {
	return m.root
}

type singlePassBuildModule struct {
	recordingModule
}

func (m singlePassBuildModule) BuildBlockResults(req ResultBuildRequest) (ResultBuildResult, ExecutionResult, error) {
	*m.calls = append(*m.calls, m.Name()+":build-block")
	*m.workSeen = append(*m.workSeen, req.Txs...)
	if m.builtResult == nil {
		return ResultBuildResult{StateRoot: m.root},
			ExecutionResult{ModuleType: m.Type(), StateRoot: m.root}, nil
	}
	return ResultBuildResult{ResultTxs: []*wire.MsgTx{m.builtResult}, StateRoot: m.root},
		ExecutionResult{ModuleType: m.Type(), StateRoot: m.root}, nil
}

func TestBlockCoordinatorValidationSplitsByModule(t *testing.T) {
	templateWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleTemplate, 1))
	evmWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleEVM, 2))
	templatePrev := wire.OutPoint{Hash: chainhash.Hash{3}, Index: 0}
	evmPrev := wire.OutPoint{Hash: chainhash.Hash{4}, Index: 0}
	templateResult := testResultTx(t, templatePrev)
	evmResult := testResultTx(t, evmPrev)

	var calls []string
	var templateWorkSeen, evmWorkSeen, templateResultsSeen, evmResultsSeen []*wire.MsgTx
	coordinator := BlockCoordinator{
		Prefix: contract.TestnetContractPrefix,
		Modules: []Module{
			recordingModule{
				stubModule: stubModule{name: "evm", moduleType: ModuleEVM, priority: 2},
				calls:      &calls, workSeen: &evmWorkSeen, resultSeen: &evmResultsSeen,
				root: testRoot(2),
			},
			recordingModule{
				stubModule: stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
				calls:      &calls, workSeen: &templateWorkSeen, resultSeen: &templateResultsSeen,
				root: testRoot(1),
			},
		},
	}
	got, err := coordinator.ValidateBlock(BlockValidationRequest{
		Txs: []*wire.MsgTx{templateWork, evmWork, templateResult, evmResult},
		ParentView: stubUTXOView{
			templatePrev: testContractAddress(t, ModuleTemplate, 3),
			evmPrev:      testContractAddress(t, ModuleEVM, 4),
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"template:execute", "template:verify",
		"evm:execute", "evm:verify",
	}, calls)
	require.Equal(t, []*wire.MsgTx{templateWork}, templateWorkSeen)
	require.Equal(t, []*wire.MsgTx{evmWork}, evmWorkSeen)
	require.Equal(t, []*wire.MsgTx{templateResult}, templateResultsSeen)
	require.Equal(t, []*wire.MsgTx{evmResult}, evmResultsSeen)
	require.Equal(t, contract.CombineStateRoots(testRoot(1), testRoot(2), [32]byte{}), got.CombinedRoot)
}

func TestBlockCoordinatorBuildResultsUsesDeterministicModuleOrder(t *testing.T) {
	templateWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleTemplate, 1))
	evmWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleEVM, 2))
	templateResult := testResultTx(t, wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0})
	evmResult := testResultTx(t, wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0})
	var calls []string
	var templateWorkSeen, evmWorkSeen, noResults []*wire.MsgTx
	coordinator := BlockCoordinator{
		Prefix: contract.TestnetContractPrefix,
		Modules: []Module{
			recordingModule{
				stubModule:  stubModule{name: "evm", moduleType: ModuleEVM, priority: 2},
				calls:       &calls,
				workSeen:    &evmWorkSeen,
				resultSeen:  &noResults,
				builtResult: evmResult,
				root:        testRoot(2),
			},
			recordingModule{
				stubModule:  stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
				calls:       &calls,
				workSeen:    &templateWorkSeen,
				resultSeen:  &noResults,
				builtResult: templateResult,
				root:        testRoot(1),
			},
		},
	}
	got, err := coordinator.BuildResults(ResultCoordinatorBuildRequest{
		Txs: []*wire.MsgTx{evmWork, templateWork},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"template:execute", "template:build",
		"evm:execute", "evm:build",
	}, calls)
	require.Equal(t, []*wire.MsgTx{templateResult, evmResult}, got.ResultTxs)
	require.Empty(t, noResults)
	require.Equal(t, contract.CombineStateRoots(testRoot(1), testRoot(2), [32]byte{}), got.CombinedRoot)
}

func TestBlockCoordinatorBuildResultsDoesNotExposeForeignResults(t *testing.T) {
	templateWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleTemplate, 1))
	evmWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleEVM, 2))
	templatePrev := wire.OutPoint{Hash: chainhash.Hash{3}, Index: 0}
	templateResult := testResultTx(t, templatePrev)
	evmResult := testResultTx(t, wire.OutPoint{Hash: chainhash.Hash{4}, Index: 0})

	var calls []string
	var templateWorkSeen, evmWorkSeen, noResults []*wire.MsgTx
	coordinator := BlockCoordinator{
		Prefix: contract.TestnetContractPrefix,
		Modules: []Module{
			recordingModule{
				stubModule:  stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
				calls:       &calls,
				workSeen:    &templateWorkSeen,
				resultSeen:  &noResults,
				builtResult: templateResult,
				root:        testRoot(1),
			},
			recordingModule{
				stubModule:  stubModule{name: "evm", moduleType: ModuleEVM, priority: 2},
				calls:       &calls,
				workSeen:    &evmWorkSeen,
				resultSeen:  &noResults,
				builtResult: evmResult,
				root:        testRoot(2),
			},
		},
	}
	got, err := coordinator.BuildResults(ResultCoordinatorBuildRequest{
		Txs: []*wire.MsgTx{templateWork, templateResult, evmWork},
		ParentView: stubUTXOView{
			templatePrev: testContractAddress(t, ModuleTemplate, 3),
		},
	})
	require.NoError(t, err)
	require.Equal(t, []*wire.MsgTx{templateWork}, templateWorkSeen)
	require.Equal(t, []*wire.MsgTx{evmWork}, evmWorkSeen)
	require.Empty(t, noResults)
	require.Equal(t, []*wire.MsgTx{templateResult, evmResult}, got.ResultTxs)
	require.Equal(t, []*wire.MsgTx{templateResult}, got.ContractSplit.ResultTxs[ModuleTemplate])
}

func TestBlockCoordinatorBuildResultsUsesSinglePassModule(t *testing.T) {
	templateWork := testWorkTx(t, contract.TxTypeInvoke, testContractAddress(t, ModuleTemplate, 1))
	templateResult := testResultTx(t, wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0})
	var calls []string
	var templateWorkSeen, noResults []*wire.MsgTx
	coordinator := BlockCoordinator{
		Prefix: contract.TestnetContractPrefix,
		Modules: []Module{
			singlePassBuildModule{recordingModule{
				stubModule:  stubModule{name: "template", moduleType: ModuleTemplate, priority: 1},
				calls:       &calls,
				workSeen:    &templateWorkSeen,
				resultSeen:  &noResults,
				builtResult: templateResult,
				root:        testRoot(1),
			}},
		},
	}
	got, err := coordinator.BuildResults(ResultCoordinatorBuildRequest{
		Txs: []*wire.MsgTx{templateWork},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"template:build-block"}, calls)
	require.Equal(t, []*wire.MsgTx{templateWork}, templateWorkSeen)
	require.Equal(t, []*wire.MsgTx{templateResult}, got.ResultTxs)
	require.Equal(t, contract.CombineStateRoots(testRoot(1), [32]byte{}, [32]byte{}), got.CombinedRoot)
}

func testRoot(seed byte) [32]byte {
	var root [32]byte
	root[0] = seed
	return root
}
