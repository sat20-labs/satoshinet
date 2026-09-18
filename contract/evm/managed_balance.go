package evm

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func (s *MemoryStateDB) ManagedBalance(addr ContractAddress) (*contractcommon.ManagedBalance, bool) {
	if addr.ContractType() != ContractTypeEVM {
		return nil, false
	}
	account := s.account(ContractGethAddress(addr))
	if account == nil {
		return nil, false
	}
	return &account.Managed, true
}

func (s *MemoryStateDB) ManagedContractClosed(addr ContractAddress) (bool, error) {
	return s.ContractClosed(ContractGethAddress(addr)), nil
}

func (e *Backend) Lifecycle(addr ContractAddress) (contractcommon.ContractLifecycle, bool, error) {
	if !e.Runtime.State.KnownContract(ContractGethAddress(addr)) {
		return contractcommon.ContractLifecycle{}, false, nil
	}
	account := e.Runtime.State.account(ContractGethAddress(addr))
	return contractcommon.ContractLifecycle{
		Deployer: account.DeployerAddr,
		Flags:    account.DeployFlags,
		Closed:   account.Closed,
	}, true, nil
}

func (e *Backend) ManagedBalance(addr ContractAddress) (*contractcommon.ManagedBalance, bool) {
	return e.Runtime.State.ManagedBalance(addr)
}

func (e *Backend) bindManagedSnapshots() {
	e.records = contractframework.BindExecutionBalances(e.records, e.ManagedBalance)
	e.pending = contractframework.BindExecutionBalances(e.pending, e.ManagedBalance)
}

// managedStateAssetView exposes accepted quantities, irrespective of which
// physical UTXOs happen to carry them. The current call is overlaid separately
// until the common executor accepts its successful outcome.
type managedStateAssetView struct {
	state *MemoryStateDB
}

func (v managedStateAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if v.state == nil {
		return nil, fmt.Errorf("missing EVM managed state")
	}
	account := v.state.account(GethAddress(owner))
	if account == nil {
		return zeroDecimal(), nil
	}
	if err := account.Managed.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", contractframework.ErrAccountingInvariant, err)
	}
	return account.Managed.AssetAmount(assetName)
}

func (e *Backend) RejectFunding(ctx contractframework.ExecutionContext,
	tx contractcommon.Tx) (contractframework.ExecutionOutcome, error) {

	if len(tx.Funding) != 1 {
		return contractframework.ExecutionOutcome{}, fmt.Errorf("%w: invalid EVM funding", contractframework.ErrCallAdmission)
	}
	output := contractframework.ContractOutputFromFunding(tx.Funding[0])
	recipient := tx.GasRefundRecipient
	if recipient == "" {
		recipient = tx.Actor
	}
	gasUsed := e.GasConfig.Normalize().InvokeBaseGas
	if ctx.ParsedTx.Type == 0 {
		gasUsed = 0
	}
	return e.appendFundingFailure(contractframework.FundingFailureRequest{
		Height: int64(e.Block.Number), TxID: tx.TxID, Kind: ExecutionKindInvoke,
		Contract: tx.Contract, CallID: DeriveInvokeCallID(tx.TxID, output.Vout, tx.Contract),
		Recipient: recipient, Funding: []ContractOutput{output}, GasLimit: tx.GasLimit,
		GasUsed: gasUsed, Status: ResultStatusInvalid,
	})
}

func (e *Backend) appendFundingFailure(req contractframework.FundingFailureRequest) (contractframework.ExecutionOutcome, error) {
	cfg := e.GasConfig.Normalize()
	req.GasAsset = cfg.GasAssetName
	fee, err := contractframework.RecordResultGasFee(cfg, e.settlementPrecision, ExecutionRecord{
		Height: req.Height, Kind: req.Kind, GasUsed: req.GasUsed, ResultFeeMode: ResultFeeModeGasAsset,
	})
	if err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	req.GasFee = fee
	outcome, err := contractframework.FundingFailureOutcome(req)
	if err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	if err := e.appendOutcome(outcome); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return outcome, nil
}
