package engine

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
)

func TestClassifyAgentWorkAfterOtherContractParsersRejectPayload(t *testing.T) {
	tx, _, err := agentcontract.BuildDeployTx(agentcontract.DeployTxBuildRequest{
		ContractPrefix:  agentcontract.TestnetContractPrefix,
		SubType:         agentcontract.SubtypePrediction,
		Version:         agentcontract.CurrentAgentVersion,
		Deployer:        "agent-miner-test",
		DeployNonce:     7,
		ContractContent: []byte("agent payload that is not a template or evm payload"),
		GasLimit:        1000,
	})
	if err != nil {
		t.Fatalf("BuildDeployTx: %v", err)
	}

	class, found, err := ClassifyTxForBlockOrder(tx, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatalf("ClassifyTxForBlockOrder: %v", err)
	}
	if !found {
		t.Fatal("expected agent deploy to be classified")
	}
	if class.ContractType != contractcommon.ContractTypeAgent ||
		class.TxType != contractcommon.TxTypeDeploy ||
		class.Priority != PriorityAgent ||
		class.GasLimit != 1000 {

		t.Fatalf("unexpected class: %#v", class)
	}

	if !BlockHasContractTypeWork(
		[]*btcutil.Tx{btcutil.NewTx(tx)},
		&chaincfg.TestNetParams,
		contractcommon.ContractTypeAgent,
	) {
		t.Fatal("expected agent deploy to be detected as agent work")
	}
}
