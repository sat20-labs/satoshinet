package base

import (
	"github.com/sat20-labs/satoshinet/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
)

// SetRGB11DIDResolver wires the same L1 Ordinals DID resolver already used by
// DKVS. It is not an activation switch: the naming index is always active.
// A resolver is only required when a block actually contains a Primary DID
// bind operation.
func (b *BaseIndexer) SetRGB11DIDResolver(resolver dkvsindexer.DIDResolver) {
	b.mutex.Lock()
	b.rgb11DIDResolver = resolver
	b.mutex.Unlock()
}

func (b *BaseIndexer) initRGB11NamingIndex() {
	index, err := rgb11names.Open(b.db, b.chaincfgParam, rgb11names.Cursor{Height: b.lastHeight, Hash: b.lastHash})
	if err != nil {
		common.Log.Panicf("open RGB11 naming index failed: %v", err)
	}
	b.rgb11Names = index
}

// Called under b.mutex BEFORE any base-index mutation for the new block.
func (b *BaseIndexer) applyRGB11NamingBlockLocked(block *common.Block) error {
	if b.rgb11Names == nil {
		return rgb11names.ErrUnavailable
	}
	events, err := b.collectRGB11NamingEventsLocked(block)
	if err != nil {
		return err
	}
	return b.rgb11Names.ApplyBlock(block, events)
}

func (b *BaseIndexer) GetRGB11Naming(query rgb11names.Query) (*rgb11names.Result, error) {
	if b == nil {
		return nil, rgb11names.ErrUnavailable
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	if b.rgb11Names == nil {
		return nil, rgb11names.ErrUnavailable
	}
	return b.rgb11Names.Lookup(query)
}
