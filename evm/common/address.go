package common

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

type ContractAddress = btcutil.AddressContract

func NewContractAddress(prefix string, version, typ byte, hash EVMAddress) (ContractAddress, error) {
	addr, err := btcutil.NewAddressContractFromHashWithPrefix(version, typ, hash[:], prefix)
	if err != nil {
		return ContractAddress{}, err
	}
	return *addr, nil
}

func DecodeContractAddress(s string) (ContractAddress, error) {
	decoded, err := btcutil.DecodeAddress(s, &chaincfg.TestNetParams)
	if err != nil {
		return ContractAddress{}, err
	}
	contract, ok := decoded.(*btcutil.AddressContract)
	if !ok {
		return ContractAddress{}, fmt.Errorf("not a contract address: %T", decoded)
	}
	return *contract, nil
}

func ContractAddressHash(contract ContractAddress) EVMAddress {
	return EVMAddress(contract.ContractHash())
}
