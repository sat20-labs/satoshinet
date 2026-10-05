package main

import (
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerShare "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/wire"
)

func (s *server) isRegisteredDKVSCore() bool {
	if s == nil || s.miningPubKey == "" || indexerShare.ShareIndexer == nil { return false }
	registry := indexerShare.ShareIndexer.GetSeqMgr()
	return registry != nil && registry.GetNodeType(s.miningPubKey) == indexercommon.NODE_TYPE_CORE
}

func (r *serverMessageBindingResolver) installDKVSWalletAdmission() {
	if r == nil || r.server == nil || r.server.assetIndexer == nil { return }
	r.server.assetIndexer.SetDKVSWalletRPCAdmission(r.server.isRegisteredDKVSCore,
		func(accountID string) (*wire.DKVSRecord, error) {
			record, core, err := r.current(accountID)
			if err != nil || core != r.server.miningPubKey {
				return nil, ErrMessageNotBoundHere
			}
			return record, nil
		})
}
