package indexer

import (
	"context"
	"errors"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

var errDKVSNotInitialized = errors.New("dkvs is not initialized")

func (p *IndexerMgr) SetDKVSNotifyCallback(fn dkvs.NotifyFunc) {
	if p.dkvsIndexer != nil {
		p.dkvsIndexer.SetNotify(fn)
	}
}

func (p *IndexerMgr) SetDKVSSubscriptionCallback(fn dkvs.SubscriptionNotifyFunc) {
	if p.dkvsIndexer != nil {
		p.dkvsIndexer.SetSubscriptionNotify(fn)
	}
}

func (p *IndexerMgr) SetDKVSResolver(resolver dkvs.DIDResolver) {
	if p.dkvsIndexer != nil {
		p.dkvsIndexer.SetResolver(resolver)
	}
}

func (p *IndexerMgr) SetDKVSFeeVerifier(verifier dkvs.FeeVerifier) {
	if p.dkvsIndexer != nil {
		p.dkvsIndexer.SetFeeVerifier(verifier)
	}
}

func (p *IndexerMgr) SetDKVSSystemVerifier(verifier dkvs.SystemVerifier) {
	if p.dkvsIndexer != nil {
		p.dkvsIndexer.SetSystemVerifier(verifier)
	}
}

func (p *IndexerMgr) SetDKVSEndpointID(endpointID string) error {
	if p.dkvsIndexer == nil {
		return errDKVSNotInitialized
	}
	return p.dkvsIndexer.SetEndpointID(endpointID)
}

func (p *IndexerMgr) PutDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	if p.dkvsIndexer == nil {
		return false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PutLocal(record)
}

// PutDKVSInternalMailbox is reserved for MessageManager after it has completed
// binding, signature, sequence and billing admission. Generic DKVS RPCs must
// never expose this method directly.
func (p *IndexerMgr) PutDKVSInternalMailbox(record *wire.DKVSRecord) (bool, error) {
	if p.dkvsIndexer == nil {
		return false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PutInternalMailbox(record)
}

func (p *IndexerMgr) PutDKVSRecordWithHash(record *wire.DKVSRecord) (bool, chainhash.Hash, error) {
	if p.dkvsIndexer == nil {
		return false, chainhash.Hash{}, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PutLocalWithHash(record)
}

func (p *IndexerMgr) PutDKVSRecordCAS(record *wire.DKVSRecord, precondition dkvs.WritePrecondition) (bool, error) {
	if p.dkvsIndexer == nil {
		return false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PutLocalCAS(record, precondition)
}

func (p *IndexerMgr) PutDKVSRecordCASResult(record *wire.DKVSRecord, precondition dkvs.WritePrecondition, options dkvs.BatchCASOptions) (*dkvs.WriteResult, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	mutations := []dkvs.CASMutation{{Record: record, Precondition: precondition}}
	if err := p.dkvsIndexer.ValidateBatchEndpointID(mutations, options.EndpointID); err != nil {
		return nil, err
	}
	return p.dkvsIndexer.PutLocalBatchCASResultWithOptions(mutations, options)
}

func (p *IndexerMgr) PutDKVSRecordBatchCAS(mutations []dkvs.CASMutation) (int, error) {
	if p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PutLocalBatchCAS(mutations)
}

func (p *IndexerMgr) PutDKVSRecordBatchCASWithOptions(mutations []dkvs.CASMutation, options dkvs.BatchCASOptions) (int, error) {
	if p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	if err := p.dkvsIndexer.ValidateBatchEndpointID(mutations, options.EndpointID); err != nil {
		return 0, err
	}
	return p.dkvsIndexer.PutLocalBatchCASWithOptions(mutations, options)
}

func (p *IndexerMgr) PutDKVSRecordBatchCASResultWithOptions(mutations []dkvs.CASMutation, options dkvs.BatchCASOptions) (*dkvs.WriteResult, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	if err := p.dkvsIndexer.ValidateBatchEndpointID(mutations, options.EndpointID); err != nil {
		return nil, err
	}
	return p.dkvsIndexer.PutLocalBatchCASResultWithOptions(mutations, options)
}

func (p *IndexerMgr) PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	if p.dkvsIndexer == nil {
		return false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.AcceptRemoteRecord(record, "")
}

func (p *IndexerMgr) NotifyDKVSNameTransfers(names []string) error {
	if p.dkvsIndexer == nil {
		return errDKVSNotInitialized
	}
	return p.dkvsIndexer.NotifyNameTransfers(names)
}

func (p *IndexerMgr) GetDKVSRecord(key string) (*wire.DKVSRecord, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.Get(key)
}

func (p *IndexerMgr) GetDKVSRecordForRelay(key string) (*wire.DKVSRecord, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	mode, err := dkvs.ReplicationModeForKey(key)
	if err != nil || mode == dkvs.ReplicationAccountBound {
		return nil, dkvs.ErrRecordNotFound
	}
	return p.dkvsIndexer.GetForRelay(key)
}

func (p *IndexerMgr) GetDKVSRecordByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.GetByHash(hash)
}

func (p *IndexerMgr) GetDKVSRecordByHashForRelay(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	record, err := p.dkvsIndexer.GetByHashForRelay(hash)
	if err != nil || record == nil {
		return record, err
	}
	mode, modeErr := dkvs.ReplicationModeForKey(record.Key)
	if modeErr != nil || mode == dkvs.ReplicationAccountBound {
		return nil, dkvs.ErrRecordNotFound
	}
	return record, nil
}

func (p *IndexerMgr) ListDKVSRecords(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	if p.dkvsIndexer == nil {
		return nil, 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.ListPrefix(prefix, start, limit)
}

func (p *IndexerMgr) SyncDKVSRecords(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if p.dkvsIndexer == nil {
		return nil, nil, false, chainhash.Hash{}, errDKVSNotInitialized
	}
	return p.dkvsIndexer.Sync(cursor, limit)
}

func (p *IndexerMgr) SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if p.dkvsIndexer == nil {
		return nil, nil, false, chainhash.Hash{}, errDKVSNotInitialized
	}
	return p.dkvsIndexer.SyncFiltered(cursor, limit, filters)
}

func (p *IndexerMgr) SyncFilteredDKVSRecordsForClient(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if p.dkvsIndexer == nil {
		return nil, nil, false, chainhash.Hash{}, errDKVSNotInitialized
	}
	return p.dkvsIndexer.SyncFilteredForClient(cursor, limit, filters)
}

func (p *IndexerMgr) SyncDKVSDirectory(prefix string, cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if p.dkvsIndexer == nil {
		return nil, nil, false, chainhash.Hash{}, errDKVSNotInitialized
	}
	return p.dkvsIndexer.SyncDirectory(prefix, cursor, limit)
}

func (p *IndexerMgr) GetDKVSPathSnapshot(path string) (*dkvs.PathSnapshot, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.GetPathSnapshot(path)
}

func (p *IndexerMgr) ApplyDKVSPathSnapshot(snapshot *dkvs.PathSnapshot) (int, error) {
	if p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.ApplyPathSnapshot(snapshot)
}

func (p *IndexerMgr) GetDKVSKeyState(key string) (dkvs.DKVSKeyState, error) {
	if p.dkvsIndexer == nil {
		return dkvs.DKVSKeyState{}, errDKVSNotInitialized
	}
	return p.dkvsIndexer.GetKeyState(key)
}

func (p *IndexerMgr) GetDKVSPrefixStatus(endpointID string,
	known []dkvs.PrefixGeneration) (*dkvs.PrefixStatusResult, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PrefixStatus(endpointID, known)
}

func (p *IndexerMgr) GetDKVSPrefixSnapshot(prefix string) (*dkvs.PrefixSnapshot, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PrefixSnapshot(prefix)
}

func (p *IndexerMgr) ReadDKVSPrefix(prefix string) (*dkvs.PrefixReadResult, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.ReadPrefix(prefix)
}

func (p *IndexerMgr) WaitFilteredDKVSRecords(ctx context.Context, filters []dkvs.Subscription, knownRoot chainhash.Hash) (chainhash.Hash, bool, error) {
	if p.dkvsIndexer == nil {
		return chainhash.Hash{}, false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.WaitFilteredForClient(ctx, filters, knownRoot)
}

func (p *IndexerMgr) WaitFilteredDKVSRecordsForClient(ctx context.Context, filters []dkvs.Subscription, knownRoot chainhash.Hash) (chainhash.Hash, bool, error) {
	return p.WaitFilteredDKVSRecords(ctx, filters, knownRoot)
}

func (p *IndexerMgr) WaitDKVSDirectory(ctx context.Context, prefix string, knownRoot chainhash.Hash) (chainhash.Hash, bool, error) {
	if p.dkvsIndexer == nil {
		return chainhash.Hash{}, false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.WaitDirectory(ctx, prefix, knownRoot)
}

func (p *IndexerMgr) GetDKVSUsage(prefix string) (*dkvs.Usage, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.Usage(prefix)
}

func (p *IndexerMgr) GetDKVSFreeLocalCachePolicy() dkvs.FreeLocalCachePolicy {
	if p.dkvsIndexer == nil {
		return dkvs.FreeLocalCachePolicy{}
	}
	return p.dkvsIndexer.ClientConfig().FreeLocal
}

func (p *IndexerMgr) GetDKVSClientConfig() dkvs.ClientConfig {
	if p.dkvsIndexer == nil {
		return dkvs.ClientConfig{}
	}
	return p.dkvsIndexer.ClientConfig()
}

func (p *IndexerMgr) GetDKVSPathMeta(path string) (*dkvs.PathMeta, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.GetPathMeta(path)
}

func (p *IndexerMgr) WaitDKVSPath(ctx context.Context, path string, generation uint64, root chainhash.Hash, viewHeight uint64) (*dkvs.PathMeta, bool, error) {
	if p.dkvsIndexer == nil {
		return nil, false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.WaitPath(ctx, path, generation, root, viewHeight)
}

func (p *IndexerMgr) GetDKVSCheckpoint() (*dkvs.Checkpoint, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.Checkpoint()
}

func (p *IndexerMgr) GetDKVSSnapshot() (*dkvs.Snapshot, error) {
	if p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.Snapshot()
}

func (p *IndexerMgr) ApplyDKVSSnapshot(snapshot *dkvs.Snapshot) (int, error) {
	if p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.ApplySnapshot(snapshot)
}

func (p *IndexerMgr) PruneExpiredDKVSRecords() (int, error) {
	if p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PruneExpired()
}

func (p *IndexerMgr) SubscribeDKVS(sub dkvs.Subscription) ([]*wire.DKVSRecord, int, error) {
	if p.dkvsIndexer == nil {
		return nil, 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.Subscribe(sub)
}

func (p *IndexerMgr) UnsubscribeDKVS(sub dkvs.Subscription) error {
	if p.dkvsIndexer == nil {
		return errDKVSNotInitialized
	}
	return p.dkvsIndexer.Unsubscribe(sub)
}

func (p *IndexerMgr) ListDKVSSubscriptions() []dkvs.Subscription {
	if p.dkvsIndexer == nil {
		return nil
	}
	return p.dkvsIndexer.Subscriptions()
}

func (p *IndexerMgr) IsDKVSSubscribed(key string) bool {
	return p.dkvsIndexer != nil && p.dkvsIndexer.IsSubscribed(key)
}

func (p *IndexerMgr) ApplyDKVSMirror(filters []dkvs.Subscription, records []*wire.DKVSRecord, root chainhash.Hash) (int, error) {
	if p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.ApplyMirror(filters, records, root)
}
