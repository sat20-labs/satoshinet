package blockchain

import (
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

// ContractAssetIndexView is the branch-scoped asset state required by contract
// consensus validation. Implementations must be isolated from the live AIDX
// view: ConnectBlock is used only to replay an exact candidate branch.
type ContractAssetIndexView interface {
	GetInternalTip() (int, chainhash.Hash, bool)
	ConnectBlock(block *wire.MsgBlock, height, tip int) error
	GetInternalTickerInfo(ticker *wire.AssetName) *sindexercommon.TickerInfo
	GetInternalAssetUTXOsInAddress(address string) map[wire.AssetName][]*sindexercommon.TxOutput
}

// ContractAssetIndexViewPreparer is optionally implemented by the production
// contract validator. The prepared view is consumed by the immediately
// following validation of blockHash and must never leak into mining/RPC paths.
type ContractAssetIndexViewPreparer interface {
	PrepareContractAssetIndexView(blockHash *chainhash.Hash, view ContractAssetIndexView) error
}
