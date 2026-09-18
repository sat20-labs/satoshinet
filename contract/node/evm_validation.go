package node

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

type EVMRuntimeFactory func(*btcutil.Block, *blockchain.UtxoViewpoint) (*evm.Runtime, error)
type EVMBlockContextBuilder func(*btcutil.Block) evm.BlockContext

type EVMBlockExecutionConfig struct {
	ChainParams         *chaincfg.Params
	ContractPrefix      string
	GasConfig           evm.GasConfig
	HistoryDB           database.DB
	NewRuntime          EVMRuntimeFactory
	BuildBlockContext   EVMBlockContextBuilder
	ResolveCaller       evm.CallerResolver
	ContractUTXOs       evm.ContractUTXOProvider
	ResolveRecipient    contractframework.ScriptRecipientResolver
	ResolveResultOutput evm.ResultOutputResolver
	ResolveResultScript evm.ResultRecipientScriptResolver
	ResolveTriggers     evm.TriggerResolver
	AssetPrecision      contractframework.AssetPrecisionResolver
	VerifyResult        evm.ResultVerifier
}

type EVMBlockExecutionValidator struct {
	cfg         EVMBlockExecutionConfig
	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*evm.MemoryStateDB
}

func NewEVMBlockExecutionValidator(cfg EVMBlockExecutionConfig) *EVMBlockExecutionValidator {
	return &EVMBlockExecutionValidator{cfg: cfg, postStates: make(map[chainhash.Hash]*evm.MemoryStateDB)}
}

func (v *EVMBlockExecutionValidator) ValidateEVMBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	return validateStandaloneModule(block, view, v.cfg.ChainParams, v.contractPrefix(), contractframework.ModuleEVM, v, v)
}

func (v *EVMBlockExecutionValidator) ValidateContractModuleBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	return v.ValidateEVMBlock(block, view)
}

func (v *EVMBlockExecutionValidator) ContractBlockModule(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractframework.Module, error) {

	if block == nil {
		return nil, evmBlockRuleError("missing block")
	}
	parent, err := v.runtime(block, view)
	if err != nil {
		return nil, evmBlockRuleError("load EVM runtime: %v", err)
	}
	if parent == nil || parent.State == nil {
		return nil, evmBlockRuleError("missing EVM runtime")
	}
	ctx, err := v.blockContext(block)
	if err != nil {
		return nil, err
	}
	gas := v.cfg.GasConfig
	gas.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	prefix := v.contractPrefix()
	resolveOutput := v.cfg.ResolveResultOutput
	if resolveOutput == nil {
		resolveOutput = func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(tx, prefix, evm.ParseContractPkScript, v.cfg.ResolveRecipient)
		}
	}
	module, err := contractframework.NewSettlementModule(contractframework.SettlementModuleConfig{
		Descriptor: evmModuleDescriptor(), ParentRoot: parent.State.StateRoot(), GasConfig: gas,
		ResolveScript: v.cfg.ResolveResultScript, ResolveOutput: resolveOutput, CountRecords: true, UseRecordStatus: true,
		Execute: func(work contractframework.WorkExecutionRequest) (contractframework.BackendBlockExecutionResult, any, error) {
			// Execution clones mutable state and only replaces this wrapper on success.
			candidate := *parent
			candidate.ContractPrefix = prefix
			// Dependencies are required lazily by actual Result construction or
			// verification. A due but gas-starved trigger is a valid pending
			// condition and must not require UTXO/script resolvers it never uses.
			resolveCaller := v.cfg.ResolveCaller
			if resolveCaller == nil {
				resolveCaller = evm.LastInputPreviousOutputCallerResolver(v.cfg.ChainParams, previousOutputScriptResolver(view))
			}
			exec, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
				Txs: work.Txs, Runtime: &candidate, ContractPrefix: prefix, GasConfig: gas, Block: ctx,
				ResolveCaller:             resolveCaller,
				ResolveGasRefundRecipient: evm.LastInputPreviousOutputGasRefundRecipientResolver(v.cfg.ChainParams, previousOutputScriptResolver(view)),
				ResolveResultScript:       v.cfg.ResolveResultScript, ResolveTriggers: v.cfg.ResolveTriggers,
				ContractUTXOs: v.cfg.ContractUTXOs, AssetPrecision: v.cfg.AssetPrecision,
			})
			return exec, candidate.State, err
		},
	})
	if err != nil {
		return nil, err
	}
	if v.cfg.VerifyResult != nil {
		// Explicit embedding/test hook retained from the existing config.
		// NewServices never installs it; production always uses the common
		// canonical byte-for-byte verifier from NewSettlementModule.
		module.VerifyResultTxsFunc = func(req contractframework.ResultVerifyRequest, exec contractframework.ExecutionResult) error {
			return evm.VerifyResultTxs(evm.ResultVerifyRequest{
				ResultTxs: req.ResultTxs, ContractPrefix: prefix, VerifyResult: v.cfg.VerifyResult,
				Execution: evm.BlockExecutionResult{Records: exec.Records, PendingRecords: exec.PendingRecords, ResultPlans: exec.ResultPlans, StateRoot: exec.StateRoot},
			})
		}
	}
	return module, nil
}

