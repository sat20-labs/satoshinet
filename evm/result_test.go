package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDeriveInvokeCallIDChangesWithVout(t *testing.T) {
	contract := testContract(t)
	a := DeriveInvokeCallID("tx", 0, contract)
	b := DeriveInvokeCallID("tx", 1, contract)
	if a == b {
		t.Fatal("call id should include vout")
	}
}

func TestDeriveTriggerCallIDChangesWithHeight(t *testing.T) {
	contract := testContract(t)
	a := DeriveTriggerCallID(contract, "vault-release", 100)
	b := DeriveTriggerCallID(contract, "vault-release", 101)
	require.NotEqual(t, a, b)
}

func TestBindResultInvokes(t *testing.T) {
	contract := testContract(t)
	out := OutPoint{TxID: "funding", Vout: 2}
	binding := InvokeCallBinding{
		CallID:       DeriveInvokeCallID("invoke", 0, contract),
		InvokeTxID:   "invoke",
		FundingInput: out,
		Contract:     contract,
	}
	bindings := BindResultInvokes([]OutPoint{{TxID: "other", Vout: 0}, out}, map[OutPoint]InvokeCallBinding{out: binding})
	if len(bindings) != 1 {
		t.Fatalf("got %d bindings want 1", len(bindings))
	}
	if bindings[0].CallID != binding.CallID {
		t.Fatalf("got %s want %s", bindings[0].CallID, binding.CallID)
	}
}

func TestBindResultTxInvokes(t *testing.T) {
	contract := testContract(t)
	hash := chainhash.Hash{1, 2, 3}
	out := OutPoint{TxID: hash.String(), Vout: 2}
	binding := InvokeCallBinding{
		CallID:       DeriveInvokeCallID("invoke", 2, contract),
		InvokeTxID:   "invoke",
		FundingInput: out,
		Contract:     contract,
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, 2), nil, nil))

	bindings, err := RequireResultTxInvokeBindings(tx, map[OutPoint]InvokeCallBinding{out: binding}, 1)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	require.Equal(t, binding.CallID, bindings[0].CallID)
}
