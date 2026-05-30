package common

import (
	"errors"
	"math/bits"
)

const (
	GasAssetName = "brc20:f:ooxx"

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
