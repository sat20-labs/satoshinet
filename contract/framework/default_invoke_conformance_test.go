package framework_test

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const defaultConformanceAsset = "ordx:f:default"

type defaultConformanceSpec struct {
	name            string
	kind            byte
	supportsDefault bool
	callID          func(string, uint32, contract.ContractAddress) string
	newFixture      func(*testing.T, bool, bool, bool) defaultConformanceFixture
}

type defaultConformanceFixture struct {
	address contract.ContractAddress
	build   func([]*wire.MsgTx, string) (framework.BackendBlockResultBuildResult, error)
	replay  func([]*wire.MsgTx, string) (framework.BackendBlockExecutionResult, error)
	root    func() [32]byte
	balance func() (*contract.ManagedBalance, bool)
}

// Every fixture runs the same cases. Runtime-specific setup is kept out of
// assertions; new runtimes need only register their fixture adapter here.
var defaultConformanceSpecs = []defaultConformanceSpec{
	{name: "template", kind: contract.ContractTypeTemplate, supportsDefault: true, callID: template.DeriveInvokeCallID, newFixture: newTemplateDefaultFixture},
	{name: "evm", kind: contract.ContractTypeEVM, supportsDefault: true, callID: evm.DeriveInvokeCallID, newFixture: newEVMDefaultFixture},
	{name: "agent", kind: contract.ContractTypeAgent, callID: agent.DeriveInvokeCallID, newFixture: newAgentDefaultFixture},
}

func TestFrameworkDefaultInvokeConformance(t *testing.T) {
	cases := []struct {
		name                                string
		known, closed, runtimeFailure, memo bool
		count                               int
	}{
		{name: "active", known: true, count: 1},
		{name: "multiple_outputs_same_contract", known: true, count: 2},
		{name: "ordinary_memo", known: true, memo: true, count: 1},
		{name: "unknown_refund", count: 2},
		{name: "closed_refund", known: true, closed: true, count: 2},
		{name: "runtime_failure_refund", known: true, runtimeFailure: true, count: 1},
	}
	for _, spec := range defaultConformanceSpecs {
		t.Run(spec.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					miner := spec.newFixture(t, tc.known, tc.closed, tc.runtimeFailure)
					validator := spec.newFixture(t, tc.known, tc.closed, tc.runtimeFailure)
					asset := defaultConformanceAsset
					if tc.runtimeFailure {
						asset = "ordx:f:unsupported"
					}
					tx := defaultConformanceTx(t, miner.address, tc.count, tc.memo, asset)
					built, err := miner.build([]*wire.MsgTx{tx}, "actor")
					require.NoError(t, err)
					replayed, err := validator.replay([]*wire.MsgTx{tx}, "actor")
					require.NoError(t, err)
					require.Equal(t, built.Execution.StateRoot, replayed.StateRoot, "mining and validation accounting")
					require.Equal(t, miner.root(), validator.root())
					require.Len(t, built.Execution.Records, tc.count)
					expectedSuccess := tc.known && !tc.closed && !tc.runtimeFailure && spec.supportsDefault
					if expectedSuccess {
						require.Len(t, built.ResultTxs, 1)
						require.Len(t, built.ResultTxs[0].TxIn, tc.count)
					} else {
						require.Empty(t, built.ResultTxs, "unfunded invalid default calls become unmanaged")
					}
					seen := make(map[framework.OutPoint]bool)
					for _, record := range built.Execution.Records {
						require.Equal(t, framework.ExecutionKindInvoke, record.Kind)
						require.Len(t, record.FundingInputs, 1)
						input := record.FundingInputs[0]
						require.False(t, seen[input], "each default output must execute only once")
						seen[input] = true
						require.Equal(t, tx.TxID(), input.TxID)
						require.Equal(t, spec.callID(tx.TxID(), input.Vout, miner.address), record.CallID)
						require.Equal(t, expectedSuccess, record.Status == contract.ResultStatusSuccess)
						require.Equal(t, expectedSuccess, record.RequiresResult)
					}
					if expectedSuccess {
						for _, input := range built.ResultTxs[0].TxIn {
							point := framework.WireOutPointToFramework(input.PreviousOutPoint)
							require.True(t, seen[point])
							delete(seen, point)
						}
						require.Empty(t, seen, "all successful calls must be settled")
						outputs, err := defaultConformanceResolveOutput(built.ResultTxs[0])
						require.NoError(t, err)
						require.Equal(t, fmt.Sprint(tc.count*10), defaultConformanceAmount(outputs, miner.address.MustEncode(), asset))
					}
					balance, found := miner.balance()
					if expectedSuccess {
						require.True(t, found)
						amount, err := balance.AssetAmount(asset)
						require.NoError(t, err)
						require.Equal(t, fmt.Sprint(tc.count*10), amount.String())
					} else if found {
						require.True(t, balance.IsZero(), "failed funding must not become managed")
					}
				})
			}
		})
	}
}

