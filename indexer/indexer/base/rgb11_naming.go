package base

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
)

// ConfigureRGB11NamingSource is startup-only and node-internal. A future
// validated STP/contract adapter supplies deterministic effects, not arbitrary
// requests. The source must not call back into this BaseIndexer or fetch latest
// L1 state: it runs under the compiling snapshot lock during block replay.
func (b *BaseIndexer) ConfigureRGB11NamingSource(source rgb11names.EventSource) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.rgb11NamingStarted {
		return fmt.Errorf("RGB11 naming source cannot change after block processing starts")
	}
	b.rgb11NamingSource = source
	return nil
}

func (b *BaseIndexer) initRGB11NamingIndex() {
	index, err := rgb11names.Open(b.db, b.chaincfgParam, rgb11names.Cursor{Height: b.lastHeight, Hash: b.lastHash})
	if err != nil {
		common.Log.Panicf("open RGB11 naming index failed: %v", err)
	}
	b.rgb11Names = index
	b.rgb11NamingStarted = false
}

// Called under b.mutex BEFORE any base-index mutation for the new block.
func (b *BaseIndexer) applyRGB11NamingBlockLocked(block *common.Block) error {
	if b.rgb11Names == nil {
		if b.rgb11NamingSource != nil {
			return rgb11names.ErrUnavailable
		}
		return nil
	}
	var events []rgb11names.Event
	if b.rgb11NamingSource != nil {
		var err error
		events, err = b.rgb11NamingSource.RGB11NamingEvents(block)
		if err != nil {
			return err
		}
	} else if b.rgb11Names.HasEffects() {
		// Do not silently continue with stale DID ownership once this database
		// contains naming effects but its validating adapter is missing.
		return rgb11names.ErrUnavailable
	}
	if err := b.rgb11Names.ApplyBlock(block, events); err != nil {
		return err
	}
	b.rgb11NamingStarted = true
	return nil
}

func (b *BaseIndexer) GetRGB11Naming(query rgb11names.Query) (*rgb11names.Result, error) {
	if b == nil {
		return nil, rgb11names.ErrUnavailable
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	result, err := b.rgb11Names.Lookup(query)
	if err == nil {
		result.SourceConfigured = b.rgb11NamingSource != nil
	}
	return result, err
}
