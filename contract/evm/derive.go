package evm

import "github.com/ethereum/go-ethereum/crypto"

func DeriveCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) (ContractAddress, error) {
	addr := EVMAddressFromGeth(crypto.CreateAddress(GethAddress(caller), nonce))
	return NewContractAddress(prefix, AddressVersionV1, ContractTypeEVM, addr)
}

func MustDeriveCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) ContractAddress {
	addr, err := DeriveCreateContractAddress(prefix, caller, nonce)
	if err != nil {
		panic(err)
	}
	return addr
}
