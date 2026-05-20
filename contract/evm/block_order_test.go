package evm

import (
	"testing"

	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestClassifyTxForBlockOrder(t *testing.T) {
	deployScript, err := evmcommon.DeployNullDataScript(DeployPayload{
		GasLimit:    456,
		DeployNonce: 1,
		InitCode:    []byte{0x60, 0x00},
	})
	if err != nil {
		t.Fatal(err)
	}
	deployTx := wire.NewMsgTx(2)
	deployTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 0}})
	deployTx.AddTxOut(&wire.TxOut{PkScript: deployScript})

	info, err := ClassifyTxForBlockOrder(deployTx, TestnetContractPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsEVM {
		t.Fatalf("expected deploy tx to be EVM")
	}
	if info.Type != TxTypeDeploy {
		t.Fatalf("unexpected tx type %v", info.Type)
	}
	if info.GasLimit != 456 {
		t.Fatalf("unexpected gas limit %d", info.GasLimit)
	}

	info, err = ClassifyTxForBlockOrder(wire.NewMsgTx(2), TestnetContractPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsEVM {
		t.Fatalf("empty non-EVM tx classified as EVM")
	}
}
