package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
)

func mustResultOutput(t *testing.T, to, assetName string, amount uint64) ResultOutput {
	t.Helper()
	output, err := resultOutputWithAsset(to, assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("result output: %v", err)
	}
	return output
}

func mustUTXO(t *testing.T, outpoint OutPoint, contract ContractAddress, assetName string, amount uint64, height int64) UTXO {
	t.Helper()
	utxo := UTXO{
		OutPoint: outpoint,
		Contract: contract,
		Height:   height,
	}
	if assetName == SatoshiAssetName {
		utxo.Value = amount
		return utxo
	}
	assets, err := NewAssetSet(assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("utxo asset set: %v", err)
	}
	utxo.Assets = assets
	return utxo
}

func mustDefaultDecimal(t *testing.T, amount uint64) *scommon.Decimal {
	t.Helper()
	decimal, err := decimalFromUint64(amount)
	if err != nil {
		t.Fatalf("decimal amount: %v", err)
	}
	return decimal
}
