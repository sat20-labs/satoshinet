package evm

import gethcommon "github.com/ethereum/go-ethereum/common"

func GethAddress(a EVMAddress) gethcommon.Address {
	return gethcommon.BytesToAddress(a[:])
}

func EVMAddressFromGeth(a gethcommon.Address) EVMAddress {
	var out EVMAddress
	copy(out[:], a.Bytes())
	return out
}

func ContractGethAddress(a ContractAddress) gethcommon.Address {
	return GethAddress(ContractAddressHash(a))
}
