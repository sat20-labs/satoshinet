package main

import (
	"bytes"
	"fmt"
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

func TestDKVSAutopayCancelDoesNotInterruptOtherPayer(t *testing.T) {
	params := chaincfg.TestNetParams
	stateDB, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer stateDB.Close()
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	payerB, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &params)
	require.NoError(t, err)
	payerA := "cancelling-payer"
	defaults := dkvs.NetworkDefaultsForParams(&params)
	ap := template.NewAutopayContract("dkvs", defaults.AutopayRecipient, defaults.AutopayFeeAssetName, defaults.FullRecordFeePerBlock)
	addr, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{19}, 32))
	require.NoError(t, err)
	content, err := ap.Encode()
	require.NoError(t, err)
	runtime, err := template.NewRuntimeWithDeployer(addr, contractcommon.DeployPayload{Type: contractcommon.ContractTypeTemplate,
		SubType: ap.TemplateName(), Version: ap.Version(), ContractContent: content}, nil, payerA)
	require.NoError(t, err)
	gas := framework.DefaultGasConfig()
	gas.GasAssetName = defaults.AutopayFeeAssetName
	amount, err := indexercommon.NewDecimalFromString(defaults.FullRecordFeePerBlock, 8)
	require.NoError(t, err)
	fee, err := gas.ContractFundingFee(framework.ExecutionKindTrigger, gas.TriggerBaseGas, true, 99)
	require.NoError(t, err)
	gasFunding := fee.Mul(indexercommon.NewDefaultDecimal(10))
	principal := amount.Mul(indexercommon.NewDefaultDecimal(10))
	for _, actor := range []string{payerA, payerB} {
		config := &template.AutopayConfigInvokeParam{AmountPerBlock: amount.String(), BlobKeyLimit: 1}
		funding := principal.Clone()
		if actor == payerA {
			config.GasFundingAmount = gasFunding.String()
			funding = funding.AddAlignPrecision(gasFunding)
		}
		param, err := config.Encode()
		require.NoError(t, err)
		_, err = runtime.ApplyInvoke(template.ApplyInvokeRequest{Action: template.InvokeAPIConfig, Param: param,
			CallID: actor, Invoker: actor, Height: 98, Timestamp: 98, GasAssetName: gas.GasAssetName,
			AssetPrecision: func(string) (int, bool) { return 8, true },
			FundingOutput: framework.ContractOutputFromFunding(contractcommon.FundingOutput{
				OutPoint: contractcommon.TxOutPoint{TxID: actor}, Contract: addr,
				Assets: wire.TxAssets{{Name: *wire.NewAssetNameFromString(gas.GasAssetName), Amount: *funding}},
			}),
		})
		require.NoError(t, err)
	}
	_, err = runtime.SettleBlockWithGasConfig(98, gas)
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(99, gas)
	require.NoError(t, err)
	store := template.NewRuntimeStore()
	store.Add(runtime)
	provider := localAutopayStateProvider{db: stateDB}
	height := uint64(99)
	data := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, data)
	defer data.Close()
	verifier := dkvs.AutopayFeeVerifier{StateProvider: &dkvs.HeightCachedAutopayStateProvider{
		Provider: provider, CurrentHeight: func() uint64 { return height }},
		Contract: addr.String(), ServiceName: "dkvs", Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, FullRecordFeePerBlock: defaults.FullRecordFeePerBlock, AddressParams: &params}
	index := dkvs.New(data, dkvs.Config{CurrentHeight: func() uint64 { return height }, TipHeight: func() uint64 { return height }, FeeVerifier: verifier})
	key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "cancel-other-payer/item")
	require.NoError(t, err)
	for height = 100; height <= 101; height++ {
		param, err := (&template.CloseInvokeParam{}).Encode()
		require.NoError(t, err)
		_, err = runtime.ApplyInvoke(template.ApplyInvokeRequest{Action: template.InvokeAPICancel, Param: param,
			CallID: fmt.Sprintf("cancel-%d", height), Invoker: payerA, Height: int64(height), Timestamp: int64(height), ResultGasFee: fee,
			FundingOutput: framework.ContractOutputFromFunding(contractcommon.FundingOutput{
				OutPoint: contractcommon.TxOutPoint{TxID: fmt.Sprintf("cancel-%d", height)}, Contract: addr,
			}),
		})
		require.NoError(t, err)
		plan, err := runtime.SettleBlockWithGasConfig(int64(height), gas)
		require.NoError(t, err)
		refunds := 0
		for _, transfer := range plan.Transfers {
			if transfer.To == payerA {
				refunds++
				require.Equal(t, principal.SubAlignPrecision(amount).String(), transfer.AssetAmt)
			}
		}
		if height == 100 {
			require.Equal(t, 1, refunds)
		} else {
			require.Zero(t, refunds, "zero-balance repeated cancel must not refund again")
		}
		runtime.SetCurrentBlock(int64(height))
		require.NoError(t, node.NewTemplateStateStore(stateDB).StoreBlockState(&chainhash.Hash{byte(height)}, store))
		raw, err := provider.GetAutopayState(addr.String())
		require.NoError(t, err)
		record, err := dkvs.NewAccountRecord(key, []byte("uninterrupted paid service"), dkvs.RecordOptions{Seq: height - 99, IssueHeight: height})
		require.NoError(t, err)
		proof, err := dkvs.NewAutopayFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), 0, addr.String(), "")
		require.NoError(t, err)
		record.FeeProof, err = dkvs.EncodeFeeProof(proof)
		require.NoError(t, err)
		hash := dkvs.SigningHash(record)
		signature, err := schnorr.Sign(priv, hash[:])
		require.NoError(t, err)
		record.Signature = signature.Serialize()
		_, err = index.PutLocal(record)
		require.NoError(t, err, "real DKVS verifier must accept the unaffected payer")
		require.Equal(t, int64(height), raw.Delegates[payerB].LastPayHeight, "other payer must pay in the cancellation block")
		paidBlocks := int64(height) - 98
		paid := amount.Mul(indexercommon.NewDefaultDecimal(paidBlocks))
		require.Equal(t, paidBlocks, raw.Delegates[payerB].PaidBlockCount)
		require.Equal(t, paid.String(), raw.Delegates[payerB].TotalPaid)
		require.Equal(t, principal.SubAlignPrecision(paid).String(), raw.Delegates[payerB].Balance)
		require.Equal(t, gasFunding.SubAlignPrecision(fee.Mul(indexercommon.NewDefaultDecimal(paidBlocks))).String(), raw.GasBalance)
		path, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		snapshot, err := index.GetPathSnapshot(path)
		require.NoError(t, err)
		require.Len(t, snapshot.Records, 1)
		require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(snapshot.Records[0]))
	}
}
