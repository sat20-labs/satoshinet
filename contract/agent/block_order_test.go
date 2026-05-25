package agent

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestClassifyAgentTxForBlockOrder(t *testing.T) {
	deployTx, _ := testAgentDeployTx(t)

	info, err := ClassifyTxForBlockOrder(deployTx, TestnetContractPrefix)
	if err != nil {
		t.Fatalf("ClassifyTxForBlockOrder failed: %v", err)
	}
	if !info.IsAgent || info.Type != TxTypeDeploy || info.GasLimit != 1000 {
		t.Fatalf("unexpected deploy order info: %#v", info)
	}

	resultTx := wire.NewMsgTx(2)
	script, err := contractcommon.ResultNullDataScript(ResultPayload{Status: ResultStatusSuccess, ResultCount: 1})
	if err != nil {
		t.Fatalf("ResultNullDataScript failed: %v", err)
	}
	resultTx.AddTxOut(wire.NewTxOut(0, nil, script))
	info, err = ClassifyTxForBlockOrder(resultTx, TestnetContractPrefix)
	if err != nil {
		t.Fatalf("Classify result failed: %v", err)
	}
	if !info.IsAgent || info.Type != TxTypeResult {
		t.Fatalf("unexpected result order info: %#v", info)
	}
}
