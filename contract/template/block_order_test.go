package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestClassifyTemplateTxForBlockOrder(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, _ := testTemplateDeployTx(t, contract)

	info, err := ClassifyTxForBlockOrder(deployTx, TestnetContractPrefix)
	require.NoError(t, err)
	require.True(t, info.IsTemplate)
	require.Equal(t, TxTypeDeploy, info.Type)
	require.Equal(t, DefaultGasConfig().DeployBaseGas, info.GasLimit)

	resultTx := wire.NewMsgTx(2)
	script, err := contractcommon.ResultNullDataScript(ResultPayload{Status: ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	resultTx.AddTxOut(wire.NewTxOut(0, nil, script))
	info, err = ClassifyTxForBlockOrder(resultTx, TestnetContractPrefix)
	require.NoError(t, err)
	require.True(t, info.IsTemplate)
	require.Equal(t, TxTypeResult, info.Type)
}
