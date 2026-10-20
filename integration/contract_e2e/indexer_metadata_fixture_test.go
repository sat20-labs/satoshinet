package contract_e2e

import (
	"strings"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/httpclient"
	"github.com/stretchr/testify/require"
)

func TestFakeL1IndexerReadPublicKey(t *testing.T) {
	const pub = "025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad"
	server := startFakeL1Indexer(t, pub, nil)
	client := httpclient.NewIndexerClient("http", strings.TrimPrefix(server.URL, "http://"), "testnet")
	got, err := client.GetIndexerPubkey("child")
	require.NoError(t, err)
	require.Equal(t, pub, got)
}

func TestFakeL1IndexerParentIdentities(t *testing.T) {
	utxos := map[string]*indexercommon.AssetsInUtxo{
		"shared:0": {OutPoint: "shared:0", Value: 1234},
	}
	for _, parent := range []string{"bootstrap", "core-first", "core-second"} {
		t.Run(parent, func(t *testing.T) {
			server := startFakeL1Indexer(t, parent, utxos)
			client := httpclient.NewIndexerClient("http", strings.TrimPrefix(server.URL, "http://"), "testnet")
			// Both children read their configured server's identity without registering.
			for _, child := range []string{"miner-first", "miner-second"} {
				got, err := client.GetIndexerPubkey(child)
				require.NoError(t, err)
				require.Equal(t, parent, got)
				output, err := client.GetTxUtxoAssets("shared:0")
				require.NoError(t, err)
				require.Equal(t, utxos["shared:0"], output)
			}
		})
	}
}
