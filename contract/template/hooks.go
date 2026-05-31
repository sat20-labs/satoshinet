package template

type FundingStateApplier interface {
	ApplyFundingState(state *TemplateRuntimeState, outputs []ContractOutput, gasAssetName string) (bool, error)
}

type GasFundingStateApplier interface {
	ApplyGasFundingState(state *TemplateRuntimeState, outputs []ContractOutput, gasAssetName string) (bool, error)
}
