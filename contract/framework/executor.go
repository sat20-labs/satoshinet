package framework

import (
	"fmt"
	"strings"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type ActorResolver func(*wire.MsgTx, contract.Tx) (string, error)
type RefundRecipientResolver func(*wire.MsgTx, contract.Tx) (string, bool, error)

type ExecutorConfig struct {
	Backend Backend
	Prefix  string

	ParseSpec ParseSpec
	Resolver  func(string) ContractScriptResolver

	Context                ExecutionContext
	ResolveActor           ActorResolver
	ResolveRefundRecipient RefundRecipientResolver
}

type Executor struct {
	cfg ExecutorConfig
}

func NewExecutor(cfg ExecutorConfig) *Executor {
	return &Executor{cfg: cfg}
}

func (e *Executor) ExecuteBlock(txs []*wire.MsgTx) ([]ExecutionOutcome, error) {
	if e.cfg.Backend == nil {
		return nil, fmt.Errorf("missing contract backend")
	}
	if err := e.ExecuteTxs(txs); err != nil {
		return nil, err
	}
	return e.cfg.Backend.FinalizeBlock(e.blockContext(nil, ParsedTx{}))
}

func (e *Executor) ExecuteTxs(txs []*wire.MsgTx) error {
	for _, tx := range txs {
		if err := e.ExecuteTx(tx); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) ExecuteTx(tx *wire.MsgTx) error {
	if e.cfg.Backend == nil {
		return fmt.Errorf("missing contract backend")
	}
	if e.cfg.Resolver == nil {
		return fmt.Errorf("%s executor missing script resolver", e.cfg.Backend.Name())
	}
	// A damaged explicit envelope must not fall through to default execution.
	if _, _, err := contract.ClassifyTxPayloadType(tx); err != nil {
		return fmt.Errorf("%w: %v", ErrCallAdmission, err)
	}
	parsed, err := ParseTx(tx, e.cfg.Resolver(e.prefix()), e.cfg.ParseSpec)
	if err != nil {
		return err
	}
	return e.ExecuteParsedTx(tx, parsed)
}

func (e *Executor) ExecuteParsedTx(tx *wire.MsgTx, parsed ParsedTx) error {
	if e.cfg.Backend == nil || tx == nil {
		return fmt.Errorf("missing contract backend or transaction")
	}
	contractTx := ContractTxFromParsed(tx, parsed, e.cfg.Backend.ContractType())
	ctx := e.blockContext(tx, parsed)
	ctx.TxID = contractTx.TxID
	switch parsed.Type {
	case 0:
		return e.executeDefaultTx(tx, contractTx)
	case contract.TxTypeDeploy:
		if parsed.Deploy == nil {
			return fmt.Errorf("%w: missing deployment payload", ErrCallAdmission)
		}
		if err := parsed.Deploy.Flags.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrCallAdmission, err)
		}
		if err := e.resolveActor(tx, parsed, &contractTx); err != nil {
			return err
		}
		outcome, err := e.cfg.Backend.Deploy(ctx, contractTx)
		if err != nil {
			return err
		}
		return e.acceptOutcome(outcome, tx, contractTx.TxID)
	case contract.TxTypeInvoke:
		if len(contractTx.Funding) != 1 || parsed.Invoke == nil || parsed.Invoke.GasLimit <= 0 {
			return fmt.Errorf("%w: invalid invocation envelope", ErrCallAdmission)
		}
		if err := e.resolveActor(tx, parsed, &contractTx); err != nil {
			return err
		}
		allowed, err := e.checkLifecycle(contractTx)
		if err != nil {
			return err
		}
		var outcome ExecutionOutcome
		if allowed {
			outcome, err = e.cfg.Backend.Invoke(ctx, contractTx)
		} else {
			outcome, err = e.cfg.Backend.RejectFunding(ctx, contractTx)
		}
		if err != nil {
			return err
		}
		return e.acceptOutcome(outcome, tx, contractTx.TxID)
	case contract.TxTypeResult:
		return fmt.Errorf("%s RESULT transactions are built by block execution and are not accepted as external input", e.cfg.Backend.Name())
	case contract.TxTypeCoinbaseStateRoot:
		return nil
	default:
		return fmt.Errorf("unsupported %s tx type %d", e.cfg.Backend.Name(), parsed.Type)
	}
}

