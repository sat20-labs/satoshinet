package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func (e *Executor) executeDefaultTx(raw *wire.MsgTx, envelope contract.Tx) error {
	outputs, err := contract.FindDefaultInvokeOutputsWithTxID(raw, e.prefix(), e.cfg.Backend.ContractType(), envelope.TxID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCallAdmission, err)
	}
	if len(outputs) == 0 {
		return nil
	}
	calls := make([]contract.Tx, 0, len(outputs))
	// Finish envelope admission before entering any business callback. A bad
	// later output must not leave earlier calls partially executed.
	for _, output := range outputs {
		quantity := contract.ManagedBalance{Value: output.Value, Assets: output.Assets}
		if err := quantity.Validate(); err != nil {
			return fmt.Errorf("%w: default funding output %d: %v", ErrCallAdmission, output.Vout, err)
		}
		call := envelope.Clone()
		call.Kind = contract.TxTypeInvoke
		call.Action = contract.ContractInvokeAPIDefault
		call.Contract = output.Contract
		call.Payload = nil
		call.Nonce = 0
		call.GasLimit = contract.DefaultInvokeGasForType(e.cfg.Backend.ContractType())
		call.Funding = ContractFundingOutputs([]ContractOutput{ContractOutputFromDefaultInvoke(output)})
		if err := ValidateInvokeGasLimit(call.GasLimit, e.cfg.Context.GasConfig); err != nil {
			return fmt.Errorf("%w: default invoke gas: %v", ErrCallAdmission, err)
		}
		if err := e.resolveActor(raw, ParsedTx{}, &call); err != nil {
			return err
		}
		calls = append(calls, call)
	}
	seenCalls := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		ctx := e.blockContext(raw, ParsedTx{})
		ctx.TxID = call.TxID
		allowed, err := e.checkLifecycle(call)
		if err != nil {
			return err
		}
		var outcome ExecutionOutcome
		var handled bool
		if allowed {
			outcome, handled, err = e.cfg.Backend.DefaultInvoke(ctx, call, call.Funding[0].Clone())
			if err != nil {
				return err
			}
		}
		if !handled {
			outcome, err = e.cfg.Backend.RejectFunding(ctx, call)
			if err != nil {
				return err
			}
			if outcome.Status == contract.ResultStatusSuccess {
				return fmt.Errorf("%w: rejected default invoke returned success", ErrAccountingInvariant)
			}
		}
		if err := validateDefaultOutcome(call, outcome); err != nil {
			return err
		}
		if _, duplicate := seenCalls[outcome.CallID]; duplicate {
			return fmt.Errorf("%w: duplicate default invoke call ID", ErrAccountingInvariant)
		}
		seenCalls[outcome.CallID] = struct{}{}
		if err := e.acceptOutcome(outcome, raw, call.TxID); err != nil {
			return err
		}
	}
	return nil
}

// Every default outcome is bound to exactly its own funding output. Successful
// calls always require a Result. A failed call may omit the Result only when its
// own funding cannot pay either the gas-asset fee or the 10-sat fallback; that
// funding then remains physical-only and becomes unmanaged surplus.
func validateDefaultOutcome(call contract.Tx, outcome ExecutionOutcome) error {
	if len(call.Funding) != 1 {
		return fmt.Errorf("%w: invalid default call funding", ErrAccountingInvariant)
	}
	input := call.Funding[0].OutPoint
	want := OutPoint{TxID: input.TxID, Vout: input.Vout}
	if outcome.Kind != ExecutionKindInvoke || outcome.Type != contract.TxTypeInvoke ||
		outcome.TxID != call.TxID || !outcome.Contract.Equal(call.Contract) || outcome.CallID == "" ||
		len(outcome.FundingInputs) != 1 || outcome.FundingInputs[0] != want || outcome.CloseContract {
		return fmt.Errorf("%w: default invoke outcome is not bound to its funding output", ErrAccountingInvariant)
	}
	if outcome.Status == contract.ResultStatusSuccess && !outcome.RequiresResult {
		return fmt.Errorf("%w: successful default invoke has no Result", ErrAccountingInvariant)
	}
	if outcome.GasUsed < 0 || outcome.GasUsed > call.GasLimit {
		return fmt.Errorf("%w: default invoke exceeds its gas budget", ErrAccountingInvariant)
	}
	return nil
}
