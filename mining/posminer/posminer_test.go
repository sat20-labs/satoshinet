package posminer

import (
	"encoding/hex"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestSubmitNewBlockReportsAcceptance(t *testing.T) {
	if hash, height, err := submitNewBlock(nil, func(*btcutil.Block) bool { return true }); err == nil || hash != nil || height != 0 {
		t.Fatalf("nil block result hash=%v height=%d err=%v", hash, height, err)
	}

	block := &wire.MsgBlock{}
	if hash, height, err := submitNewBlock(block, func(*btcutil.Block) bool { return false }); err == nil || hash != nil || height != 0 {
		t.Fatalf("rejected block result hash=%v height=%d err=%v", hash, height, err)
	}

	hash, height, err := submitNewBlock(block, func(block *btcutil.Block) bool {
		block.SetHeight(42)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if hash == nil || *hash != block.BlockHash() || height != 42 {
		t.Fatalf("accepted block result hash=%v height=%d", hash, height)
	}
}

// Reorg replaces the sorter behind the same IndexerMgr instance.
type replaceableMiningIndexer struct {
	shareindexer.Indexer
	seq *scommon.MiningSequenceMgr
}

func (i *replaceableMiningIndexer) GetSeqMgr() *scommon.MiningSequenceMgr { return i.seq }

func TestValidatorManagerUsesReplacedSorterAndMembership(t *testing.T) {
	previous := shareindexer.ShareIndexer
	t.Cleanup(func() { shareindexer.ShareIndexer = previous })
	localKey, _ := btcec.PrivKeyFromBytes([]byte{1})
	parentKey, _ := btcec.PrivKeyFromBytes([]byte{2})
	local := hex.EncodeToString(localKey.PubKey().SerializeCompressed())
	parent := hex.EncodeToString(parentKey.PubKey().SerializeCompressed())
	params := chaincfg.TestNetParams
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
	old := scommon.NewMiningSequenceMgr(&params)
	require.NoError(t, old.Init(map[string]*scommon.CoreNodeInfo{local: scommon.NewCoreNodeInfo(nil)}, 0, ""))
	indexer := &replaceableMiningIndexer{seq: old}
	shareindexer.ShareIndexer = indexer
	vm := NewValidatorManager(&ValidatorManagerConfig{Config: &Config{MiningPubKey: local}})
	require.NotNil(t, vm)
	require.Equal(t, indexercommon.NODE_TYPE_BOOTSTRAP, vm.GetNodeType())
	newSeq := scommon.NewMiningSequenceMgr(&params)
	core := scommon.NewCoreNodeInfo(nil)
	core.ServerNode = parent
	require.NoError(t, newSeq.Init(map[string]*scommon.CoreNodeInfo{parent: scommon.NewCoreNodeInfo(nil), local: core}, 0, ""))
	indexer.seq = newSeq
	require.Equal(t, indexercommon.NODE_TYPE_CORE, vm.GetNodeType(), "same manager must read current membership")
	seq, myself, err := vm.currentMiningState()
	require.NoError(t, err)
	require.Same(t, newSeq, seq)
	require.Equal(t, parent, myself.Father.PubKey)
	require.NotSame(t, old.GetMiningInfo(local), myself)
	indexer.seq = nil
	require.Equal(t, indexercommon.NODE_TYPE_NORMAL, vm.GetNodeType())
	_, _, err = vm.currentMiningState()
	require.Error(t, err)
}
