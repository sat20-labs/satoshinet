package indexer

import (
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	base_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"
	"github.com/sat20-labs/satoshinet/wire"
)

const internalTipPollInterval = 25 * time.Millisecond

// ValidationAssetView is an isolated in-memory BaseIndexer used only while
// validating a non-canonical branch. It shares the durable base DB read-only,
// then replays exact blocks supplied by blockchain without publishing or
// mutating the live compiling/rpcService state.
type ValidationAssetView struct {
	compiling *base_indexer.BaseIndexer
}

// NewValidationAssetView starts from the durable base snapshot. The caller is
// responsible for replaying the exact ancestor path required by validation.
func (b *IndexerMgr) NewValidationAssetView() (*ValidationAssetView, error) {
	if b == nil || b.baseDB == nil || b.chaincfgParam == nil {
		return nil, fmt.Errorf("asset validation view is unavailable")
	}
	compiling := base_indexer.NewBaseIndexer(
		b.baseDB, b.chaincfgParam, b.maxIndexHeight, b.periodFlushToDB,
	)
	// Validation only needs base asset state. Contract/DKVS side effects are
	// deliberately excluded from the branch replay.
	compiling.SetBlockCallback(func(*common.Block) {})
	compiling.Init()
	return &ValidationAssetView{compiling: compiling}, nil
}

func (v *ValidationAssetView) GetInternalTip() (int, chainhash.Hash, bool) {
	if v == nil || v.compiling == nil {
		return 0, chainhash.Hash{}, false
	}
	height, hashString := v.compiling.GetInternalTip()
	if height < 0 && hashString == "" {
		return height, chainhash.Hash{}, true
	}
	hash, err := chainhash.NewHashFromStr(hashString)
	if err != nil {
		return height, chainhash.Hash{}, false
	}
	return height, *hash, true
}

// runValidationAssetReplay turns a panic caused by untrusted candidate-branch
// data into a normal validation error. Canonical BaseIndexer processing keeps
// its existing fail-fast behaviour; only the isolated prevalidation view is
// allowed to recover because rejecting the candidate block is sufficient.
func runValidationAssetReplay(run func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("asset validation replay panic: %v", recovered)
		}
	}()
	return run()
}

// ConnectBlock advances only this isolated validation view. updateDB=false is
// intentional: prevalidation must never persist candidate-branch AIDX state.
func (v *ValidationAssetView) ConnectBlock(block *wire.MsgBlock, height, tip int) error {
	if v == nil || v.compiling == nil || block == nil {
		return fmt.Errorf("invalid asset validation block")
	}
	return runValidationAssetReplay(func() error {
		return v.compiling.SyncBlock(block, height, tip, false)
	})
}

func (v *ValidationAssetView) GetInternalTickerInfo(ticker *wire.AssetName) *common.TickerInfo {
	if v == nil || v.compiling == nil || ticker == nil {
		return nil
	}
	return v.compiling.GetInternalTickerInfo(ticker)
}

func (v *ValidationAssetView) GetInternalAssetUTXOsInAddress(address string) map[wire.AssetName][]*common.TxOutput {
	if v == nil || v.compiling == nil {
		return nil
	}
	utxos, err := v.compiling.GetUTXOs(address)
	if err != nil {
		return nil
	}
	result := make(map[wire.AssetName][]*common.TxOutput)
	for utxoID := range utxos {
		utxo, err := v.compiling.GetUtxoByID(utxoID)
		if err != nil {
			continue
		}
		info := v.getInternalTxOutputWithUtxo(utxo)
		if info == nil {
			continue
		}
		for _, asset := range info.OutValue.Assets {
			result[asset.Name] = append(result[asset.Name], info)
		}
		if len(info.OutValue.Assets) == 0 {
			result[common.ASSET_PLAIN_SAT] = append(result[common.ASSET_PLAIN_SAT], info)
		}
	}
	return result
}

func (v *ValidationAssetView) getInternalTxOutputWithUtxo(utxo string) *common.TxOutput {
	if v == nil || v.compiling == nil {
		return nil
	}
	info, err := v.compiling.GetUtxoInfo(utxo)
	if err != nil {
		return nil
	}
	return &common.TxOutput{
		UtxoId:      info.UtxoId,
		OutPointStr: utxo,
		OutValue: wire.TxOut{
			Value: info.Value, Assets: info.Assets, PkScript: info.PkScript,
		},
	}
}

