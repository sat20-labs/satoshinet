package agent

import contractcommon "github.com/sat20-labs/satoshinet/contract/common"

var (
	ContractPkScript      = contractcommon.ContractPkScript
	ParseContractPkScript = contractcommon.ParseContractPkScript
	IsContractPkScript    = contractcommon.IsContractPkScript
	DecodeContractAddress = contractcommon.DecodeContractAddress
)

type ContractScriptResolver func(pkScript []byte) (ContractAddress, bool, error)

func StandardContractScriptResolver(prefix string) ContractScriptResolver {
	return func(pkScript []byte) (ContractAddress, bool, error) {
		addr, ok, err := ParseContractPkScript(pkScript, prefix)
		if err != nil || !ok {
			return addr, ok, err
		}
		if addr.ContractType() != ContractTypeAgent {
			return ContractAddress{}, false, nil
		}
		return addr, true, nil
	}
}
