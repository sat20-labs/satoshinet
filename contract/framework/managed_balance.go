package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
)

type ManagedBalanceLookup func(contract.ContractAddress) (*contract.ManagedBalance, bool)
type ContractClosedLookup func(contract.ContractAddress) (bool, error)

// ManagedState is the quantity view of an existing engine snapshot. It does not
// introduce a database, an outpoint registry or another mutable balance source.
type ManagedState interface {
	ManagedBalance(contract.ContractAddress) (*contract.ManagedBalance, bool)
	ManagedContractClosed(contract.ContractAddress) (bool, error)
	StateRoot() [32]byte
}

func BindExecutionBalances(records []ExecutionRecord, lookup ManagedBalanceLookup) []ExecutionRecord {
	out := CloneExecutionRecords(records)
	last := make(map[string]int)
	for i := range out {
		out[i].ManagedBalance = nil
		if out[i].RequiresResult {
			last[ContractResultKey(out[i].Contract)] = i
		}
	}
	for _, i := range last {
		balance := contract.ManagedBalance{}
		if lookup != nil {
			if current, ok := lookup(out[i].Contract); ok && current != nil {
				balance = current.Clone()
			}
		}
		out[i].ManagedBalance = &balance
	}
	return out
}

// Per-call references are transient execution/Result bindings, not managed
// UTXO identities. A failed call's funding remains separate refund escrow.
func AttachCallFunding(plans []ResultPlan, records []ExecutionRecord) []ResultPlan {
	out := CloneResultPlans(plans)
	for i := range out {
		allowed := make(map[OutPoint]bool)
		if out[i].InputScope == ResultInputScopeExplicit {
			for _, input := range out[i].Inputs {
				allowed[input] = true
			}
		}
		for _, record := range records {
			if !record.RequiresResult || record.Contract.MustEncode() != out[i].Contract {
				continue
			}
			for _, input := range record.FundingInputs {
				if out[i].InputScope == ResultInputScopeExplicit && !allowed[input] {
					continue
				}
				out[i].CallFunding = append(out[i].CallFunding, input)
				if record.Status != contract.ResultStatusSuccess {
					out[i].RefundFunding = append(out[i].RefundFunding, input)
				}
			}
		}
		out[i].CallFunding = UniqueOutPoints(out[i].CallFunding)
		out[i].RefundFunding = UniqueOutPoints(out[i].RefundFunding)
	}
	return out
}

type ManagedResultRequest struct {
	Plan             ResultPlan
	View             ResultPlanUTXOView
	Managed          contract.ManagedBalance
	Closed           bool
	DeployerAddress  string
	BootstrapAddress string
	GasAssetName     string
	Precision        AssetPrecisionPolicy
}

