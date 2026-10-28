package base

import (
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	dbcodec "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	"github.com/stretchr/testify/require"
)

type failingLedgerReadDB struct{ indexer.KVDB }

func (f failingLedgerReadDB) BatchRead([]byte, bool, func([]byte, []byte) error) error {
	return fmt.Errorf("ledger read failed")
}

func TestChannelLedgerQuotaReadFailsClosed(t *testing.T) {
	database := dbcodec.NewKVDB(t.TempDir())
	defer database.Close()
	view := &RpcIndexer{BaseIndexer: BaseIndexer{db: failingLedgerReadDB{database}, utxoIndex: common.NewUTXOIndex()}}
	e := &common.ChannelLedgerEntry{ChannelId: "channel", Direction: "ascending", L2TxId: "a", Value: 100}
	view.utxoIndex.ChannelLedgerMap["a"] = e
	entries, err := view.GetChannelLedger("channel")
	require.ErrorContains(t, err, "ledger read failed")
	require.Nil(t, entries, "partial in-memory data must not be a successful balance")
}

func TestChannelLedgerQuotaReadIsDetachedAndDoesNotCacheHistory(t *testing.T) {
	database := dbcodec.NewKVDB(t.TempDir())
	defer database.Close()
	view := &RpcIndexer{BaseIndexer: BaseIndexer{db: database, utxoIndex: common.NewUTXOIndex()}}
	e := &common.ChannelLedgerEntry{ChannelId: "channel", Direction: "ascending", L2TxId: "a", Value: 100, L1Outpoints: []string{"a:0"}}
	key := string(stp.GetChannelLedgerDBKey(e))
	view.utxoIndex.ChannelLedgerMap[key] = e
	entries, err := view.GetChannelLedger("channel")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entries[0].Value = 0
	entries[0].L1Outpoints[0] = "changed"
	require.EqualValues(t, 100, e.Value)
	require.Equal(t, "a:0", e.L1Outpoints[0])
	delete(view.utxoIndex.ChannelLedgerMap, key)
	entries, err = view.GetChannelLedger("channel")
	require.NoError(t, err)
	require.Empty(t, entries)
}
