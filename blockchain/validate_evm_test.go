package blockchain

import (
	"math"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/evm"
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
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix, evm.AddressVersionV1, evm.ContractTypeEVM, evm.EVMAddress{1})
	if err != nil {
		t.Fatal(err)
	}
	contractOut, err := evm.NewContractTxOut(0, nil, contract)
	if err != nil {
		t.Fatal(err)
	}
	deploy.AddTxOut(contractOut)

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

func TestCheckTransactionInputsRequiresContractBaseGasFee(t *testing.T) {
	assetName := wire.NewAssetNameFromString(evmcommon.GasAssetName)
	if assetName == nil {
		t.Fatal("invalid gas asset name")
	}
	deployScript, err := evmcommon.DeployNullDataScript(evm.DeployPayload{
		GasLimit:    evmcommon.DeployBaseGas,
		DeployNonce: 1,
		InitCode:    []byte{0x60, 0x00},
	})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix,
		evm.AddressVersionV1, evm.ContractTypeEVM, evm.EVMAddress{9})
	if err != nil {
		t.Fatal(err)
	}
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	baseFee, err := evmcommon.GasFeeAtHeight(evmcommon.DeployBaseGas, 100)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		fee     uint64
		wantErr bool
	}{
		{name: "exact base fee", fee: baseFee},
		{name: "below base fee", fee: baseFee - 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prevOut := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
			tx := wire.NewMsgTx(2)
			tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
			tx.AddTxOut(wire.NewTxOut(0, nil, deployScript))
			tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
				Name:   *assetName,
				Amount: *scommon.NewDefaultDecimal(int64(evmcommon.DeployBaseGas)),
			}}, contractScript))

			view := NewUtxoViewpoint()
			view.Entries()[prevOut] = NewUtxoEntry(wire.NewTxOut(0, wire.TxAssets{{
				Name:   *assetName,
				Amount: *scommon.NewDefaultDecimal(int64(evmcommon.DeployBaseGas + test.fee)),
			}}, []byte{txscript.OP_TRUE}), 1, false)

			_, _, err := CheckTransactionInputs(btcutil.NewTx(tx), false, 100, view, &chaincfg.TestNetParams)
			if test.wantErr && err == nil {
				t.Fatal("expected missing base fee error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