func (v *EVMBlockExecutionValidator) RecordContractBlockState(block *btcutil.Block, exec contractframework.ExecutionResult) error {
	state, ok := exec.PostState.(*evm.MemoryStateDB)
	if block == nil || !ok || state == nil || exec.ModuleType != contractframework.ModuleEVM {
		return evmBlockRuleError("invalid EVM post-state snapshot")
	}
	if state.StateRoot() != exec.StateRoot {
		return evmBlockRuleError("EVM post-state root changed after validation")
	}
	v.rememberPostState(block.Hash(), state)
	return nil
}

func (v *EVMBlockExecutionValidator) HasContractBlockActivity(block *btcutil.Block, view *blockchain.UtxoViewpoint) (bool, error) {
	if block == nil {
		return false, evmBlockRuleError("missing block")
	}
	runtime, err := v.runtime(block, view)
	if err != nil {
		return false, evmBlockRuleError("load EVM runtime: %v", err)
	}
	if runtime == nil {
		return false, evmBlockRuleError("missing EVM runtime")
	}
	runtime.ContractPrefix = v.contractPrefix()
	ctx, err := v.blockContext(block)
	if err != nil {
		return false, err
	}
	return v.runtimeHasDueTriggers(runtime, ctx, v.contractPrefix())
}

func (v *EVMBlockExecutionValidator) runtimeHasDueTriggers(runtime *evm.Runtime, block evm.BlockContext, prefix string) (bool, error) {
	if len(runtime.DueTriggerCalls(block)) != 0 {
		return true, nil
	}
	if v.cfg.ResolveTriggers == nil {
		return false, nil
	}
	triggers, err := v.cfg.ResolveTriggers(evm.TriggerResolutionContext{Block: block, Runtime: runtime, ContractPrefix: prefix})
	if err != nil {
		return false, evmBlockRuleError("resolve EVM triggers: %v", err)
	}
	return len(triggers) != 0, nil
}

func (v *EVMBlockExecutionValidator) blockContractOverlay(txs []*wire.MsgTx,
	prefix string, height int32) (*contractframework.ContractUTXOOverlay, error) {

	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix: prefix, ContractType: evm.ContractTypeEVM, Base: contractframework.WithoutBlockOutputs(v.cfg.ContractUTXOs, txs),
	})
	for _, tx := range txs {
		if err := overlay.ApplyTx(tx, int64(height)); err != nil {
			return nil, evmBlockRuleError("build EVM UTXO overlay: %v", err)
		}
	}
	return overlay, nil
}

func (v *EVMBlockExecutionValidator) EVMBlockPostState(hash *chainhash.Hash) (*evm.MemoryStateDB, bool) {
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

func (v *EVMBlockExecutionValidator) BlockPostState(hash *chainhash.Hash) (contractframework.EngineState, bool) {
	state, ok := v.EVMBlockPostState(hash)
	if !ok || state == nil {
		return nil, false
	}
	return contractframework.RootEngineState{StateRoot: state.StateRoot(), StateSnapshot: state}, true
}

func (v *EVMBlockExecutionValidator) ParentState(block *btcutil.Block, view *blockchain.UtxoViewpoint) (contractframework.EngineState, bool, error) {
	runtime, err := v.runtime(block, view)
	if err != nil {
		return nil, false, err
	}
	if runtime == nil || runtime.State == nil {
		return nil, false, nil
	}
	return contractframework.RootEngineState{StateRoot: runtime.State.StateRoot(), StateSnapshot: runtime.State.Clone()}, true, nil
}

func (v *EVMBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *evm.MemoryStateDB) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *EVMBlockExecutionValidator) ReleaseBlockPostState(hash *chainhash.Hash) {
	if hash == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	delete(v.postStates, *hash)
}

