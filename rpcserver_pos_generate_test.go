package main

import (
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/mining"
	"github.com/sat20-labs/satoshinet/mining/posminer"
	"github.com/stretchr/testify/require"
)

func TestGenerateRPCDoesNotReportUnapprovedPOSV2Block(t *testing.T) {
	params := chaincfg.RegressionNetParams
	genesis := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesis
	params.POSV2Height = 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: &genesis}}
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer db.Close()
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: blockchain.NewMedianTime()})
	require.NoError(t, err)
	g := mining.NewBlkTmplGenerator(&mining.Policy{}, &params, nil, chain, blockchain.NewMedianTime(), nil, nil)
	miner := posminer.New(&posminer.Config{Chain: chain, ChainParams: &params, BlockTemplateGenerator: g})
	s := &rpcServer{cfg: rpcserverConfig{ChainParams: &params, Chain: chain, PosMiner: miner}}
	result, err := handleGenerate(s, &btcjson.GenerateCmd{NumBlocks: 1}, nil)
	require.Nil(t, result)
	var rpcErr *btcjson.RPCError
	require.ErrorAs(t, err, &rpcErr)
	require.Contains(t, rpcErr.Message, "Bootstrap approval")
	require.Equal(t, int32(0), chain.BestSnapshot().Height)
	require.False(t, miner.IsMining())
}
