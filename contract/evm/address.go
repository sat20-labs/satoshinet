package evm

import evmcommon "github.com/sat20-labs/satoshinet/contract/common"

type ContractAddress = evmcommon.ContractAddress

var (
	NewContractAddress    = evmcommon.NewContractAddress
	DecodeContractAddress = evmcommon.DecodeContractAddress
	ContractAddressHash   = evmcommon.ContractAddressHash
)
