package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
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

func testContractWithHash(t *testing.T, addr EVMAddress) ContractAddress {
	t.Helper()
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, addr)
	require.NoError(t, err)
	return contract
}
