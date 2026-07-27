package agent

import (
	"bytes"
	"strings"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestParseAgentDeployTxCombinesMultipleOPReturns(t *testing.T) {
	contractContent := validPredictionContract()
	contractContent.Description = strings.Repeat("d", 500)
	content, err := contractContent.Encode()
	if err != nil {
		t.Fatalf("PredictionContract.Encode failed: %v", err)
	}

	scripts, err := DeployNullDataScripts(DeployPayload{
		GasLimit:        5000,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     7,
		ContractContent: content,
	})
	if err != nil {
		t.Fatalf("DeployNullDataScripts failed: %v", err)
	}
	if len(scripts) <= 1 {
		t.Fatalf("expected split OP_RETURN scripts")
	}

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}

	parsed, err := ParseTx(tx, testAgentContractResolver)
	if err != nil {
		t.Fatalf("ParseTx failed: %v", err)
	}
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		t.Fatalf("unexpected parsed deploy: %#v", parsed)
	}
	if parsed.Deploy.GasLimit != 5000 {
		t.Fatalf("gas limit mismatch: %d", parsed.Deploy.GasLimit)
	}
	if string(parsed.Deploy.ContractContent) != string(content) {
		t.Fatalf("content mismatch")
	}
}

func TestParseAgentInvokeTxFindsAgentContractOutputs(t *testing.T) {
	contract := testAgentContract(t)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	param, err := (PredictionBetParam{OutcomeID: "a"}).Encode()
	if err != nil {
		t.Fatalf("PredictionBetParam.Encode failed: %v", err)
	}
	invokeScript, err := InvokeNullDataScript(InvokePayload{GasLimit: 1000, CallNonce: 9, Action: InvokeAPIBet, Param: param})
	if err != nil {
		t.Fatalf("InvokeNullDataScript failed: %v", err)
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString("ordx:f:gas"),
		Amount: *scommon.NewDefaultDecimal(20),
	}}, testAgentContractScript(contract)))

	parsed, err := ParseTx(tx, testAgentContractResolver)
	if err != nil {
		t.Fatalf("ParseTx failed: %v", err)
	}
	if parsed.Type != TxTypeInvoke || parsed.Invoke == nil {
		t.Fatalf("unexpected parsed invoke: %#v", parsed)
	}
	if parsed.Invoke.Action != InvokeAPIBet {
		t.Fatalf("action mismatch: %s", parsed.Invoke.Action)
	}
	if !bytes.Equal(parsed.Invoke.Param, param) {
		t.Fatalf("invoke param changed during OP_RETURN parsing: got %x want %x", parsed.Invoke.Param, param)
	}
	if len(parsed.ContractOutputs) != 1 {
		t.Fatalf("contract output count mismatch: %d", len(parsed.ContractOutputs))
	}
	if !contract.Equal(parsed.ContractOutputs[0].Contract) {
		t.Fatalf("contract mismatch")
	}
	amount, err := parsed.ContractOutputs[0].AssetAmount("ordx:f:gas")
	if err != nil {
		t.Fatalf("AssetAmount failed: %v", err)
	}
	if amount.Cmp(scommon.NewDefaultDecimal(20)) != 0 {
		t.Fatalf("asset amount mismatch: %s", amount.String())
	}
}

func TestParseAgentTxRejectsExternalResult(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	resultScript, err := contractcommon.ResultNullDataScript(ResultPayload{Status: contractcommon.ResultStatusSuccess, ResultCount: 1})
	if err != nil {
		t.Fatalf("ResultNullDataScript failed: %v", err)
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	_, err = ParseTx(tx, testAgentContractResolver)
	if err == nil || err.Error() != "agent RESULT transactions are built by block execution and are not accepted as external input" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAgentInvokeRejectsTemplateContractOutput(t *testing.T) {
	templateContract, err := contractcommon.NewContractAddressFromHash(
		TestnetContractPrefix,
		AddressVersionV1,
		contractcommon.ContractTypeTemplate,
		make([]byte, 32),
	)
	if err != nil {
		t.Fatalf("NewContractAddressFromHash failed: %v", err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := InvokeNullDataScript(InvokePayload{GasLimit: 1000, CallNonce: 9, Action: InvokeAPIBet})
	if err != nil {
		t.Fatalf("InvokeNullDataScript failed: %v", err)
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, nil, testAgentContractScript(templateContract)))

	_, err = ParseTx(tx, testAgentContractResolver)
	if err == nil || err.Error() != "agent INVOKE has no contract output" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testAgentContract(t *testing.T) ContractAddress {
	t.Helper()
	hash := make([]byte, AddressHashLen)
	hash[0] = 1
	addr, err := contractcommon.NewContractAddressFromHash(
		TestnetContractPrefix,
		AddressVersionV1,
		ContractTypeAgent,
		hash,
	)
	if err != nil {
		t.Fatalf("NewContractAddressFromHash failed: %v", err)
	}
	return addr
}

func testAgentContractScript(contract ContractAddress) []byte {
	script, err := ContractPkScript(contract)
	if err != nil {
		panic(err)
	}
	return script
}

func testAgentContractResolver(pkScript []byte) (ContractAddress, bool, error) {
	return ParseContractPkScript(pkScript, TestnetContractPrefix)
}
