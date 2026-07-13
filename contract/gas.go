package contract

import (
	"errors"
	"math"
	"math/big"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MainnetGasAssetName = "brc20:f:sgas"
	TestnetGasAssetName = "brc20:f:sgas"

	DeployBaseGas int64 = 5_000_000
	InvokeBaseGas int64 = 100_000
	// EVMDefaultInvokeGas is the fixed execution budget for an EVM default
	// invoke, which carries no OP_RETURN payload through which a caller can
	// select a gas limit. It deliberately does not change template or agent
	// default-invoke gas budgets.
	EVMDefaultInvokeGas int64 = 1_000_000
	ResultBaseGas       int64 = 50_000
	TriggerBaseGas      int64 = 150_000
	MaxGasPerInvoke     int64 = 50_000_000
	MaxGasPerTrigger    int64 = 5_000_000
	MaxGasPerBlock      int64 = 1_000_000_000

	ExecutionGasUnitsPerGas int64 = 1000
	GasFeePrecision         int   = 8

	GasPriceDenominator      uint64 = 10000
	InitialGasPriceNumerator uint64 = GasPriceDenominator
	GasPriceFloorNumerator   uint64 = 1
	GasPriceDecayInterval    uint64 = 100000
	GasPriceDecayNumerator   uint64 = 90
	GasPriceDecayDenominator uint64 = 100
)

// DefaultInvokeGasForType returns the consensus gas budget for a contract
// invocation expressed only by a funding output.  EVM calls need a larger
// fixed budget because their calldata is intentionally empty.
func DefaultInvokeGasForType(contractType byte) int64 {
	if contractType == ContractTypeEVM {
		return EVMDefaultInvokeGas
	}
	return InvokeBaseGas
}

var activeNet wire.BitcoinNet = wire.TestNet

func SetNetworkParam(net wire.BitcoinNet) {
	activeNet = net
}

func GetGasAssetName() string {
	switch activeNet {
	case wire.MainNet:
		return MainnetGasAssetName
	default:
		return TestnetGasAssetName
	}
}

func GasAssetNameForNet(net wire.BitcoinNet) string {
	switch net {
	case wire.MainNet:
		return MainnetGasAssetName
	default:
		return TestnetGasAssetName
	}
}

func GasFeeAtHeight(gas int64, height uint64) (int64, error) {
	fee, err := GasFeeDecimalAtHeight(gas, height)
	if err != nil {
		return 0, err
	}
	return DecimalCeilInt64(fee)
}

func GasFee(gas int64, priceNumerator, priceDenominator uint64) (int64, error) {
	fee, err := GasFeeDecimal(gas, priceNumerator, priceDenominator)
	if err != nil {
		return 0, err
	}
	return DecimalCeilInt64(fee)
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

func GasFeeDecimalAtHeight(gas int64, height uint64) (*indexercommon.Decimal, error) {
	return GasFeeDecimal(gas, GasPriceNumeratorAtHeight(height), GasPriceDenominator)
}

func GasFeeDecimal(gas int64, priceNumerator, priceDenominator uint64) (*indexercommon.Decimal, error) {
	if gas == 0 {
		return indexercommon.NewDecimal(0, GasFeePrecision), nil
	}
	if gas < 0 {
		return nil, errors.New("negative gas")
	}
	if priceNumerator == 0 || priceDenominator == 0 || ExecutionGasUnitsPerGas == 0 {
		return nil, errors.New("invalid gas price")
	}
	value := big.NewInt(gas)
	value.Mul(value, new(big.Int).SetUint64(priceNumerator))
	if GasFeePrecision > 0 {
		value.Mul(value, decimalScale(GasFeePrecision))
	}
	denominator := new(big.Int).SetUint64(priceDenominator)
	denominator.Mul(denominator, big.NewInt(ExecutionGasUnitsPerGas))
	value.Div(value, denominator)
	return &indexercommon.Decimal{Precision: GasFeePrecision, Value: value}, nil
}

func DecimalCeilInt64(d *indexercommon.Decimal) (int64, error) {
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
	if quotient.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return 0, errors.New("gas fee overflows int64")
	}
	return quotient.Int64(), nil
}

func decimalScale(precision int) *big.Int {
	return indexercommon.DecimalScale(precision)
}
