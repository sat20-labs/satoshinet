package indexer

import (
	"context"

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

func (b *IndexerMgr) PutDKVSRecordWithHash(record *wire.DKVSRecord) (bool, chainhash.Hash, error) {
	return b.dkvsIndexer.PutLocalWithHash(record)
}

func (b *IndexerMgr) PutDKVSRecordCAS(record *wire.DKVSRecord, precondition dkvs_indexer.WritePrecondition) (bool, error) {
	return b.dkvsIndexer.PutLocalCAS(record, precondition)
}

func (b *IndexerMgr) PutDKVSRecordBatchCAS(mutations []dkvs_indexer.CASMutation) (int, error) {
	return b.dkvsIndexer.PutLocalBatchCAS(mutations)
}

func (b *IndexerMgr) PutDKVSRecordBatchCASWithOptions(mutations []dkvs_indexer.CASMutation,
	options dkvs_indexer.BatchCASOptions) (int, error) {

	return b.dkvsIndexer.PutLocalBatchCASWithOptions(mutations, options)
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

func (b *IndexerMgr) GetDKVSRecordByHashForRelay(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetByHashForRelay(hash)
}

func (b *IndexerMgr) ListDKVSRecords(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	return b.dkvsIndexer.ListPrefix(prefix, start, limit)
}

func (b *IndexerMgr) GetDKVSUsage(prefix string) (*dkvs_indexer.Usage, error) {
	return b.dkvsIndexer.Usage(prefix)
}

func (b *IndexerMgr) GetDKVSFreeLocalCachePolicy() dkvs_indexer.FreeLocalCachePolicy {
	return b.dkvsIndexer.FreeLocalCachePolicy()
}

func (b *IndexerMgr) GetDKVSClientConfig() dkvs_indexer.ClientConfig {
	return b.dkvsIndexer.ClientConfig()
}

func (b *IndexerMgr) GetDKVSPathMeta(path string) (*dkvs_indexer.PathMeta, error) {
	return b.dkvsIndexer.GetPathMeta(path)
}

func (b *IndexerMgr) ApplyDKVSMirror(filters []dkvs_indexer.Subscription, records []*wire.DKVSRecord, root chainhash.Hash) (int, error) {
	return b.dkvsIndexer.ApplyMirror(filters, records, root)
}

func (b *IndexerMgr) SyncDKVSRecords(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.Sync(cursor, limit)
}

func (b *IndexerMgr) SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs_indexer.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.SyncFiltered(cursor, limit, filters)
}

func (b *IndexerMgr) SyncDKVSDirectory(prefix string, cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.SyncDirectory(prefix, cursor, limit)
}

func (b *IndexerMgr) WaitDKVSDirectory(ctx context.Context, prefix string, root chainhash.Hash) (chainhash.Hash, bool, error) {
	return b.dkvsIndexer.WaitDirectory(ctx, prefix, root)
}

func (b *IndexerMgr) SyncFilteredDKVSRecordsForClient(cursor []byte, limit uint32,
	filters []dkvs_indexer.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {

	return b.dkvsIndexer.SyncFilteredForClient(cursor, limit, filters)
}

func (b *IndexerMgr) WaitFilteredDKVSRecordsForClient(ctx context.Context, filters []dkvs_indexer.Subscription,
	root chainhash.Hash) (chainhash.Hash, bool, error) {

	return b.dkvsIndexer.WaitFilteredForClient(ctx, filters, root)
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
