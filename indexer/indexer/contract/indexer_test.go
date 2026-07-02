package contractindex

import (
	"testing"
	"time"

	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
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

func TestContractIndexerStoresEVMSourceMetadata(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	metadata := contractcommon.EVMSourceMetadata{
		ContractAddress: "tc1qexample",
		DeployTxID:      "txid",
		ContractName:    "AMM",
		Source:          "contract AMM {}",
		InitCodeHash:    "0x1234",
		VerifyStatus:    "stored",
	}
	metadata.CompilerConfig.SolcVersion = "0.8.30"
	require.NoError(t, idx.PutEVMSourceMetadata(metadata))

	got, ok := idx.GetEVMSourceMetadata(metadata.ContractAddress)
	require.True(t, ok)
	require.Equal(t, metadata.ContractAddress, got.ContractAddress)
	require.Equal(t, metadata.ContractName, got.ContractName)
	require.Equal(t, metadata.Source, got.Source)
	require.Equal(t, metadata.CompilerConfig.SolcVersion, got.CompilerConfig.SolcVersion)
	require.NotZero(t, got.SubmittedAt)
	require.NotZero(t, got.UpdatedAt)
}

func TestContractIndexerBindsResultWithoutContractOutput(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	defer kvdb.Close()

	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	deployTx, contractAddr := buildTestTemplateDeployTx(t)
	fundingVout := requireTestContractFundingVout(t, deployTx)
	resultTx := buildTestResultTxSpending(t, deployTx.TxHash(), fundingVout)
	idx.ProcessBlock(testContractBlock(10, deployTx, resultTx))

	history, total := idx.GetContractHistory(contractAddr.EncodeAddress(), 0, 0)
	require.Equal(t, 2, total)
	require.Len(t, history, 2)
	recordsByKind := make(map[string]contractengine.ContractHistoryRecord)
	for _, record := range history {
		recordsByKind[record.Kind] = record
	}
	require.Contains(t, recordsByKind, "deploy")
	require.Contains(t, recordsByKind, "result")
	result := recordsByKind["result"]
	require.Equal(t, contractAddr.EncodeAddress(), result.Contract)
	require.Equal(t, deployTx.TxID(), result.Details["result_for_txid"])
	require.Equal(t, float64(fundingVout), result.Details["result_for_vout"])
	require.Equal(t, "deploy", result.Details["result_for_kind"])
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
	tx, addr, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		Type:            contractcommon.ContractTypeTemplate,
		SubType:         contractcommon.TemplateLimitOrder,
		Deployer:        "deployer",
		DeployNonce:     1,
		ContractContent: []byte(`{"assetName":"ordx:ft:test"}`),
		GasLimit:        contractcommon.DeployBaseGas,
		Funding:         wire.TxOut{Value: 1},
	})
	require.NoError(t, err)
	return tx, addr
}

func buildTestTemplateInvokeTx(t *testing.T, contractAddr contractcommon.ContractAddress) *wire.MsgTx {
	t.Helper()
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:  contractAddr,
		GasLimit:  contractcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    contractcommon.TemplateInvokeAPIClose,
		Funding:   wire.TxOut{Value: 1},
	})
	require.NoError(t, err)
	return tx
}

func requireTestContractFundingVout(t *testing.T, tx *wire.MsgTx) uint32 {
	t.Helper()
	for i, txOut := range tx.TxOut {
		addr, ok, err := contractcommon.ParseContractPkScript(txOut.PkScript, contractcommon.TestnetContractPrefix)
		require.NoError(t, err)
		if ok {
			require.NotEmpty(t, addr.EncodeAddress())
			return uint32(i)
		}
	}
	t.Fatalf("contract funding output not found")
	return 0
}

func buildTestResultTxSpending(t *testing.T, hash chainhash.Hash, vout uint32) *wire.MsgTx {
	t.Helper()
	resultScript, err := contractcommon.ResultNullDataScript(contractcommon.ResultPayload{
		Status:      contractcommon.ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: hash, Index: vout}})
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))
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
