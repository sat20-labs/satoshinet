package indexer

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	idxcommon "github.com/sat20-labs/indexer/common"
	idxdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/base"
	contractindex "github.com/sat20-labs/satoshinet/indexer/indexer/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type commitGateDB struct {
	idxcommon.KVDB
	gate, release  chan struct{}
	failContract   bool
	contractFailed bool
}
type commitGateBatch struct {
	idxcommon.WriteBatch
	db       *commitGateDB
	contract bool
}

func (d *commitGateDB) NewWriteBatch() idxcommon.WriteBatch {
	return &commitGateBatch{WriteBatch: d.KVDB.NewWriteBatch(), db: d}
}
func (w *commitGateBatch) Put(k, v []byte) error {
	if strings.HasPrefix(string(k), "contract:v1:") {
		w.contract = true
	}
	return w.WriteBatch.Put(k, v)
}
func (w *commitGateBatch) Flush() error {
	if w.db.gate != nil && !w.contract {
		close(w.db.gate)
		<-w.db.release
	}
	if w.contract && w.db.failContract {
		w.db.contractFailed = true
		return fmt.Errorf("injected contract batch commit failure")
	}
	return w.WriteBatch.Flush()
}

func commitBufferFixture(t *testing.T) (*IndexerMgr, *commitGateDB, string, []*wire.MsgBlock, string) {
	t.Helper()
	oldChain := idxcommon.CHAIN
	idxcommon.CHAIN = "testnet"
	t.Cleanup(func() { idxcommon.CHAIN = oldChain })
	params := chaincfg.TestNetParams
	path := t.TempDir()
	db := &commitGateDB{KVDB: idxdb.NewKVDB(path)}
	t.Cleanup(func() { db.KVDB.Close() })
	compiling := base.NewBaseIndexer(db, &params, 0, 30)
	compiling.Init()
	compiling.SetBlockCallback(func(*common.Block) {})
	addr, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{19}, 20), &params)
	require.NoError(t, err)
	script, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	blocks := []*wire.MsgBlock{params.GenesisBlock}
	for h := 1; h <= 2; h++ {
		tx := wire.NewMsgTx(1)
		tx.LockTime = uint32(h)
		tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: ^uint32(0)}, []byte{1, byte(h)}, nil))
		tx.AddTxOut(wire.NewTxOut(1000, nil, script))
		blocks = append(blocks, &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: blocks[h-1].BlockHash(), Nonce: uint32(h)}, Transactions: []*wire.MsgTx{tx}})
	}
	require.NoError(t, compiling.SyncBlock(blocks[0], 0, 2, false))
	require.NoError(t, compiling.SyncBlock(blocks[1], 1, 2, false))
	compiling.UpdateDB()
	mgr := &IndexerMgr{baseDB: db, chaincfgParam: &params, compiling: compiling, rpcService: base.NewRpcIndexer(compiling), contractIndexer: contractindex.NewIndexer(db, &params)}
	compiling.SetBlockCallback(mgr.processBlock)
	compiling.SetUpdateDBCallback(mgr.forceUpdateDB)
	return mgr, db, path, blocks, addr.EncodeAddress()
}

func TestBackupCommitBlocksAddressCacheRefill(t *testing.T) {
	mgr, db, path, blocks, address := commitBufferFixture(t)
	require.NoError(t, mgr.compiling.SyncBlock(blocks[2], 2, 2, false))
	mgr.prepareDBBuffer()
	db.gate, db.release = make(chan struct{}), make(chan struct{})
	committed := make(chan struct{})
	go func() { mgr.performUpdateDBInBuffer(); close(committed) }()
	<-db.gate
	read := make(chan map[uint64]int64, 1)
	go func() { utxos, _ := mgr.compiling.GetUTXOs(address); read <- utxos }()
	var values map[uint64]int64
	select {
	case values = <-read:
		t.Error("cache refill returned while the durable address table was still stale")
	case <-time.After(100 * time.Millisecond):
	}
	close(db.release)
	<-committed
	db.gate = nil
	if values == nil {
		values = <-read
	}
	assert.Len(t, values, 2)
	mgr.prepareDBBuffer()
	mgr.performUpdateDBInBuffer()
	require.NoError(t, db.KVDB.Close())
	db.KVDB = idxdb.NewKVDB(path)
	restored := base.NewBaseIndexer(db, &chaincfg.TestNetParams, 0, 30)
	restored.Init()
	values, err := restored.GetUTXOs(address)
	require.NoError(t, err)
	require.Len(t, values, 2)
	for _, block := range blocks[1:] {
		_, err := restored.GetUtxoInfo(block.Transactions[0].TxID() + ":0")
		require.NoError(t, err)
	}
	require.True(t, restored.CheckSelf())
}

