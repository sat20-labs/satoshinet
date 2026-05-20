package evm

import (
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestDeriveCreateContractAddress(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	require.Equal(t, EVMAddressFromGeth(crypto.CreateAddress(GethAddress(caller), 3)), ContractAddressHash(contract))
	require.Equal(t, gethcommon.Address(ContractAddressHash(contract)).Hex(), ContractGethAddress(contract).Hex())
}
