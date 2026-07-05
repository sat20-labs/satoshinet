package template

import contractframework "github.com/sat20-labs/satoshinet/contract/framework"

func GasConfigForContract(base GasConfig, contract Contract) GasConfig {
	if contract == nil {
		return base.Normalize()
	}
	return contractframework.ApplyBaseGasConfig(base, contract.BaseGasConfig()).Normalize()
}

func GasConfigForRuntime(base GasConfig, runtime *ContractRuntime) GasConfig {
	if runtime == nil {
		return base.Normalize()
	}
	return GasConfigForContract(base, runtime.Contract())
}
