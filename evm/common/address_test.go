package common

import "testing"

func TestContractAddressRoundTrip(t *testing.T) {
	addr, err := ParseEVMAddressHex("0x00112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeContractAddress(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(contract) {
		t.Fatalf("decoded mismatch: got %+v want %+v", decoded, contract)
	}
}
