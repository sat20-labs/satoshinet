package contractindex

import (
	"testing"
	"time"

	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	sncommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestContractIndexerSubtractBeforeBackupUpdateDBKeepsOnlyPendingDelta(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	deployTx, contractAddr := buildTestTemplateDeployTx(t)
	idx.ProcessBlock(testContractBlock(10, deployTx))

	backup := idx.Clone()
	idx.Subtract(backup)
	backup.UpdateDB()

	summary, ok := idx.GetContractSummary(contractAddr.EncodeAddress())
	require.True(t, ok)
	require.Equal(t, int64(10), summary.CreatedHeight)
	history, total := idx.GetContractHistory(contractAddr.EncodeAddress(), 0, 0)
	require.Equal(t, 1, total)
	require.Len(t, history, 1)
	require.Equal(t, "deploy", history[0].Kind)

	invokeTx := buildTestTemplateInvokeTx(t, contractAddr)
	idx.ProcessBlock(testContractBlock(11, invokeTx))
	history, total = idx.GetContractHistory(contractAddr.EncodeAddress(), 0, 0)
	require.Equal(t, 2, total)
	require.Equal(t, "deploy", history[0].Kind)
	require.Equal(t, "invoke", history[1].Kind)

	backup = idx.Clone()
	idx.Subtract(backup)
	backup.UpdateDB()
	history, total = idx.GetContractHistory(contractAddr.EncodeAddress(), 0, 0)
	require.Equal(t, 2, total)
	require.Len(t, history, 2)
}

func TestIsContractInteractionTxFiltersPlainTx(t *testing.T) {
	plainTx := wire.NewMsgTx(2)
	plainTx.AddTxOut(wire.NewTxOut(1000, nil, []byte{txscript.OP_TRUE}))
	require.False(t, isContractInteractionTx(plainTx, contractcommon.TestnetContractPrefix))

	deployTx, contractAddr := buildTestTemplateDeployTx(t)
	require.True(t, isContractInteractionTx(deployTx, contractcommon.TestnetContractPrefix))

	defaultInvokeTx := wire.NewMsgTx(2)
	fundingOut, err := contractcommon.NewContractTxOut(1, nil, contractAddr)
	require.NoError(t, err)
	defaultInvokeTx.AddTxOut(fundingOut)
	require.True(t, isContractInteractionTx(defaultInvokeTx, contractcommon.TestnetContractPrefix))
}

func TestContractIndexerCheckSelfAcceptsPersistedAndBufferedData(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	deployTx, contractAddr := buildTestTemplateDeployTx(t)
	idx.ProcessBlock(testContractBlock(10, deployTx))
	idx.UpdateDB()

	invokeTx := buildTestTemplateInvokeTx(t, contractAddr)
	idx.ProcessBlock(testContractBlock(11, invokeTx))
	require.True(t, idx.CheckSelf())
}

func TestContractIndexerCheckSelfRejectsMismatchedSummaryKey(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	wb := kvdb.NewWriteBatch()
	require.NoError(t, setJSON(wb, []byte(contractSummaryKey("contract-a")), &contractengine.ContractSummary{Address: "contract-b"}))
	require.NoError(t, wb.Flush())
	wb.Close()

	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	require.False(t, idx.CheckSelf())
}

func TestContractIndexerCheckSelfRejectsHistoryWithoutSummary(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	record := contractengine.ContractHistoryRecord{Contract: "contract-a", TxID: "tx-a"}
	wb := kvdb.NewWriteBatch()
	require.NoError(t, setJSON(wb, []byte(contractHistoryKey("contract-a", record, 0)), &record))
	require.NoError(t, wb.Flush())
	wb.Close()

	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	require.False(t, idx.CheckSelf())
}

func buildTestTemplateDeployTx(t *testing.T) (*wire.MsgTx, contractcommon.ContractAddress) {
	t.Helper()
	tx, addr, err := contractcommon.BuildTemplateDeployTx(contractcommon.TemplateDeployTxBuildRequest{
		TemplateName:    contractcommon.TemplateLimitOrder,
		Deployer:        "deployer",
		Random:          []byte("random"),
		ContractContent: []byte(`{"assetName":"ordx:ft:test"}`),
		GasLimit:        contractcommon.DeployBaseGas,
		Funding:         wire.TxOut{Value: 1},
	})
	require.NoError(t, err)
	return tx, addr
}

func buildTestTemplateInvokeTx(t *testing.T, contractAddr contractcommon.ContractAddress) *wire.MsgTx {
	t.Helper()
	tx, err := contractcommon.BuildTemplateInvokeTx(contractcommon.TemplateInvokeTxBuildRequest{
		Contract:  contractAddr,
		GasLimit:  contractcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    contractcommon.TemplateInvokeAPIClose,
		Funding:   wire.TxOut{Value: 1},
	})
	require.NoError(t, err)
	return tx
}

func testContractBlock(height int, txs ...*wire.MsgTx) *sncommon.Block {
	block := &sncommon.Block{
		Timestamp:    time.Unix(int64(height), 0),
		Height:       height,
		Transactions: make([]*sncommon.Transaction, 0, len(txs)),
	}
	for _, tx := range txs {
		block.Transactions = append(block.Transactions, &sncommon.Transaction{
			Txid:  tx.TxID(),
			MsgTx: tx,
		})
	}
	return block
}
