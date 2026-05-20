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

func TestContractAddressRoundTripWithTemplateHash(t *testing.T) {
	hash := [32]byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88,
		0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00,
	}
	contract, err := NewContractAddressFromHash(TestnetContractPrefix, AddressVersionV1, ContractTypeTemplate, hash[:])
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
	if got := ContractAddressHashBytes(decoded); string(got) != string(hash[:]) {
		t.Fatalf("hash mismatch: got %x want %x", got, hash)
	}
}
