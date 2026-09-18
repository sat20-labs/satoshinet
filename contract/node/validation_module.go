package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

// Standalone module validation checks execution and Result settlement. The
// composite coordinator checks the combined commitment only after all modules
// have been replayed. Both paths use the same block-specific module adapter.
func validateStandaloneModule(block *btcutil.Block, view *blockchain.UtxoViewpoint,
	params *chaincfg.Params, prefix string, typ contractframework.ModuleType,
	factory ContractBlockModuleFactory, recorder ContractBlockStateRecorder) error {

	if block == nil || len(block.Transactions()) == 0 {
		return contractBlockRuleError("missing block or coinbase transaction")
	}
	if view == nil {
		return contractBlockRuleError("missing UTXO view")
	}
	hasRoot, split, err := splitBlockContractTxs(block, view, params, prefix)
	if err != nil {
		return contractBlockRuleError("split contract work: %v", err)
	}
	module, err := factory.ContractBlockModule(block, view)
	if err != nil {
		return err
	}
	exec, err := module.ExecuteWorkBlock(contractframework.WorkExecutionRequest{Txs: split.WorkTxs[typ], Prefix: prefix})
	if err != nil {
		return contractBlockRuleError("execute %s: %v", module.Name(), err)
	}
	activityHint := false
	if provider, ok := factory.(ContractBlockActivityProvider); ok {
		activityHint, err = provider.HasContractBlockActivity(block, view)
		if err != nil {
			return err
		}
	}
	// Keep standalone validation aligned with the composite coordinator. A due
	// trigger is contract activity even when its current managed gas is
	// insufficient and execution therefore leaves the state unchanged.
	active := activityHint || exec.StateChanged || len(split.WorkTxs[typ]) != 0 || len(split.ResultTxs[typ]) != 0
	if active && !hasRoot {
		return contractBlockRuleError("missing %s state root commitment", module.Name())
	}
	if err := module.VerifyResultTxs(contractframework.ResultVerifyRequest{ResultTxs: split.ResultTxs[typ], Prefix: prefix}, exec); err != nil {
		return contractBlockRuleError("verify %s Result: %v", module.Name(), err)
	}
	if active || hasRoot {
		if recorder == nil {
			return fmt.Errorf("missing module state recorder")
		}
		return recorder.RecordContractBlockState(block, exec)
	}
	return nil
}
