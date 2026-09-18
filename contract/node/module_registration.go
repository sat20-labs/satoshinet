package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/mining"
)

// ModuleRegistration is node wiring, not a second execution framework. It binds
// the existing module adapter, validator's parent-state loader and state codec.
// Additional runtimes register here rather than extending central type switches.
type ModuleRegistration struct {
	Descriptor       contractframework.ModuleDescriptor
	NewMiningModule  func(Config, mining.ContractBuildRequest) (contractframework.Module, error)
	NewValidator     func(Config) (ContractModuleBlockValidator, error)
	StateCodec       ModuleStateCodec
}

type RegisteredContractValidator struct {
	Descriptor contractframework.ModuleDescriptor
	Validator  ContractModuleBlockValidator
}

// A block-specific adapter captures the candidate parent snapshot and block
// context. The same framework coordinator subsequently executes/verifies it.
type ContractBlockModuleFactory interface {
	ContractBlockModule(*btcutil.Block, *blockchain.UtxoViewpoint) (contractframework.Module, error)
}

type ContractBlockStateRecorder interface {
	RecordContractBlockState(*btcutil.Block, contractframework.ExecutionResult) error
}

func contractModuleRegistrations(cfg Config) ([]ModuleRegistration, error) {
	codecs := defaultModuleStateCodecs()
	registered := []ModuleRegistration{
		{Descriptor: templateModuleDescriptor(), NewMiningModule: newTemplateMiningModule,
			NewValidator: NewTemplateBlockValidator, StateCodec: codecs[contractframework.ModuleTemplate]},
		{Descriptor: evmModuleDescriptor(), NewMiningModule: newEVMMiningModule,
			NewValidator: NewEVMBlockValidator, StateCodec: codecs[contractframework.ModuleEVM]},
		{Descriptor: agentModuleDescriptor(), NewMiningModule: newAgentMiningModule,
			NewValidator: NewAgentBlockValidator, StateCodec: codecs[contractframework.ModuleAgent]},
	}
	registered = append(registered, cfg.AdditionalModules...)
	seen := make(map[contractframework.ModuleType]struct{}, len(registered))
	for _, registration := range registered {
		typ := registration.Descriptor.Type()
		if typ == 0 || registration.Descriptor.Name() == "" || registration.Descriptor.ClassifyOrder == nil ||
			registration.NewMiningModule == nil || registration.NewValidator == nil ||
			registration.StateCodec.Module != typ || registration.StateCodec.Store == nil || registration.StateCodec.Delete == nil {
			return nil, fmt.Errorf("incomplete contract module registration %d", typ)
		}
		if _, exists := seen[typ]; exists {
			return nil, fmt.Errorf("duplicate contract module registration %d", typ)
		}
		seen[typ] = struct{}{}
	}
	return registered, nil
}

func normalizedServiceConfig(cfg Config) (Config, error) {
	bootstrap := cfg.BootstrapAddress
	for _, address := range []string{cfg.GasConfig.BootstrapAddress, cfg.AgentRuntime.BootstrapAddress} {
		if address == "" {
			continue
		}
		if bootstrap != "" && bootstrap != address {
			return Config{}, fmt.Errorf("conflicting bootstrap settlement recipients")
		}
		bootstrap = address
	}
	cfg.BootstrapAddress = bootstrap
	cfg.GasConfig.BootstrapAddress = bootstrap
	cfg.AgentRuntime.BootstrapAddress = bootstrap
	cfg.AgentRuntime.ChainParams = cfg.ChainParams
	return cfg, nil
}
