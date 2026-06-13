package evm

import evmcommon "github.com/sat20-labs/satoshinet/contract"

func DeriveCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) (ContractAddress, error) {
	return evmcommon.DeriveEVMCreateContractAddress(prefix, caller, nonce)
}

func MustDeriveCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) ContractAddress {
	addr, err := DeriveCreateContractAddress(prefix, caller, nonce)
	if err != nil {
		panic(err)
	}
	return addr
}
