package main

import (
	"bytes"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDKVSClosedAutopaySnapshotRetainsLocalRecordThroughGrace(t *testing.T) {
	params := chaincfg.TestNetParams
	stateDB, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer stateDB.Close()
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	payer, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &params)
	require.NoError(t, err)
	defaults := dkvs.NetworkDefaultsForParams(&params)
	ap := template.NewAutopayContract("dkvs", defaults.AutopayRecipient, defaults.AutopayFeeAssetName, defaults.FullRecordFeePerBlock)
	addr, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{17}, 32))
	require.NoError(t, err)
	content, err := ap.Encode()
	require.NoError(t, err)
	runtime, err := template.NewRuntimeWithDeployer(addr, contractcommon.DeployPayload{Type: contractcommon.ContractTypeTemplate,
		SubType: ap.TemplateName(), Version: ap.Version(), ContractContent: content}, nil, payer)
	require.NoError(t, err)
	require.True(t, runtime.DeploymentFlags().Closable())
	gas := framework.DefaultGasConfig()
	gas.GasAssetName = defaults.AutopayFeeAssetName
	fee, err := gas.ContractFundingFee(framework.ExecutionKindTrigger, gas.TriggerBaseGas, true, 99)
	require.NoError(t, err)
	gasFunding := fee.AddAlignPrecision(fee).AddAlignPrecision(fee)
	amount, err := indexercommon.NewDecimalFromString(defaults.FullRecordFeePerBlock, 8)
	require.NoError(t, err)
	param, err := (&template.AutopayConfigInvokeParam{AmountPerBlock: amount.String(), GasFundingAmount: gasFunding.String(), BlobKeyLimit: 1}).Encode()
	require.NoError(t, err)
	funding := gasFunding.AddAlignPrecision(amount).AddAlignPrecision(amount)
	_, err = runtime.ApplyInvoke(template.ApplyInvokeRequest{Action: template.InvokeAPIConfig, Param: param,
		CallID: "closed-snapshot-config", Invoker: payer, Height: 98, Timestamp: 98, GasAssetName: gas.GasAssetName,
		AssetPrecision: func(string) (int, bool) { return 8, true },
		FundingOutput: framework.ContractOutputFromFunding(contractcommon.FundingOutput{
			OutPoint: contractcommon.TxOutPoint{TxID: "closed-snapshot-config"}, Contract: addr,
			Assets: wire.TxAssets{{Name: *wire.NewAssetNameFromString(gas.GasAssetName), Amount: *funding}},
		}),
	})
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(98, gas)
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(99, gas)
	require.NoError(t, err)
	runtime.SetCurrentBlock(99)
	store := template.NewRuntimeStore()
	store.Add(runtime)
	require.NoError(t, node.NewTemplateStateStore(stateDB).StoreBlockState(&chainhash.Hash{99}, store))
	provider := localAutopayStateProvider{db: stateDB}
	height := uint64(99)
	newConfig := func() dkvs.Config {
		cached := &dkvs.HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return height }}
		return dkvs.Config{EndpointID: "closed-autopay-source", CurrentHeight: func() uint64 { return height }, TipHeight: func() uint64 { return height },
			AllowFreeLocal: true, FreeLocalCache: dkvs.FreeLocalCachePolicy{Enabled: true, MaxTTL: 3},
			FeeVerifier: dkvs.LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: dkvs.AutopayFeeVerifier{
				StateProvider: cached, Contract: addr.String(), ServiceName: "dkvs", Recipient: defaults.AutopayRecipient,
				FeeAssetName: defaults.AutopayFeeAssetName, FullRecordFeePerBlock: defaults.FullRecordFeePerBlock, AddressParams: &params,
			}, AllowFreeLocal: true}}
	}
	data := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, data)
	defer data.Close()
	config := newConfig()
	source := dkvs.New(data, config)
	key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "closed-payment/item")
	require.NoError(t, err)
	record, err := dkvs.NewAccountRecord(key, []byte("retained paid record"), dkvs.RecordOptions{Seq: 1, IssueHeight: height})
	require.NoError(t, err)
	proof, err := dkvs.NewAutopayFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), 0, addr.String(), "")
	require.NoError(t, err)
	record.FeeProof, err = dkvs.EncodeFeeProof(proof)
	require.NoError(t, err)
	signingHash := dkvs.SigningHash(record)
	signature, err := schnorr.Sign(priv, signingHash[:])
	require.NoError(t, err)
	record.Signature = signature.Serialize()
	_, err = source.PutLocal(record)
	require.NoError(t, err)
	path, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	before, err := source.GetPathSnapshot(path)
	require.NoError(t, err)
	require.Len(t, before.Records, 1)

	closeParam, err := (&template.CloseInvokeParam{}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(template.ApplyInvokeRequest{Action: template.InvokeAPIClose, Param: closeParam,
		CallID: "closed-snapshot-close", Invoker: payer, Height: 100, Timestamp: 100,
		FundingOutput: framework.ContractOutputFromFunding(contractcommon.FundingOutput{
			OutPoint: contractcommon.TxOutPoint{TxID: "closed-snapshot-close"}, Contract: addr,
		}),
	})
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(100, gas)
	require.NoError(t, err)
	runtime.SetCurrentBlock(100)
	require.NoError(t, node.NewTemplateStateStore(stateDB).StoreBlockState(&chainhash.Hash{100}, store))
	raw, err := provider.GetAutopayState(addr.String())
	require.NoError(t, err)
	require.True(t, raw.Closed)
	require.Equal(t, "closed", raw.Status)
	require.Equal(t, int64(100), raw.CurrentBlock)
	require.Equal(t, int64(99), raw.Delegates[payer].LastPayHeight)
	height = 101 // No invoke or settlement of this closed runtime after height 100.
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "warm", true: "cold"}[cold], func(t *testing.T) {
			reader := source
			viewConfig := config
			if cold {
				viewConfig = newConfig()
				reader = dkvs.New(data, viewConfig)
			}
			snapshot, err := reader.GetPathSnapshot(path)
			require.NoError(t, err, "confirmed closed contract must permit an empty network snapshot")
			require.Empty(t, snapshot.Records)
			require.Equal(t, height, snapshot.PathMeta.ViewHeight)
			verified, err := viewConfig.FeeVerifier.(dkvs.LocalCacheAutopayFeeVerifier).StateProvider.GetAutopayState(addr.String())
			require.NoError(t, err)
			require.Equal(t, int64(height), verified.CurrentBlock)
			require.Equal(t, raw.Closed, verified.Closed)
			require.Equal(t, raw.Status, verified.Status)
			require.Equal(t, raw.Delegates, verified.Delegates, "closed history and delegate status must not be synthesized")
			got, err := reader.Get(key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(got))
		})
	}
	height = 102
	count, err := source.PruneExpiredAutopayAt(height)
	require.NoError(t, err)
	require.Zero(t, count, "grace boundary is inclusive")
	_, err = source.Get(key)
	require.NoError(t, err)
	height = 103
	count, err = source.PruneExpiredAutopayAt(height)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	_, err = source.Get(key)
	require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	after, err := provider.GetAutopayState(addr.String())
	require.NoError(t, err)
	require.Equal(t, raw, after, "verifier normalization must not mutate committed contract state")
}
