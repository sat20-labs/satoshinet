package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Fixtures explicitly establish accepted quantities independently of physical
// outputs. An unsolicited physical output must never authorize itself.
func evmManagedFixture(t *testing.T, value int64, assets map[string]string) *evmcommon.ManagedBalance {
	t.Helper()
	balance := &evmcommon.ManagedBalance{}
	require.NoError(t, balance.Credit(value, nil))
	for name, amount := range assets {
		set, err := contractframework.NewAssetSet(name, mustDecimalString(t, amount))
		require.NoError(t, err)
		require.NoError(t, balance.Credit(0, set))
	}
	return balance
}

func seedEVMManagedFixture(t *testing.T, runtime *Runtime, addr ContractAddress, value int64, assets map[string]string) {
	t.Helper()
	balance, ok := runtime.State.ManagedBalance(addr)
	require.True(t, ok)
	*balance = evmManagedFixture(t, value, assets).Clone()
}

func TestBuildResultTx(t *testing.T) {
	addr := testContract(t)
	inputHash := chainhash.Hash{1, 2, 3}
	plan := ResultPlan{
		InputUTXOs: []UTXO{mustUTXO(t, OutPoint{TxID: inputHash.String(), Vout: 2}, addr, "ordx:ft:gas", 100, 0)},
		Outputs: []ResultOutput{
			mustResultOutput(t, "tb1qdest", SatoshiAssetName, 70),
			mustResultOutput(t, addr.MustEncode(), "ordx:ft:gas", 30),
		},
	}
	tx, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status: ResultStatusSuccess, ResultCount: 1, Plans: []ResultPlan{plan},
		ResolveScript: evmTestResultScriptResolver(t, addr),
	}, contractframework.ResultTxBuildOptions{UseInputUTXOs: true})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 1)
	require.Equal(t, inputHash, tx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(2), tx.TxIn[0].PreviousOutPoint.Index)
	require.Len(t, tx.TxOut, 3)
	require.Equal(t, int64(70), tx.TxOut[0].Value)
	require.Len(t, tx.TxOut[1].Assets, 1)
	require.Equal(t, "ordx:ft:gas", tx.TxOut[1].Assets[0].Name.String())
	require.LessOrEqual(t, tx.SerializeSize(), wire.MaxBlockPayload)
	parsed, err := ParseTx(tx, testContractResolver)
	require.NoError(t, err)
	require.Equal(t, TxTypeResult, parsed.Type)
	require.Equal(t, uint16(1), parsed.Result.ResultCount)
}

func TestBuildResultTxRejectsUnsupportedExtraData(t *testing.T) {
	_, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status: ResultStatusSuccess, ResultCount: 1,
		Plans: []ResultPlan{{Outputs: []ResultOutput{{To: "tb1qdest", Value: 1, ExtraData: []byte{1}}}}},
		ResolveScript: func(ResultOutput) ([]byte, error) { return []byte{txscript.OP_TRUE}, nil },
	}, contractframework.ResultTxBuildOptions{UseInputUTXOs: true})
	require.Error(t, err)
}

func TestBuildCanonicalResultTxDeployOnly(t *testing.T) {
	tx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status: ResultStatusSuccess,
		Records: []ExecutionRecord{{Kind: ExecutionKindDeploy, Type: TxTypeDeploy, Contract: testContract(t), Status: ResultStatusSuccess, RequiresResult: true}},
	})
	require.NoError(t, err)
	require.Empty(t, tx.TxIn)
	require.Len(t, tx.TxOut, 1)
	parsed, err := ParseTx(tx, nil)
	require.NoError(t, err)
	require.Equal(t, TxTypeResult, parsed.Type)
	require.Equal(t, uint16(1), parsed.Result.ResultCount)
}