// User transfers/refunds and fees precede retention, profit, and anomaly
// recovery. Runtime code cannot select a different unmanaged-surplus policy.
func AugmentManagedResultPlan(req ManagedResultRequest) (ResultPlan, error) {
	plan := CloneResultPlan(req.Plan)
	if plan.ManagedRemainder != nil {
		return plan, nil
	}
	if err := req.Managed.Validate(); err != nil {
		return ResultPlan{}, fmt.Errorf("%w: %v", ErrAccountingInvariant, err)
	}
	physical := contract.ManagedBalance{Value: req.View.Value, Assets: req.View.Assets.Clone()}
	if err := physical.Validate(); err != nil {
		return ResultPlan{}, fmt.Errorf("%w: %v", ErrAccountingInvariant, err)
	}
	available := make(map[OutPoint]UTXO, len(req.View.UTXOs))
	for _, utxo := range req.View.UTXOs {
		if !utxo.Contract.Equal(req.View.Contract) {
			return ResultPlan{}, fmt.Errorf("%w: mixed contract funding", ErrAccountingInvariant)
		}
		if _, duplicate := available[utxo.OutPoint]; duplicate {
			return ResultPlan{}, fmt.Errorf("%w: duplicate result input", ErrAccountingInvariant)
		}
		available[utxo.OutPoint] = utxo
	}
	for _, input := range plan.CallFunding {
		if _, found := available[input]; !found {
			return ResultPlan{}, fmt.Errorf("%w: missing call funding %s", ErrAccountingInvariant, input)
		}
	}
	budget := req.Managed.Clone()
	if plan.InputScope == ResultInputScopeExplicit {
		budget = physical.Clone()
	} else {
		for _, input := range UniqueOutPoints(plan.RefundFunding) {
			utxo, found := available[input]
			if !found {
				return ResultPlan{}, fmt.Errorf("%w: missing refund funding %s", ErrAccountingInvariant, input)
			}
			if err := budget.Credit(utxo.PhysicalValue(), utxo.TxAssets()); err != nil {
				return ResultPlan{}, fmt.Errorf("%w: %v", ErrAccountingInvariant, err)
			}
		}
	}
	anomaly := physical.Clone()
	if err := anomaly.Debit(budget.Value, budget.Assets); err != nil {
		return ResultPlan{}, fmt.Errorf("%w: physical balance does not cover managed balance and refunds: %v", ErrAccountingInvariant, err)
	}
	settlementBudget := budget.Clone()
	if plan.SatoshiFee < 0 {
		return ResultPlan{}, fmt.Errorf("%w: negative satoshi Result fee", ErrAccountingInvariant)
	}
	if plan.SatoshiFee > 0 {
		plain, err := settlementBudget.PlainValue()
		if err != nil {
			return ResultPlan{}, err
		}
		if plain < plan.SatoshiFee {
			return ResultPlan{}, fmt.Errorf("%w: insufficient plain sats for Result fee", ErrAccountingInvariant)
		}
		settlementBudget.Value -= plan.SatoshiFee
	}
	outputs := NormalizeResultOutputsPrecision(plan.Outputs, req.Precision)
	outputs = removeContractRetainOutputs(outputs, plan.Contract)
	refunds, err := resultGasRefundOutputs(plan, req.View, req.GasAssetName, req.Precision)
	if err != nil {
		return ResultPlan{}, err
	}
	outputs = append(outputs, refunds...)
	outputs, err = bindResultOutputs(outputs, req.View.Assets)
	if err != nil {
		return ResultPlan{}, err
	}
	feeOutputs, err := bindResultOutputs(NormalizeResultOutputsPrecision(plan.FeeOutputs, req.Precision), req.View.Assets)
	if err != nil {
		return ResultPlan{}, err
	}
	spends := append(CloneResultOutputs(outputs), feeOutputs...)
	fee := CloneDecimal(plan.GasFee)
	if fee.Sign() < 0 {
		return ResultPlan{}, fmt.Errorf("%w: negative Result fee", ErrAccountingInvariant)
	}
	fee = req.Precision.NormalizeUp(req.GasAssetName, fee)
	value, assets, err := physicalRemainderAfterOutputs(ResultPlanUTXOView{Value: settlementBudget.Value, Assets: settlementBudget.Assets},
		spends, req.GasAssetName, fee)
	if err != nil {
		return ResultPlan{}, fmt.Errorf("%w: %v", ErrAccountingInvariant, err)
	}
	remainder := contract.ManagedBalance{Value: value, Assets: assets}
	if plan.InputScope == ResultInputScopeExplicit && !remainder.IsZero() {
		return ResultPlan{}, fmt.Errorf("%w: refund-only Result leaves call funding unsettled", ErrAccountingInvariant)
	}
	managedOutput := ResultOutput{To: plan.Contract, Value: remainder.Value, Assets: remainder.Assets.Clone()}
	if req.Closed {
		profit, err := SplitContractProfit(managedOutput, req.DeployerAddress, req.BootstrapAddress, req.Precision)
		if err != nil {
			return ResultPlan{}, err
		}
		outputs = append(outputs, profit...)
	}
	if req.Closed && !anomaly.IsZero() {
		if req.BootstrapAddress == "" {
			return ResultPlan{}, fmt.Errorf("missing bootstrap recipient for unmanaged surplus")
		}
		outputs = append(outputs, ResultOutput{To: req.BootstrapAddress, Value: anomaly.Value, Assets: anomaly.Assets.Clone()})
	}
	// Managed/unmanaged are quantities across the whole address. Retention is
	// therefore the selected inputs' change, not the whole managed balance.
	// Active contracts retain unsolicited funds until close.
	mandatory := plan.CallFunding
	if plan.InputScope == ResultInputScopeExplicit {
		mandatory = append(append([]OutPoint(nil), mandatory...), plan.Inputs...)
	}
	spends = append(CloneResultOutputs(outputs), feeOutputs...)
	selected, err := selectResultFunding(req.View, spends, req.GasAssetName, fee, plan.SatoshiFee, mandatory)
	if err != nil {
		return ResultPlan{}, err
	}
	changeView := selected
	changeView.Value -= plan.SatoshiFee
	changeValue, changeAssets, err := physicalRemainderAfterOutputs(changeView, spends, req.GasAssetName, fee)
	if err != nil {
		return ResultPlan{}, err
	}
	change := ResultOutput{To: plan.Contract, Value: changeValue, Assets: changeAssets}
	if !ResultOutputIsZero(change) {
		outputs = append(outputs, change)
	}
	plan.GasFee = fee
	plan.GasRefunds = nil
	plan.Inputs = selected.Inputs
	plan.InputUTXOs = selected.UTXOs
	plan.FeeOutputs = feeOutputs
	plan.Outputs, err = CompactResultOutputs(outputs)
	if err != nil {
		return ResultPlan{}, err
	}
	plan.ManagedRemainder = &remainder
	return plan, nil
}

