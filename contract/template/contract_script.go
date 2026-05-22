package template

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
		return ParseContractPkScript(pkScript, prefix)
	}
}
