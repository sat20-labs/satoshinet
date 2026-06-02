package evm

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
)

type GasConfig struct {
	GasAssetName string

	GasPriceDenominator      uint64
	InitialGasPriceNumerator uint64
	GasPriceDecayInterval    uint64
	GasPriceDecayNumerator   uint64
	GasPriceDecayDenominator uint64
	GasPriceFloorNumerator   uint64

	// Gas limits and base gas fields are execution gas units, not gas asset
	// amounts. They are converted to gas asset fees through
	// contract/common.ExecutionGasUnitsPerGas and the height-dependent gas price.
	DeployBaseGas   uint64
	InvokeBaseGas   uint64
	ResultBaseGas   uint64
	TriggerBaseGas  uint64
	MaxGasPerInvoke uint64
	MaxGasPerBlock  uint64

	FixedGasPrice uint64
	// ResultPackingFee is also an execution gas unit amount, not a gas asset
	// amount.
	ResultPackingFee uint64
}

func DefaultGasConfig() GasConfig {
	return GasConfig{
		GasAssetName: contractcommon.GasAssetName,

		GasPriceDenominator:      contractcommon.GasPriceDenominator,
		InitialGasPriceNumerator: contractcommon.InitialGasPriceNumerator,
		GasPriceDecayInterval:    contractcommon.GasPriceDecayInterval,
		GasPriceDecayNumerator:   contractcommon.GasPriceDecayNumerator,
		GasPriceDecayDenominator: contractcommon.GasPriceDecayDenominator,
		GasPriceFloorNumerator:   contractcommon.GasPriceFloorNumerator,

		DeployBaseGas:  contractcommon.DeployBaseGas,
		InvokeBaseGas:  contractcommon.InvokeBaseGas,
		ResultBaseGas:  contractcommon.ResultBaseGas,
		TriggerBaseGas: contractcommon.TriggerBaseGas,
		MaxGasPerBlock: contractcommon.MaxGasPerBlock,

		FixedGasPrice: 1,
	}
}

func (c GasConfig) Validate() error {
	if c.GasAssetName == "" {
		return fmt.Errorf("missing gas asset name")
	}
	if c.FixedGasPrice == 0 {
		return fmt.Errorf("fixed gas price must be positive")
	}
	return nil
}

func (c GasConfig) CallFee(gasUsed uint64) uint64 {
	fee, _ := c.CheckedCallFee(gasUsed)
	return fee
}

func (c GasConfig) CheckedCallFee(gasUsed uint64) (uint64, error) {
	fee, err := c.CheckedCallFeeDecimal(gasUsed)
	if err != nil {
		return 0, err
	}
	return contractcommon.DecimalCeilUint64(fee)
}

func (c GasConfig) CheckedCallFeeAtHeight(gasUsed, height uint64) (uint64, error) {
	fee, err := c.CheckedCallFeeDecimalAtHeight(gasUsed, height)
	if err != nil {
		return 0, err
	}
	return contractcommon.DecimalCeilUint64(fee)
}

func (c GasConfig) CheckedCallFeeDecimal(gasUsed uint64) (*scommon.Decimal, error) {
	return c.CheckedCallFeeDecimalAtHeight(gasUsed, 0)
}

func (c GasConfig) CheckedCallFeeDecimalAtHeight(gasUsed, height uint64) (*scommon.Decimal, error) {
	cfg := c.normalized()
	return contractcommon.GasFeeDecimal(gasUsed, cfg.GasPriceNumeratorAtHeight(height), cfg.GasPriceDenominator)
}

func (c GasConfig) CheckedExecutionFee(gasUsed, baseGas, height uint64) (*scommon.Decimal, error) {
	return c.CheckedCallFeeDecimalAtHeight(contractcommon.EffectiveGas(gasUsed, baseGas), height)
}

func (c GasConfig) CheckedResultBaseFee(height uint64) (*scommon.Decimal, error) {
	return c.CheckedCallFeeDecimalAtHeight(c.normalized().ResultBaseGas, height)
}

func (c GasConfig) BaseGasForKind(kind ExecutionKind) uint64 {
	cfg := c.normalized()
	switch kind {
	case ExecutionKindDeploy:
		return cfg.DeployBaseGas
	case ExecutionKindTrigger:
		return cfg.TriggerBaseGas
	default:
		return cfg.InvokeBaseGas
	}
}

func (c GasConfig) ResultExecutionGas(record ExecutionRecord) uint64 {
	baseGas := c.BaseGasForKind(record.Kind)
	switch record.Kind {
	case ExecutionKindDeploy, ExecutionKindInvoke:
		if record.GasUsed <= baseGas {
			return 0
		}
		return record.GasUsed - baseGas
	case ExecutionKindTrigger:
		return contractcommon.EffectiveGas(record.GasUsed, baseGas)
	default:
		return record.GasUsed
	}
}

