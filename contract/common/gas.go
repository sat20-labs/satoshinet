package common

import (
	"errors"
	"math"
	"math/big"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MainnetGasAssetName = "brc20:f:sgas"
	TestnetGasAssetName = "brc20:f:sgas"

	// GasAssetName is the default gas asset for code paths that do not have
	// network context. Consensus and block result construction should use
	// GasAssetNameAtHeight when network context is available.
	GasAssetName = TestnetGasAssetName

	// Base gas values are execution gas units, not gas asset amounts. The gas
	// asset fee is calculated as:
	//   executionGas * priceNumerator / priceDenominator / ExecutionGasUnitsPerGas
	// At the initial price, 1000 execution gas units charge 1 gas asset unit.
	DeployBaseGas  uint64 = 100000
	InvokeBaseGas  uint64 = 20000
	ResultBaseGas  uint64 = 10000
	TriggerBaseGas uint64 = 30000
	MaxGasPerBlock uint64 = 30000000

	// ExecutionGasUnitsPerGas decouples EVM/template/agent execution gas units
	// from the protocol gas asset unit.
	ExecutionGasUnitsPerGas uint64 = 1000
	GasFeePrecision         int    = 8

	GasPriceDenominator      uint64 = 100000000
	InitialGasPriceNumerator uint64 = GasPriceDenominator
	GasPriceDecayInterval    uint64 = 100000 // in blocks
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
	fee, err := GasFeeDecimalAtHeight(gas, height)
	if err != nil {
		return 0, err
	}
	return DecimalCeilUint64(fee)
}

func GasFee(gas, priceNumerator, priceDenominator uint64) (uint64, error) {
	fee, err := GasFeeDecimal(gas, priceNumerator, priceDenominator)
	if err != nil {
		return 0, err
	}
	return DecimalCeilUint64(fee)
}

func GasFeeDecimalAtHeight(gas, height uint64) (*scommon.Decimal, error) {
	return GasFeeDecimal(gas, GasPriceNumeratorAtHeight(height), GasPriceDenominator)
}

func GasFeeDecimal(gas, priceNumerator, priceDenominator uint64) (*scommon.Decimal, error) {
	if gas == 0 {
		return scommon.NewDecimal(0, GasFeePrecision), nil
	}
	if priceNumerator == 0 || priceDenominator == 0 || ExecutionGasUnitsPerGas == 0 {
		return nil, errors.New("invalid gas price")
	}
	value := new(big.Int).SetUint64(gas)
	value.Mul(value, new(big.Int).SetUint64(priceNumerator))
	if GasFeePrecision > 0 {
		value.Mul(value, decimalScale(GasFeePrecision))
	}
	denominator := new(big.Int).SetUint64(priceDenominator)
	denominator.Mul(denominator, new(big.Int).SetUint64(ExecutionGasUnitsPerGas))
	value.Div(value, denominator)
	return &scommon.Decimal{Precision: GasFeePrecision, Value: value}, nil
}

func DecimalCeilUint64(d *scommon.Decimal) (uint64, error) {
	if d == nil || d.Sign() == 0 {
		return 0, nil
	}
	if d.Sign() < 0 {
		return 0, errors.New("negative gas fee")
	}
	scale := decimalScale(d.Precision)
	quotient, remainder := new(big.Int).QuoRem(d.Value, scale, new(big.Int))
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if quotient.Cmp(new(big.Int).SetUint64(math.MaxUint64)) > 0 {
		return 0, errors.New("gas fee overflows uint64")
	}
	return quotient.Uint64(), nil
}

func decimalScale(precision int) *big.Int {
	if precision <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
}

func EffectiveGas(used, base uint64) uint64 {
	if used < base {
		return base
	}
	return used
}
