package template

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestParseTemplateDeployTxCombinesMultipleOPReturns(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	content = append(content, make([]byte, contractcommon.MaxNullDataPayloadLen+11)...)

	scripts, err := DeployNullDataScripts(DeployPayload{
		GasLimit:        5000,
		TemplateName:    TemplateLimitOrder,
		TemplateVersion: CurrentTemplateVersion,
		Deployer:        "deployer-address",
		Random:          []byte("random"),
		ContractContent: content,
	})
	require.NoError(t, err)
	require.Greater(t, len(scripts), 1)

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}

	parsed, err := ParseTx(tx, testTemplateContractResolver)
	require.NoError(t, err)
	require.Equal(t, TxTypeDeploy, parsed.Type)
	require.NotNil(t, parsed.Deploy)
	require.Equal(t, uint64(5000), parsed.Deploy.GasLimit)
	require.Equal(t, content, parsed.Deploy.ContractContent)
}

func TestParseTemplateInvokeTxFindsTemplateContractOutputs(t *testing.T) {
	contract := testTemplateContract(t)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := InvokeNullDataScript(InvokePayload{GasLimit: 1000, CallNonce: 9, Action: InvokeAPISwap, Param: []byte{1, 2, 3}})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString("ordx:f:gas"),
		Amount: *scommon.NewDefaultDecimal(20),
	}}, testTemplateContractScript(contract)))

	parsed, err := ParseTx(tx, testTemplateContractResolver)
	require.NoError(t, err)
	require.Equal(t, TxTypeInvoke, parsed.Type)
	require.NotNil(t, parsed.Invoke)
	require.Equal(t, InvokeAPISwap, parsed.Invoke.Action)
	require.Len(t, parsed.ContractOutputs, 1)
	require.True(t, contract.Equal(parsed.ContractOutputs[0].Contract))
	amount, err := parsed.ContractOutputs[0].AssetAmount("ordx:f:gas")
	require.NoError(t, err)
	require.Equal(t, 0, amount.Cmp(scommon.NewDefaultDecimal(20)))
}

func TestParseTemplateTxRejectsExternalResult(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	resultScript, err := contractcommon.ResultNullDataScript(ResultPayload{Status: contractcommon.ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	_, err = ParseTx(tx, testTemplateContractResolver)
	require.EqualError(t, err, "template RESULT transactions are built by block execution and are not accepted as external input")
}

func TestParseTemplateInvokeRejectsEVMContractOutput(t *testing.T) {
	evmContract, err := contractcommon.NewContractAddressFromHash(
		TestnetContractPrefix,
		AddressVersionV1,
		ContractTypeEVM,
		make([]byte, 20),
	)
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := InvokeNullDataScript(InvokePayload{GasLimit: 1000, CallNonce: 9, Action: InvokeAPISwap})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, nil, testTemplateContractScript(evmContract)))

	_, err = ParseTx(tx, testTemplateContractResolver)
	require.EqualError(t, err, "template INVOKE has no contract output")
}

func testTemplateContract(t *testing.T) ContractAddress {
	t.Helper()
	hash := make([]byte, AddressHashLen)
	hash[0] = 1
	addr, err := contractcommon.NewContractAddressFromHash(
		TestnetContractPrefix,
		AddressVersionV1,
		ContractTypeTemplate,
		hash,
	)
	require.NoError(t, err)
	return addr
}

func testTemplateContractScript(contract ContractAddress) []byte {
	script, err := ContractPkScript(contract)
	if err != nil {
		panic(err)
	}
	return script
}

func testTemplateContractResolver(pkScript []byte) (ContractAddress, bool, error) {
	return ParseContractPkScript(pkScript, TestnetContractPrefix)
}
