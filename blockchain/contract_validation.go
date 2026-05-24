package blockchain

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
)

type CompositeContractBlockValidatorConfig struct {
	ChainParams *chaincfg.Params

	TemplateValidator TemplateBlockValidator
	EVMValidator      EVMBlockValidator
}

type CompositeContractBlockValidator struct {
	cfg CompositeContractBlockValidatorConfig
}

func NewCompositeContractBlockValidator(cfg CompositeContractBlockValidatorConfig) *CompositeContractBlockValidator {
	return &CompositeContractBlockValidator{cfg: cfg}
}

func (v *CompositeContractBlockValidator) ValidateContractBlock(block *btcutil.Block, view *UtxoViewpoint) error {
	hasTemplateWork := blockContainsContractTypeWork(block, v.cfg.ChainParams, contractcommon.ContractTypeTemplate)
	hasEVMWork := blockContainsContractTypeWork(block, v.cfg.ChainParams, contractcommon.ContractTypeEVM)

	if v.cfg.TemplateValidator != nil && hasTemplateWork {
		if err := v.cfg.TemplateValidator.ValidateTemplateBlock(block, view); err != nil {
			return err
		}
	}
	if v.cfg.EVMValidator != nil && hasEVMWork {
		if err := v.cfg.EVMValidator.ValidateEVMBlock(block, view); err != nil {
			return err
		}
	}
	if hasTemplateWork || hasEVMWork {
		return v.verifyCombinedStateRoot(block, hasTemplateWork, hasEVMWork)
	}
	return nil
}

func (v *CompositeContractBlockValidator) verifyCombinedStateRoot(block *btcutil.Block, hasTemplateWork, hasEVMWork bool) error {
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
	expected := contractcommon.CombineStateRoots(templateRoot, evmRoot)
	if err := contractengine.VerifyCoinbaseStateRoot(block.Transactions()[0].MsgTx(), expected); err != nil {
		return ruleError(ErrInvalidEVMBlock, fmt.Sprintf("combined contract state root: %v", err))
	}
	return nil
}

func blockContainsContractTypeWork(block *btcutil.Block, params *chaincfg.Params, contractType byte) bool {
	if block == nil || len(block.Transactions()) == 0 {
		return false
	}
	return contractengine.BlockHasContractTypeWork(block.Transactions()[1:], params, contractType)
}
