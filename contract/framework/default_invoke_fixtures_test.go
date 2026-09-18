package framework_test

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func newTemplateDefaultFixture(t *testing.T, known, closed, fail bool) defaultConformanceFixture {
	t.Helper()
	addr := defaultConformanceAddress(t, contract.ContractTypeTemplate)
	store := template.NewRuntimeStore()
	cfg := defaultConformanceGas()
	if known {
		c := template.NewExchangeContract(defaultConformanceAsset, contract.SatoshiAssetName, template.ExchangePriceModeHeight,
			[]template.ExchangePriceStep{{Threshold: "0", BPerA: "1"}})
		content, err := c.Encode()
		require.NoError(t, err)
		runtime, err := template.NewRuntimeWithDeployer(addr, template.DeployPayload{
			SubType: template.TemplateExchange, Version: template.CurrentTemplateVersion, ContractContent: content, DeployNonce: 1,
		}, nil, "actor")
		require.NoError(t, err)
		store.Add(runtime)
		if closed {
			_, err := runtime.ApplyInvoke(template.ApplyInvokeRequest{
				Action: contract.ContractInvokeAPIClose, CallID: "fixture-close", Invoker: "actor", Height: 1,
				FundingOutput: framework.ContractOutputFromFunding(contract.FundingOutput{Contract: addr}),
			})
			require.NoError(t, err)
			_, err = runtime.SettleBlockWithGasConfig(1, cfg)
			require.NoError(t, err)
			isClosed, err := store.ContractClosed(addr)
			require.NoError(t, err)
			require.True(t, isClosed)
		}
	}
	fixture := defaultConformanceFixture{address: addr, root: store.StateRoot}
	fixture.balance = func() (*contract.ManagedBalance, bool) { return store.ManagedBalance(addr) }
	fixture.build = func(txs []*wire.MsgTx, actor string) (framework.BackendBlockResultBuildResult, error) {
		return template.BuildBlockResultTxs(template.BlockResultBuildRequest{
			Txs: txs, Store: store, GasConfig: cfg, BlockHeight: 10, AssetPrecision: defaultConformancePrecision,
			ResolveInvoker: func(*wire.MsgTx, contract.Tx) (string, error) { return actor, nil },
			ResolveScript: defaultConformanceResolveScript, ResolveOutput: defaultConformanceResolveOutput,
		})
	}
	fixture.replay = func(txs []*wire.MsgTx, actor string) (framework.BackendBlockExecutionResult, error) {
		return template.ExecuteBlock(template.BlockExecutionRequest{
			Txs: txs, Store: store, GasConfig: cfg, BlockHeight: 10, AssetPrecision: defaultConformancePrecision,
			ResolveInvoker: func(*wire.MsgTx, contract.Tx) (string, error) { return actor, nil },
		})
	}
	return fixture
}

func newEVMDefaultFixture(t *testing.T, known, closed, fail bool) defaultConformanceFixture {
	t.Helper()
	addr := defaultConformanceAddress(t, contract.ContractTypeEVM)
	runtime := evm.NewRuntime(nil)
	cfg := defaultConformanceGas()
	if known {
		code := []byte{0x00}
		if fail {
			code = []byte{0x60, 0x00, 0x60, 0x00, 0xfd}
		}
		runtime.SetCode(evm.ContractAddressHash(addr), code)
		runtime.State.SetContractDeployer(evm.ContractGethAddress(addr), "actor")
		if closed {
			runtime.State.CloseContract(evm.ContractGethAddress(addr))
		}
	}
	block := evm.BlockContext{Number: 10, Time: 10, GasLimit: cfg.MaxGasPerBlock}
	fixture := defaultConformanceFixture{address: addr, root: func() [32]byte { return runtime.State.StateRoot() }}
	fixture.balance = func() (*contract.ManagedBalance, bool) { return runtime.State.ManagedBalance(addr) }
	fixture.build = func(txs []*wire.MsgTx, actor string) (framework.BackendBlockResultBuildResult, error) {
		return evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
			Txs: txs, Runtime: runtime, GasConfig: cfg, Block: block, AssetPrecision: defaultConformancePrecision,
			ResolveCaller: func(*wire.MsgTx, contract.Tx) (string, error) { return actor, nil },
			ResolveScript: defaultConformanceResolveScript, ResolveOutput: defaultConformanceResolveOutput,
		})
	}
	fixture.replay = func(txs []*wire.MsgTx, actor string) (framework.BackendBlockExecutionResult, error) {
		return evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
			Txs: txs, Runtime: runtime, GasConfig: cfg, Block: block, AssetPrecision: defaultConformancePrecision,
			ResolveCaller: func(*wire.MsgTx, contract.Tx) (string, error) { return actor, nil },
			ResolveResultScript: defaultConformanceResolveScript,
		})
	}
	return fixture
}

func newAgentDefaultFixture(t *testing.T, known, closed, fail bool) defaultConformanceFixture {
	t.Helper()
	addr := defaultConformanceAddress(t, contract.ContractTypeAgent)
	store := agent.NewRuntimeStore()
	cfg := defaultConformanceGas()
	config := agent.RuntimeConfig{CoreNodeAddress: "core", BootstrapAddress: "bootstrap"}
	if known {
		prediction := agent.PredictionContract{
			Subtype: agent.SubtypePrediction, Title: "Default invoke conformance", Description: "No implicit bet",
			TimeBase: agent.TimeBaseHeight, BetDeadline: 1000, EventTime: 1001, ConfirmAfter: 1002,
			SourceURL: "https://example.com/result", BetAsset: defaultConformanceAsset, MinBetUnit: "1",
			Outcomes: []agent.PredictionOutcome{{ID: "a", Text: "A"}, {ID: "b", Text: "B"}},
		}
		content, err := prediction.Encode()
		require.NoError(t, err)
		runtime, err := agent.NewRuntimeWithDeployer(addr, agent.DeployPayload{
			SubType: agent.SubtypePrediction, Version: agent.CurrentAgentVersion, ContractContent: content, DeployNonce: 1,
		}, config, "actor")
		require.NoError(t, err)
		if closed {
			_, err = runtime.ApplyClose(agent.ApplyCloseRequest{Invoker: "actor", TimeValue: 1})
			require.NoError(t, err)
		}
		store.Add(runtime)
	}
	fixture := defaultConformanceFixture{address: addr, root: store.StateRoot}
	fixture.balance = func() (*contract.ManagedBalance, bool) { return store.ManagedBalance(addr) }
	fixture.build = func(txs []*wire.MsgTx, actor string) (framework.BackendBlockResultBuildResult, error) {
		return agent.BuildBlockResultTxs(agent.BlockResultBuildRequest{
			Txs: txs, Store: store, GasConfig: cfg, RuntimeConfig: config, BlockHeight: 10, BlockTime: 10,
			AssetPrecision: defaultConformancePrecision,
			ResolveInvoker: func(*wire.MsgTx, contract.Tx) (string, error) { return actor, nil },
			ResolveScript: defaultConformanceResolveScript, ResolveOutput: defaultConformanceResolveOutput,
		})
	}
	fixture.replay = func(txs []*wire.MsgTx, actor string) (framework.BackendBlockExecutionResult, error) {
		return agent.ExecuteBlock(agent.BlockExecutionRequest{
			Txs: txs, Store: store, GasConfig: cfg, RuntimeConfig: config, BlockHeight: 10, BlockTime: 10,
			AssetPrecision: defaultConformancePrecision,
			ResolveInvoker: func(*wire.MsgTx, contract.Tx) (string, error) { return actor, nil },
		})
	}
	return fixture
}
