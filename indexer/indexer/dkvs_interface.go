package indexer

import (
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func (b *IndexerMgr) SetDKVSNotifyCallback(fn dkvs_indexer.NotifyFunc) {
	if b.dkvsIndexer != nil {
		b.dkvsIndexer.SetNotify(fn)
	}
}

func (b *IndexerMgr) PutDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	return b.dkvsIndexer.PutLocal(record)
}

func (b *IndexerMgr) PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	return b.dkvsIndexer.PutRemote(record)
}

func (b *IndexerMgr) GetDKVSRecord(key string) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.Get(key)
}

func (b *IndexerMgr) GetDKVSRecordByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetByHash(hash)
}

func (b *IndexerMgr) ListDKVSRecords(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	return b.dkvsIndexer.ListPrefix(prefix, start, limit)
}

func (b *IndexerMgr) SyncDKVSRecords(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.Sync(cursor, limit)
}

func (b *IndexerMgr) GetDKVSCheckpoint() (*dkvs_indexer.Checkpoint, error) {
	return b.dkvsIndexer.Checkpoint()
}
