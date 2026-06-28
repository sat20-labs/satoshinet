package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
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
	if assetName == SatoshiAssetName {
		return contractframework.UTXOFromTxOutput(outpoint, contract, height, &wire.TxOut{Value: int64(amount)})
	}
	assets, err := NewAssetSet(assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("utxo asset set: %v", err)
	}
	return contractframework.UTXOFromTxOutput(outpoint, contract, height, &wire.TxOut{Assets: assets})
}

func mustUTXOWithValueAndAsset(t *testing.T, outpoint OutPoint, contract ContractAddress, value uint64, height int64, assetName string, amount uint64) UTXO {
	t.Helper()
	assets, err := NewAssetSet(assetName, mustDefaultDecimal(t, amount))
	if err != nil {
		t.Fatalf("utxo asset set: %v", err)
	}
	return contractframework.UTXOFromTxOutput(outpoint, contract, height, &wire.TxOut{
		Value:  int64(value),
		Assets: assets,
	})
}

func mustDecimalUTXO(t *testing.T, outpoint OutPoint, contract ContractAddress, assetName, amount string, height int64) UTXO {
	t.Helper()
	assets, err := NewAssetSet(assetName, mustDecimalString(t, amount))
	if err != nil {
		t.Fatalf("utxo asset set: %v", err)
	}
	return contractframework.UTXOFromTxOutput(outpoint, contract, height, &wire.TxOut{Assets: assets})
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
