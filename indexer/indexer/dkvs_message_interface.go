package indexer

import (
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// GetDKVSMailboxPolicy exposes the local AccountBound mailbox policy to the
// MessageManager integration without making it part of the generic P2P store
// interface.
func (p *IndexerMgr) GetDKVSMailboxPolicy() dkvs.MailboxPolicy {
	if p == nil || p.dkvsIndexer == nil {
		return dkvs.MailboxPolicy{}
	}
	return p.dkvsIndexer.MailboxPolicy()
}

func (p *IndexerMgr) DeleteDKVSInternalMailbox(record *wire.DKVSRecord) (bool, error) {
	if p == nil || p.dkvsIndexer == nil {
		return false, errDKVSNotInitialized
	}
	return p.dkvsIndexer.DeleteInternalMailbox(record)
}

func (p *IndexerMgr) PutDKVSInternalTopicValues(values map[string][]byte) (int, error) {
	if p == nil || p.dkvsIndexer == nil {
		return 0, errDKVSNotInitialized
	}
	return p.dkvsIndexer.PutInternalTopicValues(values)
}

func (p *IndexerMgr) ListDKVSInternalTopicRecords() ([]*wire.DKVSRecord, error) {
	if p == nil || p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	return p.dkvsIndexer.ListInternalTopicRecords()
}

// GetDKVSAutopayRuntime exposes the already-resolved DKVS autopay verifier
// settings to MessageManager. This keeps MESSAGE_SEND/TOPIC_SERVICE on the
// same network pool and state provider as ordinary paid DKVS writes.
func (p *IndexerMgr) GetDKVSAutopayRuntime() (dkvs.AutopayStateProvider, string, string) {
	if p == nil {
		return nil, "", ""
	}
	cfg := p.dkvsConfig()
	switch verifier := cfg.FeeVerifier.(type) {
	case dkvs.LocalCacheAutopayFeeVerifier:
		return verifier.StateProvider, verifier.Contract, verifier.ServiceName
	case *dkvs.LocalCacheAutopayFeeVerifier:
		if verifier != nil {
			return verifier.StateProvider, verifier.Contract, verifier.ServiceName
		}
	case dkvs.AutopayFeeVerifier:
		return verifier.StateProvider, verifier.Contract, verifier.ServiceName
	case *dkvs.AutopayFeeVerifier:
		if verifier != nil {
			return verifier.StateProvider, verifier.Contract, verifier.ServiceName
		}
	}
	return nil, "", ""
}