// GetInternalSyncHeight returns the in-memory compiling height for internal
// node logic. External RPC queries should keep using GetSyncHeight.
func (b *IndexerMgr) GetInternalSyncHeight() int {
	if b == nil || b.compiling == nil {
		return 0
	}
	return b.compiling.GetHeight()
}

// GetInternalTip returns an atomic snapshot of the compiling index tip.
func (b *IndexerMgr) GetInternalTip() (int, chainhash.Hash, bool) {
	if b == nil || b.compiling == nil {
		return 0, chainhash.Hash{}, false
	}
	height, hashString := b.compiling.GetInternalTip()
	hash, err := chainhash.NewHashFromStr(hashString)
	if err != nil {
		return height, chainhash.Hash{}, false
	}
	return height, *hash, true
}

// InternalTipReady reports whether the compiling index is exactly at the
// requested canonical parent. Height alone is insufficient across reorgs.
func (b *IndexerMgr) InternalTipReady(height int, hash *chainhash.Hash) bool {
	if hash == nil {
		return false
	}
	currentHeight, currentHash, ok := b.GetInternalTip()
	return ok && currentHeight == height && currentHash == *hash
}

// EnsureInternalTip actively repairs the compiling index to the requested
// canonical tip. It serializes with block connect/disconnect so a failed block
// notification cannot strand consensus readiness behind a passive wait.
func (b *IndexerMgr) EnsureInternalTip(height int, hash *chainhash.Hash, tip int) error {
	if b == nil || b.compiling == nil || hash == nil {
		return fmt.Errorf("invalid internal tip target")
	}
	if b.interrupt != nil {
		select {
		case <-b.interrupt:
			return fmt.Errorf("internal tip repair canceled")
		default:
		}
	}
	b.connectMutex.Lock()
	defer b.connectMutex.Unlock()
	return ensureInternalTip(b.connectBlockOps(), height, hash, tip)
}

// WaitForInternalTip waits until the compiling index reaches the exact target
// or node shutdown cancels the wait.
func (b *IndexerMgr) WaitForInternalTip(height int, hash *chainhash.Hash,
	interrupt <-chan struct{}) error {

	if hash == nil {
		return fmt.Errorf("nil internal tip target")
	}
	ticker := time.NewTicker(internalTipPollInterval)
	defer ticker.Stop()
	for {
		if b.InternalTipReady(height, hash) {
			return nil
		}
		select {
		case <-interrupt:
			return fmt.Errorf("internal tip wait canceled")
		case <-ticker.C:
		}
	}
}

// GetInternalTickerInfo reads ticker metadata from the current compiling view.
// Consensus and block-building code must use this instead of the stable RPC
// snapshot, which can lag while a node is syncing.
func (b *IndexerMgr) GetInternalTickerInfo(ticker *wire.AssetName) *common.TickerInfo {
	if b == nil || b.compiling == nil || ticker == nil {
		return nil
	}
	return b.compiling.GetInternalTickerInfo(ticker)
}

// GetInternalAssetUTXOsInAddress returns the current compiling view for
// consensus and block-building code. External RPC queries should keep using
// GetAssetUTXOsInAddress, which reads the stable rpcService snapshot.
func (b *IndexerMgr) GetInternalAssetUTXOsInAddress(address string) map[wire.AssetName][]*common.TxOutput {
	if b == nil || b.compiling == nil {
		return nil
	}
	utxos, err := b.compiling.GetUTXOs(address)
	if err != nil {
		return nil
	}

	result := make(map[wire.AssetName][]*common.TxOutput)
	for utxoId := range utxos {
		utxo, err := b.compiling.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}
		info := b.GetInternalTxOutputWithUtxo(utxo)
		if info == nil {
			continue
		}
		for _, asset := range info.OutValue.Assets {
			result[asset.Name] = append(result[asset.Name], info)
		}
		if len(info.OutValue.Assets) == 0 {
			result[common.ASSET_PLAIN_SAT] = append(result[common.ASSET_PLAIN_SAT], info)
		}
	}
	return result
}

// GetInternalTxOutputWithUtxo returns a UTXO from the compiling view.
func (b *IndexerMgr) GetInternalTxOutputWithUtxo(utxo string) *common.TxOutput {
	if b == nil || b.compiling == nil {
		return nil
	}
	info, err := b.compiling.GetUtxoInfo(utxo)
	if err != nil {
		return nil
	}
	return &common.TxOutput{
		UtxoId:      info.UtxoId,
		OutPointStr: utxo,
		OutValue: wire.TxOut{
			Value:    info.Value,
			Assets:   info.Assets,
			PkScript: info.PkScript,
		},
	}
}
