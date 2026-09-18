package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestCloseCanonicalPlanIsDeterministic(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gas := DefaultGasConfig().GasAssetName
	cfg := GasConfig{GasAssetName: gas, BootstrapAddress: "bootstrap", FixedGasPrice: 1,
		ResultBaseGas: 5, InvokeBaseGas: contract.InvokeBaseGas, DeployBaseGas: contract.DeployBaseGas,
		MaxGasPerBlock: contract.MaxGasPerBlock}
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	const asset = "ordx:ft:profit"
	deploy := testDeployTx(t, 4, blockResultInitCode(evmReturnTrueCode()))
	profit, err := contractframework.NewAssetSet(asset, scommon.NewDefaultDecimal(100))
	require.NoError(t, err)
	require.NoError(t, deploy.TxOut[1].Assets.Merge(profit))
	deploy.TxOut[1].Value = 10
	closeTx := blockResultInvokeTx(t, addr, InvokePayload{GasLimit: contract.InvokeBaseGas,
		Action: contract.ContractInvokeAPIClose}, gas, 6000)
	anomaly := OutPoint{TxID: chainhash.Hash{9}.String(), Vout: 0}
	executor, plans1, err := executeWorkBackend(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deploy, closeTx}, Runtime: NewRuntime(nil), GasConfig: cfg,
		Block: testBlockContext(100), ResolveCaller: fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ContractUTXOs: func(ContractAddress) ([]UTXO, error) {
			return []UTXO{mustUTXO(t, anomaly, addr, asset, 13, 0)}, nil
		},
		ResolveResultScript: evmTestResultScriptResolver(t, addr),
	})
	require.NoError(t, err)
	plans2, err := executor.resultPlans(executor.pending)
	require.NoError(t, err)
	require.Equal(t, plans1, plans2)
	require.Len(t, plans1, 1)
	require.Len(t, plans1[0].Outputs, 3)
}
