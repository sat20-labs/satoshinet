package common

import (
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestSelectFundingUTXOsCombinesAssetAndValue(t *testing.T) {
	gasName := *wire.NewAssetNameFromString(GasAssetName)
	changeScript := []byte{0x51, 0x20, 0x01}
	utxos := []FundingUTXO{
		testFundingUTXO(1, 0, 5, nil, changeScript),
		testFundingUTXO(2, 0, 4, wire.TxAssets{{
			Name:   gasName,
			Amount: *indexercommon.NewDefaultDecimal(6),
		}}, changeScript),
		testFundingUTXO(3, 0, 8, wire.TxAssets{{
			Name:   gasName,
			Amount: *indexercommon.NewDefaultDecimal(7),
		}}, changeScript),
	}

	selection, err := SelectFundingUTXOs(FundingSelectionRequest{
		Available:      utxos,
		RequiredValue:  10,
		RequiredAssets: wire.TxAssets{{Name: gasName, Amount: *indexercommon.NewDefaultDecimal(10)}},
		ChangePkScript: changeScript,
	})
	if err != nil {
		t.Fatalf("SelectFundingUTXOs failed: %v", err)
	}
	if len(selection.Inputs) != 2 {
		t.Fatalf("input count mismatch: got %d", len(selection.Inputs))
	}
	if selection.ChangeOutput == nil || selection.ChangeOutput.Value != 2 {
		t.Fatalf("change value mismatch: %#v", selection.ChangeOutput)
	}
	asset, err := selection.ChangeOutput.Assets.Find(&gasName)
	if err != nil {
		t.Fatalf("missing gas change: %v", err)
	}
	if asset.Amount.Int64() != 3 {
		t.Fatalf("gas change mismatch: got %d", asset.Amount.Int64())
	}
}

func TestSelectFundingUTXOsFiltersScriptAndSpendable(t *testing.T) {
	gasName := *wire.NewAssetNameFromString(GasAssetName)
	changeScript := []byte{0x51, 0x20, 0x01}
	wrongScript := []byte{0x51, 0x20, 0x02}
	utxos := []FundingUTXO{
		testFundingUTXO(1, 0, 20, wire.TxAssets{{Name: gasName, Amount: *indexercommon.NewDefaultDecimal(20)}}, wrongScript),
		testFundingUTXO(2, 1, 20, wire.TxAssets{{Name: gasName, Amount: *indexercommon.NewDefaultDecimal(20)}}, changeScript),
		testFundingUTXO(3, 0, 20, wire.TxAssets{{Name: gasName, Amount: *indexercommon.NewDefaultDecimal(20)}}, changeScript),
	}

	selection, err := SelectFundingUTXOs(FundingSelectionRequest{
		Available:        utxos,
		RequiredValue:    10,
		RequiredAssets:   wire.TxAssets{{Name: gasName, Amount: *indexercommon.NewDefaultDecimal(10)}},
		ChangePkScript:   changeScript,
		RequiredPkScript: changeScript,
		IsSpendable: func(utxo FundingUTXO) bool {
			return utxo.OutPoint.Index != 0
		},
	})
	if err != nil {
		t.Fatalf("SelectFundingUTXOs failed: %v", err)
	}
	if len(selection.Inputs) != 1 || selection.Inputs[0].OutPoint.Index != 1 {
		t.Fatalf("unexpected selected inputs: %#v", selection.Inputs)
	}
}

func testFundingUTXO(seed byte, index uint32, value int64, assets wire.TxAssets, pkScript []byte) FundingUTXO {
	hash := chainhash.Hash{seed}
	return FundingUTXO{
		OutPoint: wire.OutPoint{Hash: hash, Index: index},
		OutValue: wire.TxOut{
			Value:    value,
			Assets:   assets,
			PkScript: pkScript,
		},
		Height:  int64(seed),
		SortKey: hash.String(),
	}
}
