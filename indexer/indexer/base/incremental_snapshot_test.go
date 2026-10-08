package base

import (
	"testing"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/stretchr/testify/require"
)

func TestSubtractTrimsOnlyUnchangedHistoricalEntries(t *testing.T) {
	b, _, _ := membershipIndexer(t)
	b.utxoIndex.AscendMap["old"] = &common.AscendData{Height: 1}
	b.utxoIndex.DescendMap["old"] = &common.DescendData{Height: 1}
	b.utxoIndex.ChannelLedgerMap["old"] = &common.ChannelLedgerEntry{L2Height: 1}
	b.utxoIndex.ChannelStateEventMap["old"] = &common.ChannelStateEvent{PunishTxIds: []string{"one"}}
	b.utxoIndex.ReferrerMap["old"] = &common.ReferrerInfo{Name: "one"}
	backup := b.Clone(false)
	b.utxoIndex.AscendMap["changed"] = &common.AscendData{Height: 2}
	b.utxoIndex.DescendMap["changed"] = &common.DescendData{Height: 2}
	b.utxoIndex.ChannelLedgerMap["changed"] = &common.ChannelLedgerEntry{L2Height: 2}
	b.utxoIndex.ChannelStateEventMap["changed"] = &common.ChannelStateEvent{PunishTxIds: []string{"two"}}
	b.utxoIndex.ReferrerMap["changed"] = &common.ReferrerInfo{Name: "two"}
	pending := b.Clone(false)
	b.utxoIndex.AscendMap["changed"].Height = 3
	b.utxoIndex.DescendMap["changed"].Height = 3
	b.utxoIndex.ChannelLedgerMap["changed"].L2Height = 3
	b.utxoIndex.ChannelStateEventMap["changed"].PunishTxIds[0] = "three"
	b.utxoIndex.ReferrerMap["changed"].Name = "three"
	b.Subtract(backup)
	require.NotContains(t, b.utxoIndex.AscendMap, "old")
	require.NotContains(t, b.utxoIndex.DescendMap, "old")
	require.NotContains(t, b.utxoIndex.ChannelLedgerMap, "old")
	require.NotContains(t, b.utxoIndex.ChannelStateEventMap, "old")
	require.NotContains(t, b.utxoIndex.ReferrerMap, "old")
	b.Subtract(pending)
	require.Equal(t, 3, b.utxoIndex.AscendMap["changed"].Height)
	require.Equal(t, 3, b.utxoIndex.DescendMap["changed"].Height)
	require.Equal(t, 3, b.utxoIndex.ChannelLedgerMap["changed"].L2Height)
	require.Equal(t, "three", b.utxoIndex.ChannelStateEventMap["changed"].PunishTxIds[0])
	require.Equal(t, "three", b.utxoIndex.ReferrerMap["changed"].Name)
}
