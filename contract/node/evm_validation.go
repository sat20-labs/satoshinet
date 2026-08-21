package node

import (
	"fmt"
	"math"
	"sync"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

// EVMRuntimeFactory returns the EVM runtime that should be used to replay a
// block. Production callers are expected to back the runtime StateDB with the
// persisted EVM state at the parent block.
type EVMRuntimeFactory func(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*evm.Runtime, error)

// EVMBlockContextBuilder maps a SatoshiNet block to the EVM execution context.
type EVMBlockContextBuilder func(block *btcutil.Block) evm.BlockContext

// EVMBlockExecutionConfig wires the generic EVM executor into blockchain block
// validation without hard-coding wallet, address-index, or state-store choices.
type EVMBlockExecutionConfig struct {
	ChainParams *chaincfg.Params

	ContractPrefix string
	GasConfig      evm.GasConfig

	NewRuntime        EVMRuntimeFactory
	BuildBlockContext EVMBlockContextBuilder
	ResolveCaller     evm.CallerResolver

	ContractUTXOs       evm.ContractUTXOProvider
	ResolveRecipient    contractframework.ScriptRecipientResolver
	ResolveResultOutput evm.ResultOutputResolver
	ResolveResultScript evm.ResultRecipientScriptResolver
	ResolveTriggers     evm.TriggerResolver
	AssetPrecision      contractframework.AssetPrecisionResolver
	VerifyResult        evm.ResultVerifier
}

// EVMBlockExecutionValidator validates EVM transaction replay, Result TX
// settlement, and the coinbase state-root commitment for a block.
type EVMBlockExecutionValidator struct {
	cfg EVMBlockExecutionConfig

	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*evm.MemoryStateDB
}

func NewEVMBlockExecutionValidator(cfg EVMBlockExecutionConfig) *EVMBlockExecutionValidator {
	return &EVMBlockExecutionValidator{
		cfg:        cfg,
		postStates: make(map[chainhash.Hash]*evm.MemoryStateDB),
	}
}

func (v *EVMBlockExecutionValidator) ValidateEVMBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	if block == nil {
		return evmBlockRuleError("missing block")
	}
	if view == nil {
		return evmBlockRuleError("missing UTXO view")
	}
	txs := block.Transactions()
	if len(txs) == 0 {
		return evmBlockRuleError("missing coinbase transaction")
	}

	coinbaseTx := txs[0].MsgTx()
	prefix := v.contractPrefix()
	hasRoot, split, err := splitBlockContractTxs(block, view, v.cfg.ChainParams, prefix)
	if err != nil {
		return evmBlockRuleError("split EVM contract txs: %v", err)
	}
	blockTxs := split.WorkTxs[contractframework.ModuleEVM]
	resultTxs := split.ResultTxs[contractframework.ModuleEVM]
	hasExecution := len(blockTxs) != 0 || len(resultTxs) != 0
	runtime, err := v.runtime(block, view)
	if err != nil {
		return evmBlockRuleError("load EVM runtime: %v", err)
	}
	if runtime == nil {
		return evmBlockRuleError("missing EVM runtime")
	}
	runtime.ContractPrefix = prefix
	blockCtx := v.blockContext(block)
	hasDueTrigger, err := v.runtimeHasDueTriggers(runtime, blockCtx, prefix)
	if err != nil {
		return err
	}
	if !hasRoot && !hasExecution && !hasDueTrigger {
		return nil
	}
	if (hasExecution || hasDueTrigger) && !hasRoot {
		return evmBlockRuleError("missing EVM state root commitment")
	}
	gasConfig := v.cfg.GasConfig
	gasConfig.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)

	contractOverlay, err := v.blockContractOverlay(blockTxs, prefix, block.Height())
	if err != nil {
		return err
	}
	verifyResult := v.resultVerifier(prefix, contractOverlay, block.Height(), gasConfig)

	req := evm.BlockExecutionRequest{
		Txs:            blockTxs,
		CoinbaseTx:     coinbaseTx,
		Runtime:        runtime,
		ContractPrefix: prefix,
		GasConfig:      gasConfig,
		Block:          blockCtx,
		ResolveCaller: evm.LastInputPreviousOutputCallerResolver(
			v.cfg.ChainParams, previousOutputScriptResolver(view)),
		ResolveGasRefundRecipient: evm.LastInputPreviousOutputGasRefundRecipientResolver(
			v.cfg.ChainParams, previousOutputScriptResolver(view)),
		ResolveResultScript: v.cfg.ResolveResultScript,
		VerifyResult:        verifyResult,
		ResolveTriggers:     v.cfg.ResolveTriggers,
		ContractUTXOs:       v.cfg.ContractUTXOs,
		AssetPrecision:      v.cfg.AssetPrecision,
	}
	result, err := evm.ExecuteBlock(req)
	if err != nil {
		return evmBlockRuleError("validate EVM block: %v", err)
	}
	if err := evm.VerifyResultTxs(evm.ResultVerifyRequest{
		ResultTxs:      resultTxs,
		Execution:      result,
		ContractPrefix: prefix,
		VerifyResult:   verifyResult,
	}); err != nil {
		return evmBlockRuleError("validate EVM result: %v", err)
	}
	v.rememberPostState(block.Hash(), runtime.State.Clone())
	if runtime.State.StateRoot() != result.StateRoot {
		return evmBlockRuleError("post-state root changed after validation")
	}
	return nil
}