// Indexer writes deliberately commit independently, as in the original L2
// indexer. A failure must panic; the failed database is discarded and rebuilt,
// rather than repaired or resumed from a partially committed height.
func TestIndexerCommitFailurePanicsAndRebuildsFromFreshDB(t *testing.T) {
	for _, mode := range []string{"backup", "historical-force"} {
		t.Run(mode, func(t *testing.T) {
			tx, addr, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{Type: contractcommon.ContractTypeTemplate, SubType: contractcommon.TemplateLimitOrder, Deployer: "deployer", DeployNonce: 1, ContractContent: []byte(`{"assetName":"ordx:ft:test"}`), GasLimit: contractcommon.DeployBaseGas, Funding: wire.TxOut{Value: 1}})
			require.NoError(t, err)
			mgr, db, path, blocks, _ := commitBufferFixture(t)
			blocks[2].Transactions = append(blocks[2].Transactions, tx)
			commit := func(mgr *IndexerMgr, blocks []*wire.MsgBlock) {
				t.Helper()
				if mode == "historical-force" {
					// tip-height == keepBlockHistory invokes the real Base callback.
					require.NoError(t, mgr.compiling.SyncBlock(blocks[2], 2, 22, true))
					return
				}
				require.NoError(t, mgr.compiling.SyncBlock(blocks[2], 2, 2, false))
				mgr.prepareDBBuffer()
				mgr.performUpdateDBInBuffer()
			}
			db.failContract = true
			require.Panics(t, func() { commit(mgr, blocks) })
			require.True(t, db.contractFailed, "failure must come from the actual Contract Flush")
			require.NoError(t, db.KVDB.Close())
			db.KVDB = idxdb.NewKVDB(path)
			// Inspect the failure only; never replay into this inconsistent DB.
			durable := base.NewBaseIndexer(db, &chaincfg.TestNetParams, 0, 30)
			durable.Init()
			require.Equal(t, 2, durable.GetSyncHeight(), "Base committed before the Contract failure")
			contracts := contractindex.NewIndexer(db, &chaincfg.TestNetParams)
			_, ok := contracts.GetContractSummary(addr.EncodeAddress())
			require.False(t, ok)

			// A separate empty DB replays genesis onward through the same fixture.
			freshMgr, freshDB, freshPath, freshBlocks, _ := commitBufferFixture(t)
			require.NotEqual(t, path, freshPath)
			freshBlocks[2].Transactions = append(freshBlocks[2].Transactions, tx)
			commit(freshMgr, freshBlocks)
			require.NoError(t, freshDB.KVDB.Close())
			freshDB.KVDB = idxdb.NewKVDB(freshPath)
			freshBase := base.NewBaseIndexer(freshDB, &chaincfg.TestNetParams, 0, 30)
			freshBase.Init()
			require.Equal(t, 2, freshBase.GetSyncHeight())
			contracts = contractindex.NewIndexer(freshDB, &chaincfg.TestNetParams)
			summary, ok := contracts.GetContractSummary(addr.EncodeAddress())
			require.True(t, ok)
			require.Equal(t, int64(2), summary.CreatedHeight)
			history, total := contracts.GetContractHistory(addr.EncodeAddress(), 0, 0)
			require.Equal(t, 1, total)
			require.Len(t, history, 1)
		})
	}
}
