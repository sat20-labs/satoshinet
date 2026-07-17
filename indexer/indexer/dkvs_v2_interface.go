package indexer

import (
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func (b *IndexerMgr) GetDKVSRecordForSync(key string) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetForSync(key)
}

func (b *IndexerMgr) GetDKVSRecordByHashForSync(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetForSyncByHash(hash)
}

func (b *IndexerMgr) BeginDKVSPathSync(token string, sub dkvsindexer.Subscription) error {
	return b.dkvsIndexer.BeginPathSync(token, sub)
}

func (b *IndexerMgr) EndDKVSPathSync(token string) ([]*wire.DKVSRecord, bool) {
	return b.dkvsIndexer.EndPathSync(token)
}

func (b *IndexerMgr) CancelDKVSPathSync(token string) {
	b.dkvsIndexer.CancelPathSync(token)
}

func (b *IndexerMgr) ListDKVSKeysForSync(sub dkvsindexer.Subscription) ([]string, error) {
	return b.dkvsIndexer.ListActiveKeysForSync(sub)
}

func (b *IndexerMgr) DeleteDKVSKeysForMirror(sub dkvsindexer.Subscription, keys []string) (int, error) {
	return b.dkvsIndexer.DeleteMirrorKeys(sub, keys)
}

func (b *IndexerMgr) GetDKVSPathMeta(path string) (*dkvsindexer.PathMeta, error) {
	return b.dkvsIndexer.PathMeta(path)
}
