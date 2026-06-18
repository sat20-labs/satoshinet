package blockchain

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestValidateTransactionScriptsAllowsEVMResultContractSpend(t *testing.T) {
	prevHash := chainhash.Hash{1, 2, 3}
	prevOut := wire.OutPoint{Hash: prevHash, Index: 0}
	contractOut := testEVMContractTxOut(t)
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = NewUtxoEntry(contractOut, 1, false)

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	resultScript, err := contract.ResultNullDataScript(contract.ResultPayload{
		Status:      contract.ResultStatusSuccess,
		ResultCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	err = ValidateTransactionScripts(btcutil.NewTx(tx), view, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateTransactionScriptsRejectsNonResultContractSpend(t *testing.T) {
	prevHash := chainhash.Hash{1, 2, 3}
	prevOut := wire.OutPoint{Hash: prevHash, Index: 0}
	contractOut := testEVMContractTxOut(t)
	view := NewUtxoViewpoint()
	view.Entries()[prevOut] = NewUtxoEntry(contractOut, 1, false)

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, nil, []byte{0x51}))

	err := ValidateTransactionScripts(btcutil.NewTx(tx), view, 0, nil, nil)
	if err == nil {
		t.Fatal("expected non-result ca spend to be rejected")
	}
}

func testEVMContractTxOut(t *testing.T) *wire.TxOut {
	t.Helper()
	addr, err := contract.ParseEVMAddressHex("00112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	ca, err := contract.NewContractAddress(contract.TestnetContractPrefix, contract.AddressVersionV1, contract.ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	txOut, err := contract.NewContractTxOut(1000, nil, ca)
	if err != nil {
		t.Fatal(err)
	}
	return txOut
}
