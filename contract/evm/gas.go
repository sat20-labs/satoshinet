package evm

import (
	"errors"
	"fmt"
	"math/bits"
)

type GasConfig struct {
	GasAssetName          string
	FixedGasPrice         uint64
	ResultPackingFee      uint64
	TriggerPackingFee     uint64
	MaxGasPerInvoke       uint64
	MaxGasPerBlock        uint64
	ContractCallBaseGas   uint64
	ContractDeployBaseGas uint64
}

func DefaultGasConfig() GasConfig {
	return GasConfig{
		GasAssetName:     "ordx:f:gas",
		FixedGasPrice:    1,
		ResultPackingFee: 0,
		MaxGasPerBlock:   30000000,
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
	hi, lo := bits.Mul64(gasUsed, c.FixedGasPrice)
	if hi != 0 {
		return 0, errors.New("gas fee overflows uint64")
	}
	return lo, nil
}

func (c GasConfig) RequiredInvokeFunding(gasLimit uint64, needsResult bool) uint64 {
	fee, _ := c.CheckedRequiredInvokeFunding(gasLimit, needsResult)
	return fee
}

func (c GasConfig) CheckedRequiredInvokeFunding(gasLimit uint64, needsResult bool) (uint64, error) {
	fee, err := c.CheckedCallFee(gasLimit)
	if err != nil {
		return 0, err
	}
	if needsResult {
		next, overflow := addUint64(fee, c.ResultPackingFee)
		if overflow {
			return 0, errors.New("invoke funding overflows uint64")
		}
		fee = next
	}
	return fee, nil
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
