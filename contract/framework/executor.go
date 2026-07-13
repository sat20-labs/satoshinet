package framework

import (
	"fmt"

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
	parsed, err := ParseTx(tx, e.cfg.Resolver(e.prefix()), e.cfg.ParseSpec)
	if err != nil {
		return err
	}
	return e.ExecuteParsedTx(tx, parsed)
}

func (e *Executor) ExecuteParsedTx(tx *wire.MsgTx, parsed ParsedTx) error {
	contractTx := ContractTxFromParsed(tx, parsed, e.cfg.Backend.ContractType())
	switch parsed.Type {
	case 0:
		return e.executeDefaultTx(tx, contractTx)
	case contract.TxTypeDeploy:
		if err := e.resolveActor(tx, parsed, &contractTx); err != nil {
			return err
		}
		_, err := e.cfg.Backend.Deploy(e.blockContext(tx, parsed), contractTx)
		return err
	case contract.TxTypeInvoke:
		if err := e.resolveActor(tx, parsed, &contractTx); err != nil {
			return err
		}
		_, err := e.cfg.Backend.Invoke(e.blockContext(tx, parsed), contractTx)
		return err
	case contract.TxTypeResult:
		return fmt.Errorf("%s RESULT transactions are built by block execution and are not accepted as external input", e.cfg.Backend.Name())
	case contract.TxTypeCoinbaseStateRoot:
		return nil
	default:
		return fmt.Errorf("unsupported %s tx type %d", e.cfg.Backend.Name(), parsed.Type)
	}
}

func (e *Executor) executeDefaultTx(tx *wire.MsgTx, contractTx contract.Tx) error {
	outputs, err := contract.FindDefaultInvokeOutputs(tx, e.prefix(), e.cfg.Backend.ContractType())
	if err != nil || len(outputs) == 0 {
		return err
	}
	for _, output := range outputs {
		funding := ContractOutputFromDefaultInvoke(output)
		defaultTx := contractTx.Clone()
		defaultTx.Kind = contract.TxTypeInvoke
		defaultTx.Action = contract.ContractInvokeAPIDefault
		defaultTx.Contract = output.Contract
		defaultTx.GasLimit = contract.DefaultInvokeGasForType(e.cfg.Backend.ContractType())
		defaultTx.Funding = ContractFundingOutputs([]ContractOutput{funding})
		if err := e.resolveActor(tx, ParsedTx{}, &defaultTx); err != nil {
			return err
		}
		okOutcome, ok, err := e.cfg.Backend.DefaultInvoke(e.blockContext(tx, ParsedTx{}), defaultTx, defaultTx.Funding[0])
		_ = okOutcome
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
	}
	return nil
}

func (e *Executor) resolveActor(tx *wire.MsgTx, parsed ParsedTx, contractTx *contract.Tx) error {
	if e.cfg.ResolveActor != nil {
		actor, err := e.cfg.ResolveActor(tx, contractTx.Clone())
		if err != nil {
			return err
		}
		contractTx.Actor = actor
	}
	if e.cfg.ResolveRefundRecipient != nil {
		recipient, ok, err := e.cfg.ResolveRefundRecipient(tx, contractTx.Clone())
		if err != nil {
			return err
		}
		if ok {
			contractTx.GasRefundRecipient = recipient
		}
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