func (v *EVMBlockExecutionValidator) ValidateContractModuleBlock(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) error {

	return v.ValidateEVMBlock(block, view)
}

func (v *EVMBlockExecutionValidator) HasContractBlockActivity(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (bool, error) {

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
	prefix := v.contractPrefix()
	runtime.ContractPrefix = prefix
	return v.runtimeHasDueTriggers(runtime, v.blockContext(block), prefix)
}

func (v *EVMBlockExecutionValidator) runtimeHasDueTriggers(runtime *evm.Runtime,
	block evm.BlockContext, prefix string) (bool, error) {

	if len(runtime.DueTriggerCalls(block)) != 0 {
		return true, nil
	}
	if v.cfg.ResolveTriggers == nil {
		return false, nil
	}
	triggers, err := v.cfg.ResolveTriggers(evm.TriggerResolutionContext{
		Block:          block,
		Runtime:        runtime,
		ContractPrefix: prefix,
	})
	if err != nil {
		return false, evmBlockRuleError("resolve EVM triggers: %v", err)
	}
	return len(triggers) != 0, nil
}

func (v *EVMBlockExecutionValidator) blockContractOverlay(
	txs []*wire.MsgTx, prefix string, height int32) (*contractframework.ContractUTXOOverlay, error) {

	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix:       prefix,
		ContractType: evm.ContractTypeEVM,
		Base:         v.cfg.ContractUTXOs,
	})
	for _, tx := range txs {
		parsed, err := evm.ParseTx(tx, evm.StandardContractScriptResolver(prefix))
		if err != nil {
			return nil, evmBlockRuleError("parse EVM tx for UTXO overlay: %v", err)
		}
		if parsed.Type == evm.TxTypeResult {
			continue
		}
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

func (v *EVMBlockExecutionValidator) ParentState(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractframework.EngineState, bool, error) {

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
		prevHash := block.MsgBlock().Header.PrevBlock
		if state, ok := v.EVMBlockPostState(&prevHash); ok {
			var runtime *evm.Runtime
			var err error
			if v.cfg.NewRuntime != nil {
				runtime, err = v.cfg.NewRuntime(block, view)
				if err != nil {
					return nil, err
				}
			} else {
				runtime = evm.NewRuntime(nil)
			}
			if runtime == nil {
				runtime = evm.NewRuntime(nil)
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

func (v *EVMBlockExecutionValidator) blockContext(block *btcutil.Block) evm.BlockContext {
	if v.cfg.BuildBlockContext != nil {
		return v.cfg.BuildBlockContext(block)
	}
	height := block.Height()
	if height < 0 {
		height = 0
	}
	gasLimit := v.cfg.GasConfig.MaxGasPerBlock
	if gasLimit == 0 {
		gasLimit = math.MaxInt64
	}
	return evm.BlockContext{
		ChainID:       evmChainID(v.cfg.ChainParams),
		Number:        uint64(height),
		Time:          uint64(block.MsgBlock().Header.Timestamp.Unix()),
		GasLimit:      gasLimit,
		FixedGasPrice: v.cfg.GasConfig.FixedGasPrice,
		ParentHash:    [32]byte(block.MsgBlock().Header.PrevBlock),
	}
}

func (v *EVMBlockExecutionValidator) resultVerifier(prefix string,
	overlay *contractframework.ContractUTXOOverlay, height int32, gasConfig evm.GasConfig) evm.ResultVerifier {

	if v.cfg.VerifyResult != nil {
		return v.cfg.VerifyResult
	}
	if v.cfg.ContractUTXOs == nil {
		return func(*wire.MsgTx, []evm.ExecutionRecord) error {
			return fmt.Errorf("missing EVM contract UTXO provider")
		}
	}
	if v.cfg.AssetPrecision == nil {
		return func(*wire.MsgTx, []evm.ExecutionRecord) error {
			return fmt.Errorf("missing EVM asset precision resolver")
		}
	}
	resolveOutput := v.cfg.ResolveResultOutput
	if resolveOutput == nil {
		resolveOutput = func(resultTx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(resultTx, prefix,
				evm.ParseContractPkScript, v.cfg.ResolveRecipient)
		}
	}
	verifier := evm.CanonicalResultVerifier{
		GasConfig:     gasConfig,
		UTXOs:         v.cfg.ContractUTXOs,
		Precision:     evm.SettlementPrecision(v.cfg.AssetPrecision),
		ResolveOutput: resolveOutput,
		ResolveScript: v.cfg.ResolveResultScript,
	}
	if overlay != nil {
		verifier.UTXOs = overlay.Provider
	}
	return func(resultTx *wire.MsgTx, settled []evm.ExecutionRecord) error {
		if err := verifier.Verify(resultTx, settled); err != nil {
			return err
		}
		if overlay != nil {
			if err := overlay.ApplyTx(resultTx, int64(height)); err != nil {
				return err
			}
		}
		return nil
	}
}

func (v *EVMBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	if v.cfg.ChainParams != nil {
		return evm.ContractPrefixForNet(v.cfg.ChainParams.Net)
	}
	return evm.TestnetContractPrefix
}

func evmBlockRuleError(format string, args ...interface{}) error {
	return blockchain.RuleError{
		ErrorCode:   blockchain.ErrInvalidEVMBlock,
		Description: fmt.Sprintf(format, args...),
	}
}
