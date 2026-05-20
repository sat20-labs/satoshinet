package evm

import (
	"errors"
	"testing"
)

func testContract(t *testing.T) ContractAddress {
	t.Helper()
	addr, err := ParseEVMAddressHex("00112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestSelectCanonicalInputsRequiredFirstThenSortedPrefix(t *testing.T) {
	contract := testContract(t)
	gasAssetName := "ordx:ft:gas"
	utxos := []UTXO{
		mustUTXO(t, OutPoint{TxID: "c", Vout: 0}, contract, gasAssetName, 4, 3),
		mustUTXO(t, OutPoint{TxID: "a", Vout: 0}, contract, gasAssetName, 3, 1),
		mustUTXO(t, OutPoint{TxID: "b", Vout: 0}, contract, gasAssetName, 5, 2),
	}
	result, err := SelectCanonicalInputs(CanonicalSelectionRequest{
		Contract:      contract,
		AssetName:     gasAssetName,
		Required:      mustDefaultDecimal(t, 9),
		Available:     utxos,
		RequiredFirst: []OutPoint{{TxID: "c", Vout: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total.Cmp(mustDefaultDecimal(t, 12)) != 0 || result.Change.Cmp(mustDefaultDecimal(t, 3)) != 0 {
		t.Fatalf("unexpected totals: total=%s change=%s", result.Total.String(), result.Change.String())
	}
	got := []string{result.Inputs[0].OutPoint.String(), result.Inputs[1].OutPoint.String(), result.Inputs[2].OutPoint.String()}
	want := []string{"c:0", "a:0", "b:0"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("input %d got %s want %s", i, got[i], want[i])
		}
	}
}

func TestSelectCanonicalInputsInsufficient(t *testing.T) {
	contract := testContract(t)
	_, err := SelectCanonicalInputs(CanonicalSelectionRequest{
		Contract:  contract,
		AssetName: "ordx:ft:gas",
		Required:  mustDefaultDecimal(t, 10),
		Available: []UTXO{mustUTXO(t, OutPoint{TxID: "a", Vout: 0}, contract, "ordx:ft:gas", 1, 0)},
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("got %v want ErrInsufficientFunds", err)
	}
}