func (v *EVMBlockExecutionValidator) runtime(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*evm.Runtime, error) {
	if block != nil {
		prev := block.MsgBlock().Header.PrevBlock
		if state, ok := v.EVMBlockPostState(&prev); ok {
			runtime := evm.NewRuntime(nil)
			if v.cfg.NewRuntime != nil {
				loaded, err := v.cfg.NewRuntime(block, view)
				if err != nil {
					return nil, err
				}
				if loaded != nil {
					runtime = loaded.Clone()
				}
			}
			runtime.State = state
			return runtime, nil
		}
	}
	if v.cfg.NewRuntime != nil {
		return v.cfg.NewRuntime(block, view)
	}
	return evm.NewRuntime(nil), nil
}

func (v *EVMBlockExecutionValidator) blockContext(block *btcutil.Block) (evm.BlockContext, error) {
	if v.cfg.BuildBlockContext != nil {
		return v.contextWithHistory(v.cfg.BuildBlockContext(block))
	}
	height := block.Height()
	if height < 0 {
		height = 0
	}
	gas := v.cfg.GasConfig.Normalize()
	return v.contextWithHistory(evm.BlockContext{
		ChainID: evmChainID(v.cfg.ChainParams), Number: uint64(height), Time: uint64(block.MsgBlock().Header.Timestamp.Unix()),
		GasLimit: gas.MaxGasPerBlock, FixedGasPrice: gas.FixedGasPrice, ParentHash: [32]byte(block.MsgBlock().Header.PrevBlock),
	})
}

func (v *EVMBlockExecutionValidator) resultVerifier(prefix string,
	overlay *contractframework.ContractUTXOOverlay, height int32, gasConfig evm.GasConfig) evm.ResultVerifier {

	if v.cfg.VerifyResult != nil {
		return v.cfg.VerifyResult
	}
	resolve := v.cfg.ResolveResultOutput
	if resolve == nil {
		resolve = func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(tx, prefix, evm.ParseContractPkScript, v.cfg.ResolveRecipient)
		}
	}
	verifier := evm.CanonicalResultVerifier{
		GasConfig: gasConfig, UTXOs: v.cfg.ContractUTXOs, Precision: evm.SettlementPrecision(v.cfg.AssetPrecision),
		ResolveOutput: resolve, ResolveScript: v.cfg.ResolveResultScript,
	}
	if overlay != nil {
		verifier.UTXOs = overlay.Provider
	}
	return func(tx *wire.MsgTx, records []evm.ExecutionRecord) error {
		if v.cfg.ContractUTXOs == nil || v.cfg.AssetPrecision == nil {
			return fmt.Errorf("missing EVM contract UTXO or asset precision resolver")
		}
		if err := verifier.Verify(tx, records); err != nil {
			return err
		}
		if overlay != nil {
			return overlay.ApplyTx(tx, int64(height))
		}
		return nil
	}
}

func (v *EVMBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	return contractPrefixForParams(v.cfg.ChainParams)
}

func evmBlockRuleError(format string, args ...interface{}) error {
	return contractBlockRuleError(format, args...)
}

func (v *EVMBlockExecutionValidator) contextWithHistory(ctx evm.BlockContext) (evm.BlockContext, error) {
	if v.cfg.HistoryDB == nil {
		return ctx, nil
	}
	return evmContextWithHistory(v.cfg.HistoryDB, ctx)
}

func evmContextWithHistory(db database.DB, ctx evm.BlockContext) (evm.BlockContext, error) {
	hashes, err := recentEVMBlockHashes(db, chainhash.Hash(ctx.ParentHash), ctx.Number)
	if err != nil {
		return evm.BlockContext{}, err
	}
	ctx.BlockHashes = hashes
	return ctx, nil
}
