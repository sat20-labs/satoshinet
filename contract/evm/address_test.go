package evm

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
	if ContractAddressHash(decoded).String() != addr.String() {
		t.Fatalf("hash mismatch: got %s want %s", ContractAddressHash(decoded).String(), addr.String())
	}
}

func TestContractAddressRejectsBadPrefix(t *testing.T) {
	addr, err := ParseEVMAddressHex("00112233445566778899aabbccddeeff00112233")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewContractAddress("bc", AddressVersionV1, ContractTypeEVM, addr); err == nil {
		t.Fatal("expected bad prefix error")
	}
}
