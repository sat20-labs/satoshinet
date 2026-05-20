package blockchain

import (
	"math"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/evm"
	evmcommon "github.com/sat20-labs/satoshinet/evm/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestCheckEVMBlockOrder(t *testing.T) {
	coinbase := wire.NewMsgTx(2)
	coinbase.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Index: math.MaxUint32},
		SignatureScript:  []byte{0x01, 0x01},
	})
	coinbase.AddTxOut(&wire.TxOut{PkScript: []byte{txscript.OP_TRUE}})

	ordinary := wire.NewMsgTx(2)
	ordinary.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 0}})
	ordinary.AddTxOut(&wire.TxOut{PkScript: []byte{txscript.OP_TRUE}})

	deployScript, err := evmcommon.DeployNullDataScript(evm.DeployPayload{
		GasLimit:    1,
		DeployNonce: 1,
		InitCode:    []byte{0x60, 0x00},
	})
	if err != nil {
		t.Fatal(err)
	}
	deploy := wire.NewMsgTx(2)
	deploy.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 1}})
	deploy.AddTxOut(&wire.TxOut{PkScript: deployScript})

	validBlock := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, ordinary, deploy},
	})
	if err := checkEVMBlockOrder(validBlock, &chaincfg.TestNetParams); err != nil {
		t.Fatalf("valid EVM block order rejected: %v", err)
	}

	invalidBlock := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, deploy, ordinary},
	})
	err = checkEVMBlockOrder(invalidBlock, &chaincfg.TestNetParams)
	if err == nil {
		t.Fatalf("expected invalid EVM block order error")
	}
	ruleErr, ok := err.(RuleError)
	if !ok {
		t.Fatalf("expected RuleError, got %T", err)
	}
	if ruleErr.ErrorCode != ErrInvalidEVMBlock {
		t.Fatalf("unexpected error code %v", ruleErr.ErrorCode)
	}

	stateRootScript, err := evmcommon.StateRootNullDataScript(evm.StateRootPayload{})
	if err != nil {
		t.Fatal(err)
	}
	stateRootTx := wire.NewMsgTx(2)
	stateRootTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 2}})
	stateRootTx.AddTxOut(&wire.TxOut{PkScript: stateRootScript})
	stateRootBlock := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, stateRootTx},
	})
	err = checkEVMBlockOrder(stateRootBlock, &chaincfg.TestNetParams)
	if err == nil {
		t.Fatalf("expected non-coinbase state root error")
	}
	ruleErr, ok = err.(RuleError)
	if !ok {
		t.Fatalf("expected RuleError, got %T", err)
	}
	if ruleErr.ErrorCode != ErrInvalidEVMBlock {
		t.Fatalf("unexpected error code %v", ruleErr.ErrorCode)
	}

	resultScript, err := evmcommon.ResultNullDataScript(evm.ResultPayload{
		Status:      evm.ResultStatusSuccess,
		ResultCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultTx := wire.NewMsgTx(2)
	resultTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: 3}})
	resultTx.AddTxOut(&wire.TxOut{PkScript: resultScript})
	resultBeforeDeployBlock := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, resultTx, deploy},
	})
	err = checkEVMBlockOrder(resultBeforeDeployBlock, &chaincfg.TestNetParams)
	if err == nil {
		t.Fatalf("expected EVM tx after result error")
	}
	ruleErr, ok = err.(RuleError)
	if !ok {
		t.Fatalf("expected RuleError, got %T", err)
	}
	if ruleErr.ErrorCode != ErrInvalidEVMBlock {
		t.Fatalf("unexpected error code %v", ruleErr.ErrorCode)
	}
}
