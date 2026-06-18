package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestCanonicalResultVerifier(t *testing.T) {
	contract := testContract(t)
	gasAssetName := "ordx:ft:gas"
	fundingHash := chainhash.Hash{1}
	assetHash := chainhash.Hash{2}
	funding := OutPoint{TxID: fundingHash.String(), Vout: 1}
	assetInput := OutPoint{TxID: assetHash.String(), Vout: 0}
	record := ExecutionRecord{
		Type:           TxTypeInvoke,
		Contract:       contract,
		Status:         ResultStatusSuccess,
		GasUsed:        10,
		FundingInputs:  []OutPoint{funding},
		RequiresResult: true,
		AssetIntents: []AssetIntent{{
			From:      contract,
			To:        "tb1qdest",
			AssetName: SatoshiAssetName,
			Amount:    mustDefaultDecimal(t, 70),
		}},
	}
	available := []UTXO{
		mustUTXO(t, funding, contract, gasAssetName, 100, 10),
		mustUTXO(t, assetInput, contract, SatoshiAssetName, 80, 11),
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: fundingHash, Index: 1}, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: assetHash, Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, []byte{0x51}))

	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 2, InvokeBaseGas: 10, ResultBaseGas: 5},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveOutput: func(*wire.MsgTx) ([]ResultOutput, error) {
			return []ResultOutput{
				mustResultOutput(t, "tb1qdest", SatoshiAssetName, 70),
				{
					To:     contract.MustEncode(),
					Value:  10,
					Assets: mustResultOutputWithDecimalAsset(t, contract.MustEncode(), gasAssetName, "99.97").Assets,
				},
			}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))

	tx.TxIn[0], tx.TxIn[1] = tx.TxIn[1], tx.TxIn[0]
	require.Error(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestCanonicalResultVerifierSkipsMetadataOnlyDeploySettlement(t *testing.T) {
	record := ExecutionRecord{
		Type:           TxTypeDeploy,
		Contract:       testContract(t),
		Status:         ResultStatusSuccess,
		RequiresResult: true,
	}
	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: "gas", FixedGasPrice: 1, ResultPackingFee: 1},
		UTXOs: func(ContractAddress) ([]UTXO, error) {
			t.Fatal("deploy settlement must not require canonical asset inputs")
			return nil, nil
		},
	}
	require.NoError(t, verifier.Verify(wire.NewMsgTx(2), []ExecutionRecord{record}))
}

