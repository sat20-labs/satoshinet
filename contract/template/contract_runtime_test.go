package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func TestNewRuntimeDecodesLimitOrderContract(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateLimitOrder,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(
		contractcommon.TestnetContractPrefix,
		deploy.ContractContent,
		"deployer-address",
		deploy.DeployNonce,
	)
	require.NoError(t, err)

	runtime, err := NewRuntime(addr, deploy, nil)
	require.NoError(t, err)
	require.Equal(t, addr.EncodeAddress(), runtime.URL())
	require.Equal(t, TemplateLimitOrder, runtime.TemplateName())
	require.Equal(t, CurrentTemplateVersion, runtime.Version())

	decoded, ok := runtime.Contract().(*LimitOrderContract)
	require.True(t, ok)
	require.Equal(t, "ordx:f:test", decoded.AssetName)

	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	require.NoError(t, runtime.CheckInvoke(InvokeAPISwap, param))
}

func TestNewRuntimeDecodesAMMContract(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100", 20, "2000")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateAMM,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(
		contractcommon.TestnetContractPrefix,
		deploy.ContractContent,
		"deployer-address",
		deploy.DeployNonce,
	)
	require.NoError(t, err)

	runtime, err := NewRuntime(addr, deploy, nil)
	require.NoError(t, err)
	require.Equal(t, TemplateAMM, runtime.TemplateName())

	decoded, ok := runtime.Contract().(*AMMContract)
	require.True(t, ok)
	require.Equal(t, "100", decoded.AssetAmt)
	require.Equal(t, int64(20), decoded.SatValue)

	param, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}).Encode()
	require.NoError(t, err)
	require.NoError(t, runtime.CheckInvoke(InvokeAPIAddLiquidity, param))
	require.Error(t, runtime.CheckInvoke("stake", param))
}

func TestNewRuntimeRejectsMismatchedTemplateVersion(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateLimitOrder,
		Version:         CurrentTemplateVersion + 1,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(
		contractcommon.TestnetContractPrefix,
		deploy.ContractContent,
		"deployer-address",
		deploy.DeployNonce,
	)
	require.NoError(t, err)

	_, err = NewRuntime(addr, deploy, nil)
	require.EqualError(t, err, "template version mismatch 1 != 2")
}

func TestNewRuntimeRejectsContentForWrongTemplate(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateAMM,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(
		contractcommon.TestnetContractPrefix,
		deploy.ContractContent,
		"deployer-address",
		deploy.DeployNonce,
	)
	require.NoError(t, err)

	_, err = NewRuntime(addr, deploy, nil)
	require.ErrorContains(t, err, "decode template contract content")
}