func (c GasConfig) RequiredInvokeFunding(gasLimit uint64, needsResult bool) uint64 {
	fee, _ := c.CheckedRequiredInvokeFunding(gasLimit, needsResult)
	return fee
}

func (c GasConfig) CheckedRequiredInvokeFunding(gasLimit uint64, needsResult bool) (uint64, error) {
	fee, err := c.CheckedRequiredInvokeFundingDecimal(gasLimit, needsResult)
	if err != nil {
		return 0, err
	}
	return contractcommon.DecimalCeilUint64(fee)
}

func (c GasConfig) RequiredInvokeFundingDecimal(gasLimit uint64, needsResult bool) *scommon.Decimal {
	fee, _ := c.CheckedRequiredInvokeFundingDecimal(gasLimit, needsResult)
	return fee
}

func (c GasConfig) CheckedRequiredInvokeFundingDecimal(gasLimit uint64, needsResult bool) (*scommon.Decimal, error) {
	fee, err := c.CheckedCallFeeDecimalAtHeight(gasLimit, 0)
	if err != nil {
		return nil, err
	}
	if !needsResult {
		return fee, nil
	}
	resultFee, err := c.CheckedResultBaseFee(0)
	if err != nil {
		return nil, err
	}
	return fee.AddAlignPrecision(resultFee), nil
}

func SplitGasFunding(totalGasAsset, callFee, resultPackingFee uint64) (feeToMiner, contractRemainder uint64, err error) {
	required, overflow := addUint64(callFee, resultPackingFee)
	if overflow {
		return 0, 0, errors.New("gas funding requirement overflows uint64")
	}
	if totalGasAsset < required {
		return 0, 0, ErrInsufficientFunds
	}
	return required, totalGasAsset - required, nil
}

func (c GasConfig) GasPriceNumeratorAtHeight(height uint64) uint64 {
	cfg := c.normalized()
	numerator := cfg.InitialGasPriceNumerator
	if cfg.GasPriceDecayInterval == 0 {
		return numerator
	}
	epochs := height / cfg.GasPriceDecayInterval
	for epochs > 0 && numerator > cfg.GasPriceFloorNumerator {
		numerator = numerator * cfg.GasPriceDecayNumerator / cfg.GasPriceDecayDenominator
		if numerator < cfg.GasPriceFloorNumerator {
			return cfg.GasPriceFloorNumerator
		}
		epochs--
	}
	return numerator
}

func (c GasConfig) normalized() GasConfig {
	def := DefaultGasConfig()
	if c.GasAssetName == "" {
		c.GasAssetName = def.GasAssetName
	}
	if c.GasPriceDenominator == 0 {
		if c.FixedGasPrice != 0 && c.FixedGasPrice != def.FixedGasPrice {
			c.GasPriceDenominator = 1
		} else {
			c.GasPriceDenominator = def.GasPriceDenominator
		}
	}
	if c.InitialGasPriceNumerator == 0 {
		if c.FixedGasPrice != 0 && c.FixedGasPrice != def.FixedGasPrice {
			c.InitialGasPriceNumerator = c.FixedGasPrice
		} else {
			c.InitialGasPriceNumerator = def.InitialGasPriceNumerator
		}
	}
	if c.GasPriceDecayInterval == 0 {
		c.GasPriceDecayInterval = def.GasPriceDecayInterval
	}
	if c.GasPriceDecayNumerator == 0 {
		c.GasPriceDecayNumerator = def.GasPriceDecayNumerator
	}
	if c.GasPriceDecayDenominator == 0 {
		c.GasPriceDecayDenominator = def.GasPriceDecayDenominator
	}
	if c.GasPriceFloorNumerator == 0 {
		c.GasPriceFloorNumerator = def.GasPriceFloorNumerator
	}
	if c.DeployBaseGas == 0 {
		c.DeployBaseGas = def.DeployBaseGas
	}
	if c.InvokeBaseGas == 0 {
		c.InvokeBaseGas = def.InvokeBaseGas
	}
	if c.ResultBaseGas == 0 {
		c.ResultBaseGas = c.ResultPackingFee
	}
	if c.ResultBaseGas == 0 {
		c.ResultBaseGas = def.ResultBaseGas
	}
	if c.TriggerBaseGas == 0 {
		c.TriggerBaseGas = def.TriggerBaseGas
	}
	if c.MaxGasPerBlock == 0 {
		c.MaxGasPerBlock = def.MaxGasPerBlock
	}
	if c.FixedGasPrice == 0 {
		c.FixedGasPrice = def.FixedGasPrice
	}
	return c
}