func TestCanonicalResultVerifierDeployUsesFundingUTXO(t *testing.T) {
	contract := testContract(t)
	gasHash := chainhash.Hash{5}
	gasInput := OutPoint{TxID: gasHash.String(), Vout: 1}
	record := ExecutionRecord{
		Type:           TxTypeDeploy,
		Kind:           ExecutionKindDeploy,
		Contract:       contract,
		Status:         ResultStatusSuccess,
		GasUsed:        10,
		FundingInputs:  []OutPoint{gasInput},
		RequiresResult: true,
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: gasHash, Index: 1}, nil, nil))
	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: "ordx:ft:gas", FixedGasPrice: 1, DeployBaseGas: 10, ResultBaseGas: 1},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, gasInput, contract, "ordx:ft:gas", 20, 1),
			}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestCanonicalResultVerifierInvokeFeeSettlementWithoutAssetIntent(t *testing.T) {
	contract := testContract(t)
	gasAssetName := "ordx:ft:gas"
	gasHash := chainhash.Hash{9}
	gasInput := OutPoint{TxID: gasHash.String(), Vout: 1}
	record := ExecutionRecord{
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		Contract:       contract,
		Status:         ResultStatusSuccess,
		GasUsed:        50,
		FundingInputs:  []OutPoint{gasInput},
		RequiresResult: true,
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: gasHash, Index: 1}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "gas"},
		Amount: *mustDefaultDecimal(t, 99960),
	}}, []byte{0x51}))

	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 1, InvokeBaseGas: 20, ResultBaseGas: 10},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, gasInput, contract, gasAssetName, 100000, 1),
			}, nil
		},
		ResolveOutput: func(*wire.MsgTx) ([]ResultOutput, error) {
			return []ResultOutput{
				mustResultOutputWithDecimalAsset(t, contract.MustEncode(), gasAssetName, "99999.96"),
			}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestCanonicalResultVerifierRefundsExplicitInvokeGasEscrowToRecipient(t *testing.T) {
	contract := testContract(t)
	gasAssetName := "ordx:ft:gas"
	refundRecipient := "tb1qrefund"
	gasHash := chainhash.Hash{11}
	gasInput := OutPoint{TxID: gasHash.String(), Vout: 1}
	record := ExecutionRecord{
		Type:               TxTypeInvoke,
		Kind:               ExecutionKindInvoke,
		Contract:           contract,
		Status:             ResultStatusSuccess,
		GasUsed:            50,
		FundingInputs:      []OutPoint{gasInput},
		GasRefundRecipient: refundRecipient,
		RequiresResult:     true,
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: gasHash, Index: 1}, nil, nil))

	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 1, InvokeBaseGas: 20, ResultBaseGas: 10},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, gasInput, contract, gasAssetName, 100000, 1),
			}, nil
		},
		ResolveOutput: func(*wire.MsgTx) ([]ResultOutput, error) {
			return []ResultOutput{
				mustResultOutputWithDecimalAsset(t, refundRecipient, gasAssetName, "99999.96"),
			}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestCanonicalResultVerifierDefaultInvokeKeepsFundingInContractAddress(t *testing.T) {
	contract := testContract(t)
	assetName := "ordx:ft:test"
	fundingHash := chainhash.Hash{10}
	funding := OutPoint{TxID: fundingHash.String(), Vout: 0}
	record := ExecutionRecord{
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         "default-call",
		Contract:       contract,
		Status:         ResultStatusSuccess,
		GasUsed:        20,
		FundingInputs:  []OutPoint{funding},
		RequiresResult: true,
		ResultFeeMode:  ResultFeeModePlainTxFee,
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: fundingHash, Index: 0}, nil, nil))

	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: "ordx:ft:gas", FixedGasPrice: 1, InvokeBaseGas: 20, ResultBaseGas: 10},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXOWithValueAndAsset(t, funding, contract, 100, 1, assetName, 200),
			}, nil
		},
		ResolveOutput: func(*wire.MsgTx) ([]ResultOutput, error) {
			return []ResultOutput{
				mustResultOutputWithDecimalAssetAndValue(t, contract.MustEncode(), 100, assetName, "200"),
			}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestCanonicalResultVerifierTriggerUsesContractGasUTXO(t *testing.T) {
	contract := testContract(t)
	gasAssetName := "ordx:ft:gas"
	gasHash := chainhash.Hash{3}
	assetHash := chainhash.Hash{4}
	gasInput := OutPoint{TxID: gasHash.String(), Vout: 0}
	assetInput := OutPoint{TxID: assetHash.String(), Vout: 0}
	record := ExecutionRecord{
		Kind:           ExecutionKindTrigger,
		CallID:         DeriveTriggerCallID(contract, "vault-release", 100),
		TriggerID:      "vault-release",
		Contract:       contract,
		Status:         ResultStatusSuccess,
		GasUsed:        10,
		RequiresResult: true,
		AssetIntents: []AssetIntent{{
			From:      contract,
			To:        "tb1qdest",
			AssetName: SatoshiAssetName,
			Amount:    mustDefaultDecimal(t, 70),
		}},
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: gasHash, Index: 0}, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: assetHash, Index: 0}, nil, nil))

	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 2, TriggerBaseGas: 10, ResultBaseGas: 5},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, assetInput, contract, SatoshiAssetName, 100, 11),
				mustUTXO(t, gasInput, contract, gasAssetName, 100, 10),
			}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}

func TestBackendWithCanonicalResultVerifier(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	invokeTx := testInvokeTx(t, contract, InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Calldata:  EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	})
	funding := OutPoint{TxID: invokeTx.TxID(), Vout: 1}
	assetHash := chainhash.Hash{8}
	assetInput := OutPoint{TxID: assetHash.String(), Vout: 0}
	gasAssetName := "ordx:ft:gas"
	resultTx := testCanonicalResultTx(t, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
		{Hash: assetHash, Index: 0},
	}, []wire.TxOut{
		{Value: 77, PkScript: []byte{0x51}},
		{Value: 23, Assets: wire.TxAssets{{
			Name:   wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "gas"},
			Amount: *scommon.NewDecimal(99950, 8),
		}}, PkScript: testContractScript(contract)},
	})

	verifier := CanonicalResultVerifier{
		GasConfig: GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 1, ResultPackingFee: 0},
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, funding, contract, gasAssetName, 100000, 10),
				mustUTXO(t, assetInput, contract, SatoshiAssetName, 100, 11),
			}, nil
		},
		ResolveOutput: func(tx *wire.MsgTx) ([]ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(tx, TestnetContractPrefix, evmcommon.ParseContractPkScript, func(pkScript []byte) (string, bool, error) {
				if len(pkScript) == 1 && pkScript[0] == 0x51 {
					return "tb1qdest", true, nil
				}
				return "", false, nil
			})
		},
	}
	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)
	require.NoError(t, VerifyResultTxs(ResultVerifyRequest{
		ResultTxs:    []*wire.MsgTx{resultTx},
		Execution:    executed,
		VerifyResult: verifier.Verify,
	}))
}
