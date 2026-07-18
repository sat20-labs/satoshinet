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

func (b *IndexerMgr) SetDKVSSubscriptionCallback(fn dkvs_indexer.SubscriptionNotifyFunc) {
	if b.dkvsIndexer != nil {
		b.dkvsIndexer.SetSubscriptionNotify(fn)
	}
}

func (b *IndexerMgr) SetDKVSResolver(resolver dkvs_indexer.DIDResolver) {
	if b.dkvsIndexer != nil {
		b.dkvsIndexer.SetResolver(resolver)
	}
}

func (b *IndexerMgr) SetDKVSFeeVerifier(verifier dkvs_indexer.FeeVerifier) {
	if b.dkvsIndexer != nil {
		b.dkvsIndexer.SetFeeVerifier(verifier)
	}
}

func (b *IndexerMgr) SetDKVSSystemVerifier(verifier dkvs_indexer.SystemVerifier) {
	if b.dkvsIndexer != nil {
		b.dkvsIndexer.SetSystemVerifier(verifier)
	}
}

func (b *IndexerMgr) PutDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	return b.dkvsIndexer.PutLocal(record)
}

func (b *IndexerMgr) PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	return b.dkvsIndexer.PutRemote(record)
}

func (b *IndexerMgr) NotifyDKVSNameTransfers(names []string) error {
	return b.dkvsIndexer.NotifyNameTransfers(names)
}

func (b *IndexerMgr) GetDKVSRecord(key string) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.Get(key)
}

func (b *IndexerMgr) GetDKVSRecordForRelay(key string) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetForRelay(key)
}

func (b *IndexerMgr) GetDKVSRecordByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetByHash(hash)
}

func (b *IndexerMgr) ListDKVSRecords(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	return b.dkvsIndexer.ListPrefix(prefix, start, limit)
}

func (b *IndexerMgr) GetDKVSUsage(prefix string) (*dkvs_indexer.Usage, error) {
	return b.dkvsIndexer.Usage(prefix)
}

func (b *IndexerMgr) GetDKVSPathMeta(path string) (*dkvs_indexer.PathMeta, error) {
	return b.dkvsIndexer.GetPathMeta(path)
}

func (b *IndexerMgr) DeleteDKVSMirrorKeys(keys []string) (int, error) {
	return b.dkvsIndexer.DeleteMirrorKeys(keys)
}

func (b *IndexerMgr) SyncDKVSRecords(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.Sync(cursor, limit)
}

func (b *IndexerMgr) SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs_indexer.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.SyncFiltered(cursor, limit, filters)
}

func (b *IndexerMgr) ListActiveDKVSKeys(filters []dkvs_indexer.Subscription) ([]string, error) {
	return b.dkvsIndexer.ListActiveKeys(filters)
}

func (b *IndexerMgr) GetDKVSCheckpoint() (*dkvs_indexer.Checkpoint, error) {
	return b.dkvsIndexer.Checkpoint()
}

func (b *IndexerMgr) GetDKVSSnapshot() (*dkvs_indexer.Snapshot, error) {
	return b.dkvsIndexer.Snapshot()
}

func (b *IndexerMgr) ApplyDKVSSnapshot(snapshot *dkvs_indexer.Snapshot) (int, error) {
	return b.dkvsIndexer.ApplySnapshot(snapshot)
}

func (b *IndexerMgr) PruneExpiredDKVSRecords() (int, error) {
	return b.dkvsIndexer.PruneExpired()
}

func (b *IndexerMgr) SubscribeDKVS(sub dkvs_indexer.Subscription) ([]*wire.DKVSRecord, int, error) {
	return b.dkvsIndexer.Subscribe(sub)
}

func (b *IndexerMgr) UnsubscribeDKVS(sub dkvs_indexer.Subscription) error {
	return b.dkvsIndexer.Unsubscribe(sub)
}

func (b *IndexerMgr) ListDKVSSubscriptions() []dkvs_indexer.Subscription {
	return b.dkvsIndexer.Subscriptions()
}

func (b *IndexerMgr) IsDKVSSubscribed(key string) bool {
	return b.dkvsIndexer.IsSubscribed(key)
}
