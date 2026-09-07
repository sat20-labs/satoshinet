//go:build rpctest

package contract_e2e

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Result outputs are settlement only: funding another contract must not
// automatically execute it and start an unbounded chain of Result transactions.
// An ordinary default transaction remains a supported invocation entry point.
func TestNetworkEVMResultOutputDoesNotInvokeNextContract(t *testing.T) {
	f := newTemplateNetworkFixture(t, nil)
	gasAsset := evm.DefaultGasConfig().GasAssetName
	inputs := f.splitAsset(t, f.gasAnchor, gasAsset,
		[]int64{1000000, 1000000, 1000000, 1000000},
		[]int64{10000, 10000, 10000, 10000}, f.traderA)
	funding := func(value int64) wire.TxOut {
		return wire.TxOut{Value: value, Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)}}
	}
	// B's default entry writes a marker and records its caller. It needs no
	// calldata, proxy call, trigger, external service or Solidity compiler.
	bCode := []byte{0x60, 1, 0x60, 0, 0x55, 0x33, 0x60, 1, 0x55, 0}
	bDeploy, b, _ := buildTemplateWitnessEVMDeployTx(t, f, f.traderA, 901,
		cascadeNetworkInitCode(bCode), []wire.OutPoint{inputs[0]}, funding(0), nil)
	f.sendAndWaitTx(t, bDeploy)
	requireEVMResultStatusForTx(t, f.bootstrapNode, bDeploy, b, evm.ResultStatusSuccess)

	t.Run("ordinary_default_call_control", func(t *testing.T) {
		control := buildTemplateDefaultInvokeTx(t, f, f.traderA, b, []wire.OutPoint{inputs[1]}, funding(1000))
		require.False(t, txHasContractOpReturn(control))
		f.sendAndWaitTx(t, control)
		requireEVMResultStatusForTx(t, f.bootstrapNode, control, b, evm.ResultStatusSuccess)
		block := cascadeNetworkBlockForTx(t, f.bootstrapNode, control)
		result := cascadeNetworkResultSpending(block, wire.OutPoint{Hash: control.TxHash(), Index: 0})
		require.NotNil(t, result, "ordinary default funding must execute B and produce a spending Result")
		t.Logf("control PASS: B=%s funding=%s:0 Result B=%s", b.MustEncode(), control.TxID(), result.TxID())
	})

	// A calls only the asset precompile. Its Result must transfer both ordinary
	// sats and gas assets to B. Embed calldata to avoid invoke payload limits.
	data := evm.EncodeTransferAssetsCall([]string{contractcommon.SatoshiAssetName, gasAsset},
		[]string{b.MustEncode(), b.MustEncode()}, []string{"1000", "1000"}, [][]byte{nil, nil})
	aDeploy, a, _ := buildTemplateWitnessEVMDeployTx(t, f, f.traderA, 902,
		cascadeNetworkInitCode(cascadeNetworkPrecompileCode(data)), []wire.OutPoint{inputs[2]}, funding(0), nil)
	f.sendAndWaitTx(t, aDeploy)
	requireEVMResultStatusForTx(t, f.bootstrapNode, aDeploy, a, evm.ResultStatusSuccess)
	// A also uses a default funding transaction: gas assets are retained by
	// the contract rather than simultaneously requested as an invoke refund.
	// Leave ordinary sats in A for its Result's transaction fee.
	invokeA := buildTemplateDefaultInvokeTx(t, f, f.traderA, a, []wire.OutPoint{inputs[3]}, funding(2000))
	require.False(t, txHasContractOpReturn(invokeA))
	f.sendAndWaitTx(t, invokeA)
	requireEVMResultStatusForTx(t, f.bootstrapNode, invokeA, a, evm.ResultStatusSuccess)
	block := cascadeNetworkBlockForTx(t, f.bootstrapNode, invokeA)
	aFunding, err := evm.FindContractOutputsForContract(invokeA, evm.StandardContractScriptResolver(evm.TestnetContractPrefix), a)
	require.NoError(t, err)
	require.Len(t, aFunding, 1)
	resultA := cascadeNetworkResultSpending(block, wire.OutPoint{Hash: invokeA.TxHash(), Index: aFunding[0].Vout})
	require.NotNil(t, resultA)
	bOutputs, err := evm.FindContractOutputsForContract(resultA, evm.StandardContractScriptResolver(evm.TestnetContractPrefix), b)
	require.NoError(t, err)
	require.Len(t, bOutputs, 1, "A must actually fund B before testing automatic invocation")
	bFunding := bOutputs[0]
	require.Equal(t, int64(1000), bFunding.PhysicalValue())
	gas, err := bFunding.AssetAmount(gasAsset)
	require.NoError(t, err)
	require.Equal(t, "1000", gas.String())
	defaults, err := contractcommon.FindDefaultInvokeOutputs(resultA, evm.TestnetContractPrefix, evm.ContractTypeEVM)
	require.NoError(t, err)
	resultB := cascadeNetworkResultSpending(block, wire.OutPoint{Hash: resultA.TxHash(), Index: bFunding.Vout})
	f.requireNodesSynced(t)
	t.Logf("A=%s B=%s block=%s invoke A=%s Result A=%s B funding=%s:%d sats=1000 gas=1000 default outputs=%d Result B present=%v",
		a.MustEncode(), b.MustEncode(), block.BlockHash(), invokeA.TxID(), resultA.TxID(), resultA.TxID(), bFunding.Vout, len(defaults), resultB != nil)
	require.Empty(t, defaults, "Result settlement outputs must never become automatic default invocations")
	require.Nil(t, resultB, "Result A funding B must not trigger an automatic Result B")
}

func cascadeNetworkInitCode(runtime []byte) []byte {
	code := []byte{0x61, byte(len(runtime) >> 8), byte(len(runtime)), 0x61, 0, 15, 0x60, 0, 0x39,
		0x61, byte(len(runtime) >> 8), byte(len(runtime)), 0x60, 0, 0xf3}
	return append(code, runtime...)
}

func cascadeNetworkPrecompileCode(data []byte) []byte {
	code := []byte{0x61, byte(len(data) >> 8), byte(len(data)), 0x61, 0, 0, 0x60, 0, 0x39,
		0x60, 0, 0x60, 0, 0x61, byte(len(data) >> 8), byte(len(data)), 0x60, 0, 0x60, 0, 0x73}
	code = append(code, evm.AssetPrecompileAddress.Bytes()...)
	code = append(code, 0x61, 0xc3, 0x50, 0xf1, 0x50, 0)
	code[4], code[5] = byte(len(code)>>8), byte(len(code))
	return append(code, data...)
}

func cascadeNetworkBlockForTx(t *testing.T, node *rpctest.Harness, tx *wire.MsgTx) *wire.MsgBlock {
	t.Helper()
	hash := tx.TxHash()
	verbose, err := node.Client.GetRawTransactionVerbose(&hash)
	require.NoError(t, err)
	blockHash, err := chainhash.NewHashFromStr(verbose.BlockHash)
	require.NoError(t, err)
	block, err := node.Client.GetBlock(blockHash)
	require.NoError(t, err)
	return block
}

func cascadeNetworkResultSpending(block *wire.MsgBlock, funding wire.OutPoint) *wire.MsgTx {
	for _, tx := range block.Transactions {
		parsed, err := evm.ParseTx(tx, nil)
		if err != nil || parsed.Result == nil {
			continue
		}
		for _, in := range tx.TxIn {
			if in.PreviousOutPoint == funding {
				return tx
			}
		}
	}
	return nil
}
