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
	DeployBaseGas  uint64 = 5_000_000
	InvokeBaseGas  uint64 = 100_000
	ResultBaseGas  uint64 = 50_000
	TriggerBaseGas uint64 = 150_000
	MaxGasPerBlock uint64 = 1_000_000_000

	// 
	// DeployBaseGas_Template	uint64 = DeployBaseGas/5
	// DeployBaseGas_Agent		uint64 = DeployBaseGas/2
	// InvokeBaseGas_Agent 		uint64 = InvokeBaseGas*5
	// TriggerBaseGas_Agent    	uint64 = InvokeBaseGas_Agent + ResultBaseGas

	// ExecutionGasUnitsPerGas decouples EVM/template/agent execution gas units
	// from the protocol gas asset unit.
	ExecutionGasUnitsPerGas uint64 = 1000
	GasFeePrecision         int    = 8

	// 每 GasPriceDecayInterval 个区块调整一次gas
	// 调整为 Numerator 当前的 GasPriceDecayNumerator/GasPriceDecayDenominator
	// 直到分子下降到 GasPriceFloorNumerator
	GasPriceDenominator      uint64 = 10000 // 分母
	InitialGasPriceNumerator uint64 = GasPriceDenominator // 分子
	GasPriceFloorNumerator   uint64 = 1
	GasPriceDecayInterval    uint64 = 100000 // in blocks
	GasPriceDecayNumerator   uint64 = 90
	GasPriceDecayDenominator uint64 = 100
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
	return scommon.DecimalScale(precision)
}

func EffectiveGas(used, base uint64) uint64 {
	if used < base {
		return base
	}
	return used
}
