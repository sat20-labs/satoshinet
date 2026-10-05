package dkvs

import (
	"context"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

func TestActiveSyncNeverSeenPrefixIsEmptyCurrentView(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "never-seen/value")
	if err != nil { t.Fatal(err) }
	prefix, err := CollectionPathForKey(key)
	if err != nil { t.Fatal(err) }

	page, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{
		Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true,
	})
	if err != nil {
		t.Fatalf("never-seen prefix must be a confirmed empty current view: %v", err)
	}
	if page == nil || !page.Complete || page.Next != nil || len(page.Records) != 0 {
		t.Fatalf("unexpected empty page: %+v", page)
	}
	if page.Meta.EndpointID != idx.EndpointID() || page.Meta.Scope.Prefix != prefix ||
		page.Meta.Generation != 0 || page.Meta.Root != (chainhash.Hash{}) {
		t.Fatalf("unexpected empty metadata: %+v", page.Meta)
	}
}
