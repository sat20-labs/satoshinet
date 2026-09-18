package template

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func (s *RuntimeStore) ManagedBalance(addr ContractAddress) (*contractcommon.ManagedBalance, bool) {
	runtime, ok := s.Get(addr)
	if !ok || runtime == nil || runtime.base == nil {
		return nil, false
	}
	return &runtime.base.managed, true
}

func (s *RuntimeStore) ContractClosed(addr ContractAddress) (bool, error) {
	runtime, ok := s.Get(addr)
	if !ok || runtime == nil {
		return false, nil
	}
	state, err := runtime.RuntimeState()
	if err != nil {
		return false, err
	}
	return state.ClosedForContract(runtime.Contract()), nil
}

func (e *Backend) Lifecycle(addr ContractAddress) (contractcommon.ContractLifecycle, bool, error) {
	runtime, ok := e.Store.Get(addr)
	if !ok || runtime == nil {
		return contractcommon.ContractLifecycle{}, false, nil
	}
	closed, err := e.Store.ContractClosed(addr)
	if err != nil {
		return contractcommon.ContractLifecycle{}, false, err
	}
	return contractcommon.ContractLifecycle{
		Deployer: runtime.base.Deployer(), Flags: runtime.base.flags,
		Closed: closed || e.closing[addr.MustEncode()],
	}, true, nil
}

func (e *Backend) ManagedBalance(addr ContractAddress) (*contractcommon.ManagedBalance, bool) {
	return e.Store.ManagedBalance(addr)
}

func (e *Backend) RejectFunding(_ contractframework.ExecutionContext,
	tx contractcommon.Tx) (contractframework.ExecutionOutcome, error) {

	if len(tx.Funding) != 1 {
		return contractframework.ExecutionOutcome{}, fmt.Errorf("%w: invalid template funding count", contractframework.ErrCallAdmission)
	}
	gasConfig := e.GasConfig.Normalize()
	if runtime, ok := e.Store.Get(tx.Contract); ok {
		gasConfig = GasConfigForRuntime(e.GasConfig, runtime)
	}
	fee, err := e.resultFee(gasConfig)
	if err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	output := contractframework.ContractOutputFromFunding(tx.Funding[0])
	return e.appendFundingFailure(contractframework.FundingFailureRequest{
		Height: e.BlockHeight, TxID: tx.TxID, Kind: ExecutionKindInvoke,
		Contract: tx.Contract, CallID: DeriveInvokeCallID(tx.TxID, output.Vout, tx.Contract),
		Recipient: tx.Actor, Funding: []ContractOutput{output}, GasLimit: tx.GasLimit,
		GasAsset: gasConfig.GasAssetName, GasFee: fee,
	})
}

func (e *Backend) appendFundingFailure(req contractframework.FundingFailureRequest) (contractframework.ExecutionOutcome, error) {
	outcome, err := contractframework.FundingFailureOutcome(req)
	if err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	e.appendOutcome(outcome)
	return outcome, nil
}

func checkTemplateFundingAssets(c Contract, output ContractOutput, gasAsset string) error {
	assetA, assetB := defaultInvokePoolAssets(c)
	for _, asset := range output.TxAssets() {
		name := asset.Name.String()
		if name != assetA && name != assetB && name != gasAsset {
			return fmt.Errorf("template does not accept funding asset %s", name)
		}
	}
	return nil
}
