package template

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
)

type ContractFlags = contract.ContractFlags

func (r *ContractRuntime) DeploymentFlags() ContractFlags {
	if r == nil || r.base == nil {
		return 0
	}
	return r.base.flags
}

func (r *ContractRuntime) CheckInvocationLifecycle(action, actor string) error {
	if r == nil || r.base == nil {
		return fmt.Errorf("missing template runtime")
	}
	state, err := r.RuntimeState()
	if err != nil {
		return err
	}
	return r.checkInvocationLifecycle(state, action, actor)
}

// The caller may reuse its decoded state through admission and execution.
// This helper neither reads storage nor publishes any state changes.
func (r *ContractRuntime) checkInvocationLifecycle(state TemplateRuntimeState, action, actor string) error {
	if r == nil || r.base == nil {
		return fmt.Errorf("missing template runtime")
	}
	if action == contract.ContractInvokeAPIClose {
		return contract.ValidateContractClose(r.base.flags,
			state.ClosedForContract(r.contract), r.base.Deployer(), actor)
	}
	if state.ClosedForContract(r.contract) {
		return contract.ErrContractClosed
	}
	return nil
}
