package blockchain

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
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
	resultScript, err := evmcommon.ResultNullDataScript(evm.ResultPayload{
		Status:      evm.ResultStatusSuccess,
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
		t.Fatal("expected non-result contract spend to be rejected")
	}
}

func testEVMContractTxOut(t *testing.T) *wire.TxOut {
	t.Helper()
	addr, err := evm.ParseEVMAddressHex("00112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix, evm.AddressVersionV1, evm.ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	txOut, err := evm.NewContractTxOut(1000, nil, contract)
	if err != nil {
		t.Fatal(err)
	}
	return txOut
}