func TestFrameworkDefaultInvokeAdmissionDoesNotMutateParent(t *testing.T) {
	for _, spec := range defaultConformanceSpecs {
		t.Run(spec.name, func(t *testing.T) {
			for _, failure := range []string{"missing_actor", "negative_later_output", "malformed_envelope"} {
				t.Run(failure, func(t *testing.T) {
					fixture := spec.newFixture(t, true, false, false)
					tx := defaultConformanceTx(t, fixture.address, 2, false, defaultConformanceAsset)
					actor := "actor"
					switch failure {
					case "missing_actor":
						actor = ""
					case "negative_later_output":
						tx.TxOut[1].Value = -1
					case "malformed_envelope":
						script, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddOp(txscript.OP_16).AddInt64(int64(contract.ContentTypeContractInvoke)).Script()
						require.NoError(t, err)
						tx.AddTxOut(wire.NewTxOut(0, nil, script))
					}
					before := fixture.root()
					_, err := fixture.build([]*wire.MsgTx{tx}, actor)
					require.Error(t, err)
					require.Equal(t, before, fixture.root())
				})
			}
		})
	}
}

func defaultConformanceAddress(t *testing.T, kind byte) contract.ContractAddress {
	t.Helper()
	hash := make([]byte, 20)
	hash[0] = kind
	addr, err := contract.NewContractAddressFromHash(contract.TestnetContractPrefix, contract.AddressVersionV1, kind, hash)
	require.NoError(t, err)
	return addr
}

func defaultConformanceTx(t *testing.T, addr contract.ContractAddress, count int, memo bool, asset string) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{99}}})
	for i := 0; i < count; i++ {
		script, err := contract.ContractPkScript(addr)
		require.NoError(t, err)
		tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{Name: *wire.NewAssetNameFromString(asset), Amount: *scommon.NewDefaultDecimal(10)}}, script))
	}
	if memo {
		script, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddData([]byte("ordinary memo")).Script()
		require.NoError(t, err)
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	return tx
}

func defaultConformanceResolveScript(output framework.ResultOutput) ([]byte, error) {
	if addr, err := contract.DecodeContractAddress(output.To); err == nil {
		return contract.ContractPkScript(addr)
	}
	return txscript.NewScriptBuilder().AddData([]byte(output.To)).AddOp(txscript.OP_DROP).AddOp(txscript.OP_TRUE).Script()
}

func defaultConformanceResolveOutput(tx *wire.MsgTx) ([]framework.ResultOutput, error) {
	return framework.ResultOutputsFromTx(tx, contract.TestnetContractPrefix, contract.ParseContractPkScript,
		func(script []byte) (string, bool, error) {
			pushes, err := txscript.PushedData(script)
			if err != nil || len(pushes) != 1 {
				return "", false, err
			}
			return string(pushes[0]), true, nil
		})
}

func defaultConformanceAmount(outputs []framework.ResultOutput, to, asset string) string {
	total := scommon.NewDefaultDecimal(0)
	for _, output := range outputs {
		if output.To != to {
			continue
		}
		for _, value := range output.Assets {
			if value.Name.String() == asset {
				total = total.AddAlignPrecision(value.Amount.Clone())
			}
		}
	}
	return total.String()
}

func defaultConformanceGas() framework.GasConfig {
	cfg := framework.DefaultGasConfig()
	cfg.BootstrapAddress = "bootstrap"
	return cfg
}

func defaultConformancePrecision(string) (int, bool) { return 8, true }
