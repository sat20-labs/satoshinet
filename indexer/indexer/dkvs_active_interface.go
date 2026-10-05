package indexer

import (
	"context"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *IndexerMgr) GetDKVSActivePage(ctx context.Context, request dkvs.ActiveSyncRequest) (*dkvs.ActivePage, error) {
	if p == nil || p.dkvsIndexer == nil { return nil, errDKVSNotInitialized }
	return p.dkvsIndexer.ActiveSyncPage(ctx, request)
}

func (p *IndexerMgr) WatchDKVSActive(ctx context.Context, request dkvs.ActiveWatchRequest) (*dkvs.ActiveWatchResult, error) {
	if p == nil || p.dkvsIndexer == nil { return nil, errDKVSNotInitialized }
	return p.dkvsIndexer.WatchActive(ctx, request)
}