func (e *Executor) checkLifecycle(tx contract.Tx) (bool, error) {
	lifecycle, found, err := e.cfg.Backend.Lifecycle(tx.Contract)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if err := lifecycle.Flags.Validate(); err != nil {
		return false, fmt.Errorf("%w: invalid stored deployment policy: %v", ErrAccountingInvariant, err)
	}
	return lifecycle.CheckInvoke(tx.Action, tx.Actor) == nil, nil
}

// Only successful calls credit managed quantities. Failed-call funding stays
// in the transient refund plan and cannot become a business asset.
func (e *Executor) acceptOutcome(outcome ExecutionOutcome, raw *wire.MsgTx, rawTxID string) error {
	if outcome.Kind == 0 {
		return fmt.Errorf("%w: %s work produced no execution outcome", ErrCallAdmission, e.cfg.Backend.Name())
	}
	if outcome.Status > contract.ResultStatusInvalid {
		return fmt.Errorf("%w: unknown execution status", ErrAccountingInvariant)
	}
	if outcome.Status != contract.ResultStatusSuccess {
		if !outcome.RequiresResult && (len(outcome.AssetIntents) != 0 ||
			(outcome.GasFee != nil && outcome.GasFee.Sign() != 0) || outcome.GasRefundRecipient != "") {
			return fmt.Errorf("%w: non-refunded failure carries Result accounting", ErrAccountingInvariant)
		}
		return nil
	}
	if len(outcome.FundingInputs) == 0 {
		return nil
	}
	balance, ok := e.cfg.Backend.ManagedBalance(outcome.Contract)
	if !ok || balance == nil {
		return fmt.Errorf("%w: successful funded call has no managed balance", ErrAccountingInvariant)
	}
	if raw == nil {
		return fmt.Errorf("%w: missing funding transaction", ErrAccountingInvariant)
	}
	next := balance.Clone()
	seen := make(map[OutPoint]struct{}, len(outcome.FundingInputs))
	if rawTxID == "" {
		rawTxID = raw.TxID()
	}
	for _, input := range outcome.FundingInputs {
		if input.TxID != rawTxID || uint64(input.Vout) >= uint64(len(raw.TxOut)) {
			return fmt.Errorf("%w: funding does not belong to the current call", ErrAccountingInvariant)
		}
		if _, duplicate := seen[input]; duplicate {
			return fmt.Errorf("%w: duplicate call funding", ErrAccountingInvariant)
		}
		seen[input] = struct{}{}
		output := raw.TxOut[input.Vout]
		if output == nil {
			return fmt.Errorf("%w: nil call funding", ErrAccountingInvariant)
		}
		addr, isContract, err := contract.ParseContractPkScript(output.PkScript, e.prefix())
		if err != nil || !isContract || !addr.Equal(outcome.Contract) {
			return fmt.Errorf("%w: call funding target mismatch", ErrAccountingInvariant)
		}
		if err := next.Credit(output.Value, output.Assets); err != nil {
			return fmt.Errorf("%w: %v", ErrAccountingInvariant, err)
		}
	}
	*balance = next
	return nil
}

func (e *Executor) resolveActor(raw *wire.MsgTx, parsed ParsedTx, call *contract.Tx) error {
	if e.cfg.ResolveActor != nil {
		actor, err := e.cfg.ResolveActor(raw, call.Clone())
		if err != nil {
			return err
		}
		call.Actor = actor
	}
	if strings.TrimSpace(call.Actor) == "" {
		return fmt.Errorf("%w: missing contract actor", ErrCallAdmission)
	}
	if e.cfg.ResolveRefundRecipient != nil {
		recipient, ok, err := e.cfg.ResolveRefundRecipient(raw, call.Clone())
		if err != nil {
			return err
		}
		if ok {
			if strings.TrimSpace(recipient) == "" {
				return fmt.Errorf("%w: empty refund recipient", ErrCallAdmission)
			}
			call.GasRefundRecipient = recipient
		}
	}
	if call.GasRefundRecipient == "" {
		call.GasRefundRecipient = call.Actor
	}
	return nil
}

func (e *Executor) blockContext(tx *wire.MsgTx, parsed ParsedTx) ExecutionContext {
	ctx := e.cfg.Context
	ctx.Prefix = e.prefix()
	ctx.RawTx = tx
	ctx.ParsedTx = parsed
	return ctx
}

func (e *Executor) prefix() string {
	if e.cfg.Prefix != "" {
		return e.cfg.Prefix
	}
	if e.cfg.Context.Prefix != "" {
		return e.cfg.Context.Prefix
	}
	return contract.TestnetContractPrefix
}