func TestBuildCanonicalResultTxInvoke(t *testing.T) {
	addr := testContract(t)
	gasHash, assetHash := chainhash.Hash{1}, chainhash.Hash{2}
	gasAssetName := "ordx:ft:gas"
	gasInput := OutPoint{TxID: gasHash.String(), Vout: 1}
	assetInput := OutPoint{TxID: assetHash.String(), Vout: 0}
	record := ExecutionRecord{
		Kind: ExecutionKindInvoke, Type: TxTypeInvoke, Contract: addr, Status: ResultStatusSuccess,
		GasUsed: 10, FundingInputs: []OutPoint{gasInput}, RequiresResult: true,
		ManagedBalance: evmManagedFixture(t, 80, map[string]string{gasAssetName: "100000"}),
		AssetIntents: []AssetIntent{{From: addr, To: "tb1qdest", AssetName: SatoshiAssetName, Amount: mustDefaultDecimal(t, 70)}},
	}
	available := []UTXO{mustUTXO(t, gasInput, addr, gasAssetName, 100000, 10), mustUTXO(t, assetInput, addr, SatoshiAssetName, 80, 11)}
	gasConfig := GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 2, InvokeBaseGas: 10, ResultBaseGas: 5}
	provider := func(got ContractAddress) ([]UTXO, error) { require.True(t, addr.Equal(got)); return available, nil }
	tx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status: ResultStatusSuccess, Records: []ExecutionRecord{record}, GasConfig: gasConfig,
		UTXOs: provider, ResolveScript: evmTestResultScriptResolver(t, addr),
	})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 2)
	require.Len(t, tx.TxOut, 3)
	require.Equal(t, gasHash, tx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, assetHash, tx.TxIn[1].PreviousOutPoint.Hash)
	require.Equal(t, int64(10), tx.TxOut[1].Value)
	require.Len(t, tx.TxOut[1].Assets, 1)
	verifier := CanonicalResultVerifier{GasConfig: gasConfig, UTXOs: provider,
		ResolveOutput: evmTestResultOutputResolver(addr), ResolveScript: evmTestResultScriptResolver(t, addr)}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestBuildCanonicalResultTxRefundsUnclaimedGasFunding(t *testing.T) {
	testCanonicalGasClaim(t, 0, "999.95")
}

func TestBuildCanonicalResultTxKeepsClaimedGasFunding(t *testing.T) {
	testCanonicalGasClaim(t, 300, "699.95")
}

func testCanonicalGasClaim(t *testing.T, retained uint64, expectedRefund string) {
	t.Helper()
	addr := testContract(t)
	gasName := "ordx:ft:gas"
	input := OutPoint{TxID: chainhash.Hash{1}.String(), Vout: 1}
	record := ExecutionRecord{
		Kind: ExecutionKindInvoke, Type: TxTypeInvoke, Contract: addr, Status: ResultStatusSuccess,
		GasUsed: 10, FundingInputs: []OutPoint{input}, GasRefundRecipient: "tb1qdest", RequiresResult: true,
		ManagedBalance: evmManagedFixture(t, 0, map[string]string{gasName: "1000"}),
	}
	if retained != 0 {
		record.RetainedGasFunding = mustDefaultDecimal(t, retained)
	}
	tx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status: ResultStatusSuccess, Records: []ExecutionRecord{record},
		GasConfig: GasConfig{GasAssetName: gasName, FixedGasPrice: 1, InvokeBaseGas: 10, ResultBaseGas: 50},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, addr.Equal(got))
			return []UTXO{mustUTXO(t, input, addr, gasName, 1000, 10)}, nil
		}, ResolveScript: evmTestResultScriptResolver(t, addr),
	})
	require.NoError(t, err)
	outputs, err := evmTestResultOutputResolver(addr)(tx)
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "tb1qdest", gasName, expectedRefund)
	if retained == 0 {
		requireNoResultAssetAmount(t, outputs, addr.MustEncode(), gasName)
	} else {
		requireResultAssetAmount(t, outputs, addr.MustEncode(), gasName, "300")
	}
}

