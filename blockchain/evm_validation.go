package blockchain

import (
	"fmt"
	"math"
	"sync"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/wire"
)

// EVMRuntimeFactory returns the EVM runtime that should be used to replay a
// block. Production callers are expected to back the runtime StateDB with the
// persisted EVM state at the parent block.
type EVMRuntimeFactory func(block *btcutil.Block, view *UtxoViewpoint) (*evm.Runtime, error)

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
	ResolveRecipient    evm.ScriptRecipientResolver
	ResolveResultOutput evm.ResultOutputResolver
	ResolveTriggers     evm.TriggerResolver
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

func (v *EVMBlockExecutionValidator) ValidateEVMBlock(block *btcutil.Block, view *UtxoViewpoint) error {
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
	hasRoot, hasExecution, needsCaller, err := v.scanEVMWork(block, prefix)
	if err != nil {
		return err
	}
	if !hasRoot && !hasExecution {
		return nil
	}
	if hasExecution && !hasRoot {
		return evmBlockRuleError("missing EVM state root commitment")
	}
	if needsCaller && v.cfg.ResolveCaller == nil {
		return evmBlockRuleError("missing EVM caller resolver")
	}

	runtime, err := v.runtime(block, view)
	if err != nil {
		return evmBlockRuleError("load EVM runtime: %v", err)
	}
	if runtime == nil {
		return evmBlockRuleError("missing EVM runtime")
	}
	runtime.ContractPrefix = prefix

	blockTxs := make([]*wire.MsgTx, 0, len(txs)-1)
	for _, tx := range txs[1:] {
		blockTxs = append(blockTxs, tx.MsgTx())
	}
	contractOverlay, err := v.blockContractOverlay(blockTxs, prefix, block.Height())
	if err != nil {
		return err
	}

	req := evm.BlockExecutionRequest{
		Txs:             blockTxs,
		CoinbaseTx:      coinbaseTx,
		Runtime:         runtime,
		ContractPrefix:  prefix,
		GasConfig:       v.cfg.GasConfig,
		Block:           v.blockContext(block),
		ResolveCaller:   v.cfg.ResolveCaller,
		VerifyResult:    v.resultVerifier(prefix, contractOverlay, block.Height()),
		ResolveTriggers: v.cfg.ResolveTriggers,
	}
	var result evm.BlockExecutionResult
	if hasRoot {
		result, err = evm.ExecuteBlockAndVerifyStateRoot(req)
	} else {
		result, err = evm.ExecuteBlock(req)
	}
	if err != nil {
		return evmBlockRuleError("validate EVM block: %v", err)
	}
	v.rememberPostState(block.Hash(), runtime.State.Clone())
	if runtime.State.StateRoot() != result.StateRoot {
		return evmBlockRuleError("post-state root changed after validation")
	}
	return nil
}

func (v *EVMBlockExecutionValidator) blockContractOverlay(
	txs []*wire.MsgTx, prefix string, height int32) (*evm.ContractUTXOOverlay, error) {

	overlay := evm.NewContractUTXOOverlay(prefix, v.cfg.ContractUTXOs)
	for _, tx := range txs {
		parsed, err := evm.ParseTx(tx, evm.StandardContractScriptResolver(prefix))
		if err != nil {
			return nil, evmBlockRuleError("parse EVM tx for UTXO overlay: %v", err)
		}
		if parsed.Type == evm.TxTypeResult {
			continue
		}
		if err := overlay.AddTxOutputs(tx, int64(height)); err != nil {
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

func (v *EVMBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *evm.MemoryStateDB) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *EVMBlockExecutionValidator) scanEVMWork(block *btcutil.Block, prefix string) (bool, bool, bool, error) {
	txs := block.Transactions()
	_, hasRoot, err := evm.FindCoinbaseStateRoot(txs[0].MsgTx())
	if err != nil {
		return false, false, false, evmBlockRuleError("invalid EVM state root: %v", err)
	}

	hasExecution := false
	needsCaller := false
	for i, tx := range txs[1:] {
		info, err := evm.ClassifyTxForBlockOrder(tx.MsgTx(), prefix)
		if err != nil {
			return false, false, false, evmBlockRuleError(
				"malformed EVM transaction %v at index %d: %v",
				tx.Hash(), i+1, err)
		}
		if info.IsEVM && info.Type != evm.TxTypeCoinbaseStateRoot {
			hasExecution = true
			if info.Type == evm.TxTypeDeploy || info.Type == evm.TxTypeInvoke {
				needsCaller = true
			}
		}
	}
	return hasRoot, hasExecution, needsCaller, nil
}

func (v *EVMBlockExecutionValidator) runtime(block *btcutil.Block, view *UtxoViewpoint) (*evm.Runtime, error) {
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
		gasLimit = math.MaxUint64
	}
	return evm.BlockContext{
		Number:        uint64(height),
		Time:          uint64(block.MsgBlock().Header.Timestamp.Unix()),
		GasLimit:      gasLimit,
		FixedGasPrice: v.cfg.GasConfig.FixedGasPrice,
	}
}

func (v *EVMBlockExecutionValidator) resultVerifier(prefix string,
	overlay *evm.ContractUTXOOverlay, height int32) evm.ResultVerifier {

	if v.cfg.VerifyResult != nil {
		return v.cfg.VerifyResult
	}
	if v.cfg.ContractUTXOs == nil {
		return nil
	}
	resolveOutput := v.cfg.ResolveResultOutput
	if resolveOutput == nil {
		resolveOutput = func(resultTx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return evm.ResultOutputsFromTx(resultTx, prefix, v.cfg.ResolveRecipient)
		}
	}
	verifier := evm.CanonicalResultVerifier{
		GasConfig:     v.cfg.GasConfig,
		UTXOs:         v.cfg.ContractUTXOs,
		ResolveOutput: resolveOutput,
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
	return ruleError(ErrInvalidEVMBlock, fmt.Sprintf(format, args...))
}
