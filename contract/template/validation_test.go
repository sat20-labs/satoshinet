package template

import (
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestValidateTemplateDeployTxBasic(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	script, err := DeployNullDataScript(DeployPayload{
		GasLimit:        DefaultGasConfig().DeployBaseGas,
		TemplateName:    TemplateLimitOrder,
		TemplateVersion: CurrentTemplateVersion,
		Deployer:        "deployer-address",
		Random:          []byte("random"),
		ContractContent: content,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))

	validated, err := ValidateDeployTxBasic(tx, TestnetContractPrefix, nil, DefaultGasConfig())
	require.NoError(t, err)
	require.Equal(t, DefaultGasConfig().DeployBaseGas, validated.Payload.GasLimit)
	require.Equal(t, TemplateLimitOrder, validated.Runtime.TemplateName())
	require.Equal(t, validated.Address.EncodeAddress(), validated.Runtime.URL())
}

func TestValidateTemplateInvokeTxBasic(t *testing.T) {
	contract := testTemplateContract(t)
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPISwap,
		Param:     param,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(7, nil, testTemplateContractScript(contract)))

	validated, err := ValidateInvokeTxBasic(tx, testTemplateContractResolver, func(addr ContractAddress) bool {
		return addr.Equal(contract)
	}, DefaultGasConfig())
	require.NoError(t, err)
	require.True(t, contract.Equal(validated.Contract))
	require.Equal(t, DefaultGasConfig().InvokeBaseGas, validated.Payload.GasLimit)
	require.Equal(t, InvokeAPISwap, validated.Payload.Action)
	require.Len(t, validated.FundingOutputs, 1)
}

func TestValidateTemplateInvokeTxBasicRejectsMissingContract(t *testing.T) {
	contract := testTemplateContract(t)
	invokeScript, err := InvokeNullDataScript(InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1, Action: InvokeAPISwap})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(7, nil, testTemplateContractScript(contract)))

	_, err = ValidateInvokeTxBasic(tx, testTemplateContractResolver, func(ContractAddress) bool { return false }, GasConfig{})
	require.EqualError(t, err, "invoke target contract does not exist")
}
