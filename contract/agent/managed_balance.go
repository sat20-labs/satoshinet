package agent

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func (s *RuntimeStore) ManagedBalance(addr ContractAddress) (*contractcommon.ManagedBalance, bool) {
	runtime, ok := s.Get(addr)
	if !ok || runtime == nil {
		return nil, false
	}
	return &runtime.managed, true
}

func (s *RuntimeStore) ContractClosed(addr ContractAddress) (bool, error) {
	runtime, ok := s.Get(addr)
	return ok && runtime != nil && runtime.state.Closed, nil
}

func (e *Backend) Lifecycle(addr ContractAddress) (contractcommon.ContractLifecycle, bool, error) {
	runtime, ok := e.Store.Get(addr)
	if !ok || runtime == nil {
		return contractcommon.ContractLifecycle{}, false, nil
	}
	return contractcommon.ContractLifecycle{
		Deployer: runtime.deployer, Flags: runtime.deploy.Flags, Closed: runtime.state.Closed,
	}, true, nil
}

func (e *Backend) ManagedBalance(addr ContractAddress) (*contractcommon.ManagedBalance, bool) {
	return e.Store.ManagedBalance(addr)
}

func (e *Backend) RejectFunding(_ contractframework.ExecutionContext,
	tx contractcommon.Tx) (contractframework.ExecutionOutcome, error) {

	if len(tx.Funding) != 1 {
		return contractframework.ExecutionOutcome{}, fmt.Errorf("%w: invalid agent funding", contractframework.ErrCallAdmission)
	}
	cfg := e.GasConfig.Normalize()
	fee, err := e.resultFee()
	if err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	output := contractframework.ContractOutputFromFunding(tx.Funding[0])
	return e.appendFundingFailure(contractframework.FundingFailureRequest{
		Height: e.BlockHeight, TxID: tx.TxID, Kind: ExecutionKindInvoke,
		Contract: tx.Contract, CallID: DeriveInvokeCallID(tx.TxID, output.Vout, tx.Contract),
		Recipient: tx.Actor, Funding: []ContractOutput{output}, GasLimit: tx.GasLimit,
		GasAsset: cfg.GasAssetName, GasFee: fee,
	})
}

func (e *Backend) appendFundingFailure(req contractframework.FundingFailureRequest) (contractframework.ExecutionOutcome, error) {
	outcome, err := contractframework.FundingFailureOutcome(req)
	if err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	if outcome.RequiresResult {
		plan, err := contractframework.FailureResultPlan(outcome.ToRecord())
		if err != nil {
			return contractframework.ExecutionOutcome{}, err
		}
		e.resultPlans = append(e.resultPlans, plan)
	}
	e.appendOutcome(outcome)
	return outcome, nil
}
