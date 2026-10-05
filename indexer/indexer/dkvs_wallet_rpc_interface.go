package indexer

import (
	"sync"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// The existing IndexerMgr is a process singleton. This slot holds callbacks
// owned by that live manager, not account data or a second binding registry.
// No callbacks installed means public KV writes are denied by default.
var walletRPCAdmissionSlot struct {
	sync.RWMutex
	owner  *IndexerMgr
	policy *dkvs.WalletRPCAdmission
}

func (p *IndexerMgr) SetDKVSWalletRPCAdmission(isCore func() bool,
	currentBinding func(string) (*wire.DKVSRecord, error)) {
	walletRPCAdmissionSlot.Lock()
	defer walletRPCAdmissionSlot.Unlock()
	if isCore == nil || currentBinding == nil {
		if walletRPCAdmissionSlot.owner == p {
			walletRPCAdmissionSlot.owner, walletRPCAdmissionSlot.policy = nil, nil
		}
		return
	}
	walletRPCAdmissionSlot.owner = p
	walletRPCAdmissionSlot.policy = &dkvs.WalletRPCAdmission{
		IsCoreNode: isCore, CurrentBinding: currentBinding,
	}
}

// Only the wallet-facing RPC router calls this method. Internal writers and
// authenticated P2P use separate methods and cannot be selected by HTTP input.

func (p *IndexerMgr) currentWalletWritePolicy() (*dkvs.WalletRPCAdmission, error) {
	if p == nil || p.dkvsIndexer == nil {
		return nil, errDKVSNotInitialized
	}
	walletRPCAdmissionSlot.RLock()
	defer walletRPCAdmissionSlot.RUnlock()
	if walletRPCAdmissionSlot.owner != p || walletRPCAdmissionSlot.policy == nil {
		return nil, dkvs.ErrPermissionDenied
	}
	policy := *walletRPCAdmissionSlot.policy
	policy.Indexer = p.dkvsIndexer
	return &policy, nil
}

func (p *IndexerMgr) PutDKVSRecordBatchCASAuthorized(mutations []dkvs.CASMutation,
	options dkvs.BatchCASOptions, authorization *dkvs.WalletWriteAuthorization) (*dkvs.WriteResult, error) {
	policy, err := p.currentWalletWritePolicy()
	if err != nil {
		return nil, err
	}
	return policy.PutRecords(mutations, options, authorization)
}
