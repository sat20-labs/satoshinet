package indexer

import "github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"

// GetRGB11Naming reads the published RPC snapshot, never a half-processed live
// block. The returned indexed_at height/hash identifies the exact view.
func (b *IndexerMgr) GetRGB11Naming(query rgb11names.Query) (*rgb11names.Result, error) {
	if b == nil { return nil, rgb11names.ErrUnavailable }
	b.mutex.RLock()
	view := b.rpcService
	b.mutex.RUnlock()
	if view == nil { return nil, rgb11names.ErrUnavailable }
	return view.GetRGB11Naming(query)
}
