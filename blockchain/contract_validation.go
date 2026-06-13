package blockchain

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractapi "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	"github.com/sat20-labs/satoshinet/contract/evm"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

type CompositeContractBlockValidatorConfig struct {
	ChainParams *chaincfg.Params

	TemplateValidator TemplateBlockValidator
	EVMValidator      EVMBlockValidator
	AgentValidator    AgentBlockValidator
}

type CompositeContractBlockValidator struct {
	cfg CompositeContractBlockValidatorConfig
}

type contractBlockActivity struct {
	Template bool
	EVM      bool
	Agent    bool
}

func NewCompositeContractBlockValidator(cfg CompositeContractBlockValidatorConfig) *CompositeContractBlockValidator {
	return &CompositeContractBlockValidator{cfg: cfg}
}

func (v *CompositeContractBlockValidator) ValidateContractBlock(block *btcutil.Block, view *UtxoViewpoint) error {
	activity, err := v.blockActivity(block, view)
	if err != nil {
		return err
	}

	if v.cfg.TemplateValidator != nil && activity.Template {
		if err := v.cfg.TemplateValidator.ValidateTemplateBlock(block, view); err != nil {
			return err
		}
	}
	if v.cfg.EVMValidator != nil && activity.EVM {
		if err := v.cfg.EVMValidator.ValidateEVMBlock(block, view); err != nil {
			return err
		}
	}
	if v.cfg.AgentValidator != nil && activity.Agent {
		if err := v.cfg.AgentValidator.ValidateAgentBlock(block, view); err != nil {
			return err
		}
	}
	if activity.Template || activity.EVM || activity.Agent {
		return v.verifyCombinedStateRoot(block, activity.Template, activity.EVM, activity.Agent)
	}
	return nil
}

func (v *CompositeContractBlockValidator) TemplateBlockPostState(hash *chainhash.Hash) (*tmplcontract.RuntimeStore, bool) {
	provider, ok := v.cfg.TemplateValidator.(TemplateBlockStateProvider)
	if !ok {
		return nil, false
	}
	return provider.TemplateBlockPostState(hash)
}

func (v *CompositeContractBlockValidator) EVMBlockPostState(hash *chainhash.Hash) (*evm.MemoryStateDB, bool) {
	provider, ok := v.cfg.EVMValidator.(EVMBlockStateProvider)
	if !ok {
		return nil, false
	}
	return provider.EVMBlockPostState(hash)
}

func (v *CompositeContractBlockValidator) AgentBlockPostState(hash *chainhash.Hash) (*agentcontract.RuntimeStore, bool) {
	provider, ok := v.cfg.AgentValidator.(AgentBlockStateProvider)
	if !ok {
		return nil, false
	}
	return provider.AgentBlockPostState(hash)
}

func (v *CompositeContractBlockValidator) verifyCombinedStateRoot(block *btcutil.Block, hasTemplateWork, hasEVMWork, hasAgentWork bool) error {
	var templateRoot [32]byte
	if hasTemplateWork {
		provider, ok := v.cfg.TemplateValidator.(TemplateBlockStateProvider)
		if !ok {
			return ruleError(ErrInvalidEVMBlock, "template validator cannot expose post-state")
		}
		postState, ok := provider.TemplateBlockPostState(block.Hash())
		if !ok || postState == nil {
			return ruleError(ErrInvalidEVMBlock, "missing template post-state")
		}
		templateRoot = postState.StateRoot()
	}
	var evmRoot [32]byte
	if hasEVMWork {
		provider, ok := v.cfg.EVMValidator.(EVMBlockStateProvider)
		if !ok {
			return ruleError(ErrInvalidEVMBlock, "EVM validator cannot expose post-state")
		}
		postState, ok := provider.EVMBlockPostState(block.Hash())
		if !ok || postState == nil {
			return ruleError(ErrInvalidEVMBlock, "missing EVM post-state")
		}
		evmRoot = postState.StateRoot()
	}
	var agentRoot [32]byte
	if hasAgentWork {
		provider, ok := v.cfg.AgentValidator.(AgentBlockStateProvider)
		if !ok {
			return ruleError(ErrInvalidEVMBlock, "agent validator cannot expose post-state")
		}
		postState, ok := provider.AgentBlockPostState(block.Hash())
		if !ok || postState == nil {
			return ruleError(ErrInvalidEVMBlock, "missing agent post-state")
		}
		agentRoot = postState.StateRoot()
	}
	expected := contractcommon.CombineStateRoots(templateRoot, evmRoot, agentRoot)
	payload, found, err := contractapi.FindCoinbaseStateRoot(block.Transactions()[0].MsgTx())
	if err != nil {
		return ruleError(ErrInvalidEVMBlock, fmt.Sprintf("combined contract state root: %v", err))
	}
	if !found {
		return ruleError(ErrInvalidEVMBlock, "combined contract state root: missing state root commitment")
	}
	if payload.StateRoot != expected {
		return ruleError(ErrInvalidEVMBlock,
			fmt.Sprintf("combined contract state root mismatch: committed=%x expected=%x template=%x evm=%x agent=%x",
				payload.StateRoot, expected, templateRoot, evmRoot, agentRoot))
	}
	return nil
}

