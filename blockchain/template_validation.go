package blockchain

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

type TemplateRuntimeFactory func(block *btcutil.Block, view *UtxoViewpoint) (*template.RuntimeStore, error)

type TemplateBlockExecutionConfig struct {
	ChainParams *chaincfg.Params

	ContractPrefix string
	GasConfig      template.GasConfig
	Registry       *template.Registry

	NewRuntime          TemplateRuntimeFactory
	ResolveInvoker      template.InvokerResolver
	ResolveOutput       template.ResultOutputResolver
	ContractUTXOs       template.ContractUTXOProvider
	VerifyResult        func(resultTx *wire.MsgTx, expected []template.ResultPlan, status template.ResultStatus) error
	SkipStateRootVerify bool
}

type TemplateBlockExecutionValidator struct {
	cfg TemplateBlockExecutionConfig

	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*template.RuntimeStore
}

func NewTemplateBlockExecutionValidator(cfg TemplateBlockExecutionConfig) *TemplateBlockExecutionValidator {
	return &TemplateBlockExecutionValidator{
		cfg:        cfg,
		postStates: make(map[chainhash.Hash]*template.RuntimeStore),
	}
}

func (v *TemplateBlockExecutionValidator) ValidateTemplateBlock(block *btcutil.Block, view *UtxoViewpoint) error {
	if block == nil {
		return templateBlockRuleError("missing block")
	}
	if view == nil {
		return templateBlockRuleError("missing UTXO view")
	}
	txs := block.Transactions()
	if len(txs) == 0 {
		return templateBlockRuleError("missing coinbase transaction")
	}

	prefix := v.contractPrefix()
	hasRoot, hasExecution, err := v.scanTemplateWork(block, prefix)
	if err != nil {
		return err
	}
	if !hasRoot && !hasExecution {
		return nil
	}
	if hasExecution && !hasRoot {
		return templateBlockRuleError("missing template state root commitment")
	}

	store, err := v.runtime(block, view)
	if err != nil {
		return templateBlockRuleError("load template runtime: %v", err)
	}
	gasConfig := v.cfg.GasConfig
	gasConfig.GasAssetName = contractcommon.GasAssetNameAtHeight(int64(block.Height()))
	blockTxs := make([]*wire.MsgTx, 0, len(txs)-1)
	resultTxs := make([]*wire.MsgTx, 0)
	for _, tx := range txs[1:] {
		info, err := template.ClassifyTxForBlockOrder(tx.MsgTx(), prefix)
		if err != nil || !info.IsTemplate {
			continue
		}
		if info.Type == template.TxTypeResult {
			activity, err := contractResultActivity(tx.MsgTx(), view, v.cfg.ChainParams)
			if err != nil {
				return templateBlockRuleError("template result activity: %v", err)
			}
			if !activity.Template {
				continue
			}
			resultTxs = append(resultTxs, tx.MsgTx())
			continue
		}
		blockTxs = append(blockTxs, tx.MsgTx())
	}

	executed, err := template.ExecuteBlock(template.BlockExecutionRequest{
		Txs:            blockTxs,
		Store:          store,
		Registry:       v.cfg.Registry,
		ContractPrefix: prefix,
		GasConfig:      v.cfg.GasConfig,
		BlockHeight:    int64(block.Height()),
		ResolveInvoker: template.LastInputPreviousOutputInvokerResolver(
			v.cfg.ChainParams, previousOutputScriptResolver(view)),
	})
	if err != nil {
		return templateBlockRuleError("validate template block: %v", err)
	}
	if hasRoot && !v.cfg.SkipStateRootVerify {
		if err := template.VerifyCoinbaseStateRoot(txs[0].MsgTx(), executed.StateRoot); err != nil {
			return templateBlockRuleError("template state root: %v", err)
		}
	}
	contractUTXOs := template.ContractUTXOProviderWithTxOutputs(v.cfg.ContractUTXOs, blockTxs, prefix)
	resultPlans, err := template.AugmentResultPlans(executed.ResultPlans, store, gasConfig, contractUTXOs)
	if err != nil {
		return templateBlockRuleError("template result plan: %v", err)
	}
	if len(resultPlans) == 0 {
		if len(resultTxs) != 0 {
			return templateBlockRuleError("unexpected template RESULT transaction")
		}
	} else if len(resultTxs) > 1 {
		return templateBlockRuleError("unexpected extra template RESULT transactions")
	}
	if err := v.verifyResults(resultTxs, resultPlans); err != nil {
		return templateBlockRuleError("template result: %v", err)
	}
	v.rememberPostState(block.Hash(), store.Clone())
	return nil
}

func (v *TemplateBlockExecutionValidator) TemplateBlockPostState(hash *chainhash.Hash) (*template.RuntimeStore, bool) {
	if hash == nil {
		return nil, false
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	state, ok := v.postStates[*hash]
	if !ok {
		return nil, false
	}
	return state.Clone(), true
}

func (v *TemplateBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *template.RuntimeStore) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *TemplateBlockExecutionValidator) scanTemplateWork(block *btcutil.Block, prefix string) (bool, bool, error) {
	txs := block.Transactions()
	_, hasRoot, err := template.FindCoinbaseStateRoot(txs[0].MsgTx())
	if err != nil {
		return false, false, templateBlockRuleError("invalid template state root: %v", err)
	}
	hasExecution := false
	for _, tx := range txs[1:] {
		info, err := template.ClassifyTxForBlockOrder(tx.MsgTx(), prefix)
		if err != nil {
			continue
		}
		if info.IsTemplate && info.Type != template.TxTypeCoinbaseStateRoot {
			hasExecution = true
		}
	}
	return hasRoot, hasExecution, nil
}

func (v *TemplateBlockExecutionValidator) runtime(block *btcutil.Block, view *UtxoViewpoint) (*template.RuntimeStore, error) {
	if v.cfg.NewRuntime != nil {
		return v.cfg.NewRuntime(block, view)
	}
	return template.NewRuntimeStore(), nil
}

func (v *TemplateBlockExecutionValidator) verifyResults(resultTxs []*wire.MsgTx, plans []template.ResultPlan) error {
	if len(plans) == 0 {
		if len(resultTxs) != 0 {
			return fmt.Errorf("unexpected template RESULT transactions")
		}
		return nil
	}
	if len(resultTxs) != 1 {
		return fmt.Errorf("template result transaction count mismatch: got %d want 1", len(resultTxs))
	}
	if v.cfg.VerifyResult != nil {
		return v.cfg.VerifyResult(resultTxs[0], plans, template.ResultStatusSuccess)
	}
	verifier := template.CanonicalResultVerifier{ResolveOutput: v.cfg.ResolveOutput}
	return verifier.Verify(resultTxs[0], plans, template.ResultStatusSuccess)
}

func (v *TemplateBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	if v.cfg.ChainParams != nil {
		return template.ContractPrefixForNet(v.cfg.ChainParams.Net)
	}
	return template.TestnetContractPrefix
}

func templateBlockRuleError(format string, args ...interface{}) error {
	return ruleError(ErrInvalidEVMBlock, fmt.Sprintf(format, args...))
}
