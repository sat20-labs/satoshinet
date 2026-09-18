package framework_test

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestFrameworkDefaultInvokeMixedRuntimeTransaction(t *testing.T) {
	classifiers := []func(*wire.MsgTx, string) (framework.TxOrderInfo, error){
		template.ClassifyTxForBlockOrder, evm.ClassifyTxForBlockOrder, agent.ClassifyTxForBlockOrder,
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{})
	var modules []framework.Module
	fixtures := make([]defaultConformanceFixture, len(defaultConformanceSpecs))
	for i, spec := range defaultConformanceSpecs {
		fixtures[i] = spec.newFixture(t, true, false, false)
		funding := defaultConformanceTx(t, fixtures[i].address, 2, false, defaultConformanceAsset)
		for _, output := range funding.TxOut {
			tx.AddTxOut(output)
		}
		modules = append(modules, framework.ModuleAdapter{ModuleDescriptor: framework.ModuleDescriptor{
			NameValue: spec.name, TypeValue: framework.ModuleType(spec.kind), PriorityValue: i + 1,
			ClassifyOrder: classifiers[i],
		}})
	}
	split, err := framework.SplitBlockContractTxs(framework.SplitRequest{Txs: []*wire.MsgTx{tx}, Modules: modules})
	require.NoError(t, err)
	require.Len(t, split.WorkTxs, 3)
	consumed := make(map[framework.OutPoint]bool)
	for i, spec := range defaultConformanceSpecs {
		fixture := fixtures[i]
		built, err := fixture.build(split.WorkTxs[framework.ModuleType(spec.kind)], "actor")
		require.NoError(t, err)
		require.Len(t, built.Execution.Records, 2)
		for _, record := range built.Execution.Records {
			require.True(t, record.Contract.Equal(fixture.address))
			require.Equal(t, spec.supportsDefault, record.Status == contract.ResultStatusSuccess)
			require.Equal(t, spec.supportsDefault, record.RequiresResult)
		}
		if !spec.supportsDefault {
			require.Empty(t, built.ResultTxs)
			continue
		}
		require.Len(t, built.ResultTxs, 1)
		require.Len(t, built.ResultTxs[0].TxIn, 2)
		for _, input := range built.ResultTxs[0].TxIn {
			point := framework.WireOutPointToFramework(input.PreviousOutPoint)
			require.Equal(t, tx.TxID(), point.TxID)
			require.False(t, consumed[point], "different runtimes must not settle the same funding output")
			consumed[point] = true
		}
		outputs, err := defaultConformanceResolveOutput(built.ResultTxs[0])
		require.NoError(t, err)
		require.Equal(t, "20", defaultConformanceAmount(outputs, fixture.address.MustEncode(), defaultConformanceAsset))
	}
	require.Len(t, consumed, 4)
}

func TestFrameworkDefaultInvokeRefundConservesMixedFunding(t *testing.T) {
	const secondAsset = "brc20:f:extra"
	cfg := defaultConformanceGas()
	for _, spec := range defaultConformanceSpecs {
		t.Run(spec.name, func(t *testing.T) {
			for _, closed := range []bool{false, true} {
				name := "unknown"
				if closed {
					name = "closed"
				}
				t.Run(name, func(t *testing.T) {
					fixture := spec.newFixture(t, closed, closed, false)
					tx := defaultConformanceTx(t, fixture.address, 2, false, defaultConformanceAsset)
					for _, output := range tx.TxOut {
						output.Value = 37
						require.NoError(t, output.Assets.Merge(wire.TxAssets{
							{Name: *wire.NewAssetNameFromString(secondAsset), Amount: *scommon.NewDefaultDecimal(5)},
							{Name: *wire.NewAssetNameFromString(cfg.GasAssetName), Amount: *scommon.NewDefaultDecimal(120)},
						}))
					}
					built, err := fixture.build([]*wire.MsgTx{tx}, "actor")
					require.NoError(t, err)
					require.Len(t, built.Execution.Records, 2)
					require.Len(t, built.ResultTxs, 1)
					outputs, err := defaultConformanceResolveOutput(built.ResultTxs[0])
					require.NoError(t, err)
					var sats int64
					for _, output := range outputs {
						require.Equal(t, "actor", output.To, "no refund may be swept to bootstrap or retained by the contract")
						sats += output.Value
					}
					require.Equal(t, int64(74), sats)
					require.Equal(t, "20", defaultConformanceAmount(outputs, "actor", defaultConformanceAsset))
					require.Equal(t, "10", defaultConformanceAmount(outputs, "actor", secondAsset))
					expectedGas := scommon.NewDefaultDecimal(240)
					// Invalid calls share one refund policy across all runtimes:
					// current-call Result gas first, then the 10-sat fallback.
					fee, err := cfg.CheckedResultBaseFee(10)
					require.NoError(t, err)
					expectedGas = expectedGas.SubAlignPrecision(fee).SubAlignPrecision(fee)
					require.Equal(t, expectedGas.String(), defaultConformanceAmount(outputs, "actor", cfg.GasAssetName))
					if balance, ok := fixture.balance(); ok {
						require.True(t, balance.IsZero())
					}
				})
			}
		})
	}
}