// Commit settled quantities into the candidate snapshot. Outgoing Result
// transfers to another known active contract in the same engine are passive
// credits, not invocations. Cross-engine credits are applied by the coordinator
// after all modules' results have been built/verified.
func ApplyManagedResultBalances(plans []ResultPlan, lookup ManagedBalanceLookup, closed ContractClosedLookup) error {
	if len(plans) == 0 {
		return nil
	}
	if lookup == nil || closed == nil {
		return fmt.Errorf("missing managed settlement state")
	}
	staged := make(map[*contract.ManagedBalance]contract.ManagedBalance)
	for _, plan := range plans {
		if plan.InputScope == ResultInputScopeExplicit {
			continue
		}
		if plan.ManagedRemainder == nil {
			return fmt.Errorf("%w: Result plan has not been accounted", ErrAccountingInvariant)
		}
		addr, err := contract.DecodeContractAddress(plan.Contract)
		if err != nil {
			return err
		}
		balance, exists := lookup(addr)
		if !exists || balance == nil {
			if !plan.ManagedRemainder.IsZero() {
				return fmt.Errorf("%w: unknown contract has managed remainder", ErrAccountingInvariant)
			}
			continue
		}
		if _, duplicate := staged[balance]; duplicate {
			return fmt.Errorf("%w: duplicate contract settlement", ErrAccountingInvariant)
		}
		next := plan.ManagedRemainder.Clone()
		isClosed, err := closed(addr)
		if err != nil {
			return err
		}
		if isClosed {
			next = contract.ManagedBalance{}
		}
		staged[balance] = next
	}
	for _, plan := range plans {
		source, err := contract.DecodeContractAddress(plan.Contract)
		if err != nil {
			return err
		}
		for _, output := range plan.Outputs {
			target, err := contract.DecodeContractAddress(output.To)
			if err != nil || target.Equal(source) || target.ContractType() != source.ContractType() {
				continue
			}
			if err := stageManagedCredit(staged, target, output, lookup, closed); err != nil {
				return err
			}
		}
	}
	for balance, next := range staged {
		*balance = next
	}
	return nil
}

func stageManagedCredit(staged map[*contract.ManagedBalance]contract.ManagedBalance,
	target contract.ContractAddress, output ResultOutput, lookup ManagedBalanceLookup, closed ContractClosedLookup) error {

	balance, exists := lookup(target)
	if !exists || balance == nil {
		return nil
	}
	isClosed, err := closed(target)
	if err != nil || isClosed {
		return err
	}
	next, stagedAlready := staged[balance]
	if !stagedAlready {
		next = balance.Clone()
	}
	if err := next.Credit(output.Value, output.Assets); err != nil {
		return fmt.Errorf("%w: invalid passive Result credit: %v", ErrAccountingInvariant, err)
	}
	staged[balance] = next
	return nil
}

func applyCrossModuleResultBalances(executions map[ModuleType]ExecutionResult) error {
	staged := make(map[*contract.ManagedBalance]contract.ManagedBalance)
	changed := make(map[ModuleType]bool)
	// Stable traversal also makes the first reported invariant deterministic.
	for _, sourceType := range sortedExecutionTypes(executions) {
		for _, plan := range executions[sourceType].ResultPlans {
			for _, output := range plan.Outputs {
				target, err := contract.DecodeContractAddress(output.To)
				if err != nil || ModuleType(target.ContractType()) == sourceType {
					continue
				}
				targetType := ModuleType(target.ContractType())
				exec, exists := executions[targetType]
				if !exists {
					continue
				}
				state, ok := exec.PostState.(ManagedState)
				if !ok {
					return fmt.Errorf("%w: module %d has no managed state", ErrAccountingInvariant, targetType)
				}
				if err := stageManagedCredit(staged, target, output, state.ManagedBalance, state.ManagedContractClosed); err != nil {
					return err
				}
				changed[targetType] = true
			}
		}
	}
	for balance, next := range staged {
		*balance = next
	}
	for typ := range changed {
		exec := executions[typ]
		root := exec.PostState.(ManagedState).StateRoot()
		exec.StateChanged = exec.StateChanged || root != exec.StateRoot
		exec.StateRoot = root
		executions[typ] = exec
	}
	return nil
}
