package evm

import evmcommon "github.com/sat20-labs/satoshinet/evm/common"

var (
	ContractPkScript      = evmcommon.ContractPkScript
	ParseContractPkScript = evmcommon.ParseContractPkScript
	IsContractPkScript    = evmcommon.IsContractPkScript
)

func StandardContractScriptResolver(prefix string) ContractScriptResolver {
	return func(pkScript []byte) (ContractAddress, bool, error) {
		return ParseContractPkScript(pkScript, prefix)
	}
}
