package indexer

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

func TestIsMainnetUsesNetworkIdentityAndHandlesNil(t *testing.T) {
	var nilManager *IndexerMgr
	if nilManager.IsMainnet() {
		t.Fatal("nil manager reported mainnet")
	}
	if (&IndexerMgr{}).IsMainnet() {
		t.Fatal("manager without chain params reported mainnet")
	}
	if !(&IndexerMgr{chaincfgParam: &chaincfg.MainNetParams}).IsMainnet() {
		t.Fatal("mainnet params were not recognized")
	}
	params := chaincfg.MainNetParams
	params.Name = "renamed-mainnet"
	if !(&IndexerMgr{chaincfgParam: &params}).IsMainnet() {
		t.Fatal("mainnet detection depended on the display name")
	}
	if (&IndexerMgr{chaincfgParam: &chaincfg.TestNetParams}).IsMainnet() {
		t.Fatal("testnet params reported mainnet")
	}
}

func TestRenamedMainnetStillDisablesChannelEventReports(t *testing.T) {
	params := chaincfg.MainNetParams
	params.Name = "renamed-mainnet"
	mgr := &IndexerMgr{chaincfgParam: &params}
	if err := mgr.RecordChannelStateEvent(&common.ChannelStateEvent{}); err == nil {
		t.Fatal("renamed mainnet accepted a testnet-only channel event report")
	}
}
