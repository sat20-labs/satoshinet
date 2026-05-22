package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLimitOrderContractEncodeDecode(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	require.NoError(t, contract.CheckContent())

	encoded, err := contract.Encode()
	require.NoError(t, err)

	var decoded LimitOrderContract
	require.NoError(t, decoded.Decode(encoded))
	require.Equal(t, contract, &decoded)
	require.Equal(t, TemplateLimitOrder, decoded.TemplateName())
	require.Equal(t, CurrentTemplateVersion, decoded.Version())
}

func TestLimitOrderInvokeParamEncodeDecodeAndCheck(t *testing.T) {
	param := &LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "12.5",
		UnitPrice: "3",
	}
	encoded, err := param.Encode()
	require.NoError(t, err)

	var decoded LimitOrderInvokeParam
	require.NoError(t, decoded.Decode(encoded))
	require.Equal(t, param, &decoded)
	require.NoError(t, decoded.Check(InvokeAPISwap))
}

func TestRefundInvokeParamEncodeDecode(t *testing.T) {
	var empty RefundInvokeParam
	encoded, err := empty.Encode()
	require.NoError(t, err)
	require.Empty(t, encoded)

	param := &RefundInvokeParam{ItemIDs: []int64{1, 3, 5}}
	encoded, err = param.Encode()
	require.NoError(t, err)

	var decoded RefundInvokeParam
	require.NoError(t, decoded.Decode(encoded))
	require.Equal(t, param, &decoded)
}

func TestLimitOrderRejectsLiquidityAndStakeActions(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeStake,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "1",
	}).Encode()
	require.NoError(t, err)

	require.Error(t, contract.CheckInvoke(InvokeAPIAddLiquidity, param))
	require.Error(t, contract.CheckInvoke(InvokeAPISwap, param))
	require.NoError(t, contract.CheckInvoke(InvokeAPIRefund, nil))
}

func TestDefaultRegistryCreatesTemplateContracts(t *testing.T) {
	registry := NewDefaultRegistry()

	limitOrder, err := registry.NewContract(TemplateLimitOrder)
	require.NoError(t, err)
	require.Equal(t, TemplateLimitOrder, limitOrder.TemplateName())

	amm, err := registry.NewContract(TemplateAMM)
	require.NoError(t, err)
	require.Equal(t, TemplateAMM, amm.TemplateName())
}
