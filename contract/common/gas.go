package common

import (
	"errors"
	"math/bits"

	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MainnetGasAssetName = "brc20:f:sgas"
	TestnetGasAssetName = "brc20:f:sgas"

	// GasAssetName is the default gas asset for code paths that do not have
	// network context. Consensus and block result construction should use
	// GasAssetNameAtHeight when network context is available.
	GasAssetName = TestnetGasAssetName

	DeployBaseGas  uint64 = 100000
	InvokeBaseGas  uint64 = 20000
	ResultBaseGas  uint64 = 10000
	TriggerBaseGas uint64 = 30000
	MaxGasPerBlock uint64 = 30000000

	GasPriceDenominator      uint64 = 100000000
	InitialGasPriceNumerator uint64 = GasPriceDenominator
	GasPriceDecayInterval    uint64 = 100000
	GasPriceDecayNumerator   uint64 = 95
	GasPriceDecayDenominator uint64 = 100
	GasPriceFloorNumerator   uint64 = 10000
)

func GasAssetNameAtHeight(net wire.BitcoinNet, height int64) string {
	return GasAssetNameForNet(net)
}

func GasAssetNameForNet(net wire.BitcoinNet) string {
	switch net {
	case wire.MainNet:
		return MainnetGasAssetName
	default:
		return TestnetGasAssetName
	}
}

func GasPriceNumeratorAtHeight(height uint64) uint64 {
	numerator := InitialGasPriceNumerator
	if GasPriceDecayInterval == 0 {
		return numerator
	}
	epochs := height / GasPriceDecayInterval
	for epochs > 0 && numerator > GasPriceFloorNumerator {
		numerator = numerator * GasPriceDecayNumerator / GasPriceDecayDenominator
		if numerator < GasPriceFloorNumerator {
			return GasPriceFloorNumerator
		}
		epochs--
	}
	return numerator
}

func GasFeeAtHeight(gas, height uint64) (uint64, error) {
	return GasFee(gas, GasPriceNumeratorAtHeight(height), GasPriceDenominator)
}

func GasFee(gas, priceNumerator, priceDenominator uint64) (uint64, error) {
	if gas == 0 {
		return 0, nil
	}
	if priceNumerator == 0 || priceDenominator == 0 {
		return 0, errors.New("invalid gas price")
	}
	hi, lo := bits.Mul64(gas, priceNumerator)
	if hi >= priceDenominator {
		return 0, errors.New("gas fee overflows uint64")
	}
	quotient, remainder := bits.Div64(hi, lo, priceDenominator)
	if remainder != 0 {
		if quotient == ^uint64(0) {
			return 0, errors.New("gas fee overflows uint64")
		}
		quotient++
	}
	return quotient, nil
}

func EffectiveGas(used, base uint64) uint64 {
	if used < base {
		return base
	}
	return used
}