func blockContainsContractTypeWork(block *btcutil.Block, params *chaincfg.Params, contractType byte) bool {
	if block == nil || len(block.Transactions()) == 0 {
		return false
	}
	return contractengine.BlockHasContractTypeWork(block.Transactions()[1:], params, contractType)
}

func (v *CompositeContractBlockValidator) blockActivity(block *btcutil.Block,
	view *UtxoViewpoint) (contractBlockActivity, error) {

	var activity contractBlockActivity
	if block == nil || len(block.Transactions()) == 0 {
		return activity, nil
	}
	for i, tx := range block.Transactions()[1:] {
		class, found, err := contractengine.ClassifyTxForBlockOrder(tx.MsgTx(), v.cfg.ChainParams)
		if err != nil {
			return activity, ruleError(ErrInvalidEVMBlock,
				fmt.Sprintf("malformed contract transaction %v at index %d: %v",
					tx.Hash(), i+1, err))
		}
		if !found {
			continue
		}
		if class.IsWork() {
			markContractActivity(&activity, class.ContractType)
			defaultTypes, err := contractengine.DefaultInvokeContractTypes(tx.MsgTx(), v.cfg.ChainParams)
			if err != nil {
				return activity, ruleError(ErrInvalidEVMBlock,
					fmt.Sprintf("invalid default contract invoke %v at index %d: %v", tx.Hash(), i+1, err))
			}
			for contractType := range defaultTypes {
				markContractActivity(&activity, contractType)
			}
			continue
		}
		if class.IsResult() {
			resultActivity, err := contractResultActivity(tx.MsgTx(), view, v.cfg.ChainParams)
			if err != nil {
				return activity, ruleError(ErrInvalidEVMBlock,
					fmt.Sprintf("invalid CONTRACT_RESULT %v at index %d: %v",
						tx.Hash(), i+1, err))
			}
			activity.Template = activity.Template || resultActivity.Template
			activity.EVM = activity.EVM || resultActivity.EVM
			activity.Agent = activity.Agent || resultActivity.Agent
		}
	}
	return activity, nil
}

func markContractActivity(activity *contractBlockActivity, contractType byte) {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		activity.Template = true
	case contractcommon.ContractTypeEVM:
		activity.EVM = true
	case contractcommon.ContractTypeAgent:
		activity.Agent = true
	}
}

func contractResultActivity(tx *wire.MsgTx, view *UtxoViewpoint,
	params *chaincfg.Params) (contractBlockActivity, error) {

	var activity contractBlockActivity
	if tx == nil {
		return activity, fmt.Errorf("missing transaction")
	}
	if view == nil {
		return activity, fmt.Errorf("missing UTXO view")
	}
	prefix := contractValidationPrefixForParams(params)
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			return activity, fmt.Errorf("nil input")
		}
		entry := view.LookupEntry(txIn.PreviousOutPoint)
		if entry == nil {
			continue
		}
		contract, ok, err := contractcommon.ParseContractPkScript(entry.PkScript(), prefix)
		if err != nil {
			return activity, err
		}
		if !ok {
			continue
		}
		markContractActivity(&activity, contract.ContractType())
	}
	if !activity.Template && !activity.EVM && !activity.Agent {
		return activity, fmt.Errorf("result spends no contract UTXO")
	}
	return activity, nil
}

func contractValidationPrefixForParams(params *chaincfg.Params) string {
	if params == nil {
		return contractcommon.TestnetContractPrefix
	}
	if params.Net == wire.MainNet {
		return contractcommon.MainnetContractPrefix
	}
	return contractcommon.TestnetContractPrefix
}

func previousOutputScriptResolver(view *UtxoViewpoint) func(wire.OutPoint) ([]byte, bool) {
	return func(outpoint wire.OutPoint) ([]byte, bool) {
		if view == nil {
			return nil, false
		}
		entry := view.LookupEntry(outpoint)
		if entry == nil {
			return nil, false
		}
		return append([]byte(nil), entry.PkScript()...), true
	}
}
