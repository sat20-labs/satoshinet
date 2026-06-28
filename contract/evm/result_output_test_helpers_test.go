package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func mustResultOutput(t *testing.T, to, assetName string, amount uint64) ResultOutput {
	t.Helper()
	output, err := contractframework.ResultOutputWithAsset(to, assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("result output: %v", err)
	}
	return output
}

func mustResultOutputWithDecimalAsset(t *testing.T, to, assetName, amount string) ResultOutput {
	t.Helper()
	decimal, err := scommon.NewDecimalFromString(amount, contractGasPrecisionForTest())
	if err != nil {
		t.Fatalf("decimal amount: %v", err)
	}
	output, err := contractframework.ResultOutputWithAsset(to, assetName, decimal)
	if err != nil {
		t.Fatalf("result output: %v", err)
	}
	return output
}

func mustResultOutputWithDecimalAssetAndValue(t *testing.T, to string, value int64, assetName, amount string) ResultOutput {
	t.Helper()
	output := mustResultOutputWithDecimalAsset(t, to, assetName, amount)
	output.Value = value
	return output
}

func contractGasPrecisionForTest() int {
	return 18
}

func mustUTXO(t *testing.T, outpoint OutPoint, contract ContractAddress, assetName string, amount uint64, height int64) UTXO {
	t.Helper()
	utxo := UTXO{
		OutPoint: outpoint,
		Contract: contract,
		Height:   height,
	}
	if assetName == SatoshiAssetName {
		utxo.Value = int64(amount)
		return utxo
	}
	assets, err := NewAssetSet(assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("utxo asset set: %v", err)
	}
	utxo.Assets = assets
	return utxo
}

func mustUTXOWithValueAndAsset(t *testing.T, outpoint OutPoint, contract ContractAddress, value uint64, height int64, assetName string, amount uint64) UTXO {
	t.Helper()
	utxo := mustUTXO(t, outpoint, contract, SatoshiAssetName, value, height)
	assets, err := NewAssetSet(assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("utxo asset set: %v", err)
	}
	utxo.Assets = assets
	return utxo
}

func mustDecimalUTXO(t *testing.T, outpoint OutPoint, contract ContractAddress, assetName, amount string, height int64) UTXO {
	t.Helper()
	utxo := UTXO{
		OutPoint: outpoint,
		Contract: contract,
		Height:   height,
	}
	assets, err := NewAssetSet(assetName, mustDecimalString(t, amount))
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

func mustDecimalString(t *testing.T, amount string) *scommon.Decimal {
	t.Helper()
	decimal, err := scommon.NewDecimalFromString(amount, contractGasPrecisionForTest())
	if err != nil {
		t.Fatalf("decimal amount: %v", err)
	}
	return decimal
}