func TestBuildCanonicalResultTxTruncatesAssetOutputsToPrecision(t *testing.T) {
	addr := testContract(t)
	assetName := "brc20:f:ooxx"
	input := OutPoint{TxID: chainhash.Hash{3}.String(), Vout: 0}
	record := ExecutionRecord{
		Kind: ExecutionKindInvoke, Type: TxTypeInvoke, Contract: addr, Status: ResultStatusSuccess,
		RequiresResult: true, ResultFeeMode: ResultFeeModePlainTxFee,
		ManagedBalance: evmManagedFixture(t, 0, map[string]string{assetName: "10"}),
		AssetIntents: []AssetIntent{{From: addr, To: "tb1qdest", AssetName: assetName, Amount: mustDecimalString(t, "3.9")}},
	}
	precision := SettlementPrecision(func(name string) (int, bool) { return 0, name == assetName })
	provider := func(got ContractAddress) ([]UTXO, error) {
		require.True(t, addr.Equal(got))
		return []UTXO{mustDecimalUTXO(t, input, addr, assetName, "10", 11)}, nil
	}
	tx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status: ResultStatusSuccess, Records: []ExecutionRecord{record}, GasConfig: GasConfig{GasAssetName: assetName},
		UTXOs: provider, Precision: precision, ResolveScript: evmTestResultScriptResolver(t, addr),
	})
	require.NoError(t, err)
	require.Len(t, tx.TxOut, 3)
	require.Len(t, tx.TxOut[0].Assets, 1)
	require.Equal(t, assetName, tx.TxOut[0].Assets[0].Name.String())
	require.Equal(t, "3", tx.TxOut[0].Assets[0].Amount.String())
	require.Len(t, tx.TxOut[1].Assets, 1)
	require.Equal(t, "7", tx.TxOut[1].Assets[0].Amount.String())
	verifier := CanonicalResultVerifier{GasConfig: GasConfig{GasAssetName: assetName}, UTXOs: provider,
		Precision: precision, ResolveOutput: evmTestResultOutputResolver(addr), ResolveScript: evmTestResultScriptResolver(t, addr)}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}


func TestBuildCanonicalResultTxPreservesOrdXBindingSat(t *testing.T) {
	addr := testContract(t)
	const assetName = "ordx:f:bound-result"
	input := OutPoint{TxID: chainhash.Hash{4}.String(), Vout: 0}
	assets := wire.TxAssets{{
		Name: *wire.NewAssetNameFromString(assetName),
		Amount: *scommon.NewDefaultDecimal(2),
		BindingSat: 2,
	}}
	managed := &evmcommon.ManagedBalance{}
	require.NoError(t, managed.Credit(1, assets))

	record := ExecutionRecord{
		Kind: ExecutionKindInvoke, Type: TxTypeInvoke, Contract: addr,
		Status: ResultStatusSuccess, RequiresResult: true,
		ResultFeeMode: ResultFeeModePlainTxFee,
		ManagedBalance: managed,
		AssetIntents: []AssetIntent{{
			From: addr, To: "tb1qdest", AssetName: assetName,
			Amount: scommon.NewDefaultDecimal(1),
		}},
	}
	provider := func(got ContractAddress) ([]UTXO, error) {
		require.True(t, addr.Equal(got))
		return []UTXO{contractframework.UTXOFromTxOutput(input, addr, 1,
			&wire.TxOut{Value: 1, Assets: assets.Clone()})}, nil
	}
	tx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status: ResultStatusSuccess, Records: []ExecutionRecord{record},
		GasConfig: GasConfig{GasAssetName: DefaultGasConfig().GasAssetName},
		UTXOs: provider, ResolveScript: evmTestResultScriptResolver(t, addr),
	})
	require.NoError(t, err)

	var boundValues []int64
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		asset, findErr := txOut.Assets.Find(wire.NewAssetNameFromString(assetName))
		if findErr != nil || asset == nil {
			continue
		}
		require.Equal(t, "1", asset.Amount.String())
		require.Equal(t, uint32(2), asset.BindingSat)
		boundValues = append(boundValues, txOut.Value)
	}
	require.ElementsMatch(t, []int64{0, 1}, boundValues,
		"partial OrdX output uses zero carrier sats while the remaining physical sat stays with contract change")
}
