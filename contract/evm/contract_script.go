package evm

import evmcommon "github.com/sat20-labs/satoshinet/contract/common"

var (
	ContractPkScript      = evmcommon.ContractPkScript
	ParseContractPkScript = evmcommon.ParseContractPkScript
	IsContractPkScript    = evmcommon.IsContractPkScript
)

func StandardContractScriptResolver(prefix string) ContractScriptResolver {
	return func(pkScript []byte) (ContractAddress, bool, error) {
		addr, ok, err := ParseContractPkScript(pkScript, prefix)
		if err != nil || !ok {
			return addr, ok, err
		}
		if addr.ContractType() != ContractTypeEVM {
			return ContractAddress{}, false, nil
		}
		return addr, true, nil
	}
}
