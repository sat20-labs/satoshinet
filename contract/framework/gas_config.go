package framework

import (
	"fmt"
	"math"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
)

type GasConfig struct {
	GasAssetName     string
	BootstrapAddress string

	GasPriceDenominator      uint64
	InitialGasPriceNumerator uint64
	GasPriceDecayInterval    uint64
	GasPriceDecayNumerator   uint64
	GasPriceDecayDenominator uint64
	GasPriceFloorNumerator   uint64

	DeployBaseGas    int64
	InvokeBaseGas    int64
	ResultBaseGas    int64
	TriggerBaseGas   int64
	MaxGasPerInvoke  int64
	MaxGasPerTrigger int64
	MaxGasPerBlock   int64

	FixedGasPrice    uint64
	ResultPackingFee int64
}

type BaseGasConfig struct {
	DeployBaseGas    int64
	InvokeBaseGas    int64
	ResultBaseGas    int64
	TriggerBaseGas   int64
	MaxGasPerInvoke  int64
	MaxGasPerTrigger int64
}

func DefaultBaseGasConfig() BaseGasConfig {
	cfg := DefaultGasConfig()
	return BaseGasConfig{
		DeployBaseGas:    cfg.DeployBaseGas,
		InvokeBaseGas:    cfg.InvokeBaseGas,
		ResultBaseGas:    cfg.ResultBaseGas,
		TriggerBaseGas:   cfg.TriggerBaseGas,
		MaxGasPerInvoke:  cfg.MaxGasPerInvoke,
		MaxGasPerTrigger: cfg.MaxGasPerTrigger,
	}
}

func NormalizeBaseGasConfig(cfg BaseGasConfig) BaseGasConfig {
	def := DefaultBaseGasConfig()
	if cfg.DeployBaseGas == 0 {
		cfg.DeployBaseGas = def.DeployBaseGas
	}
	if cfg.InvokeBaseGas == 0 {
		cfg.InvokeBaseGas = def.InvokeBaseGas
	}
	if cfg.ResultBaseGas == 0 {
		cfg.ResultBaseGas = def.ResultBaseGas
	}
	if cfg.TriggerBaseGas == 0 {
		cfg.TriggerBaseGas = def.TriggerBaseGas
	}
	if cfg.MaxGasPerInvoke == 0 {
		cfg.MaxGasPerInvoke = def.MaxGasPerInvoke
	}
	if cfg.MaxGasPerTrigger == 0 {
		cfg.MaxGasPerTrigger = def.MaxGasPerTrigger
	}
	return cfg
}

func BaseGasConfigFromFields(deployBaseGas, invokeBaseGas,
	resultBaseGas, triggerBaseGas, maxGasPerInvoke int64) BaseGasConfig {

	return BaseGasConfig{
		DeployBaseGas:    deployBaseGas,
		InvokeBaseGas:    invokeBaseGas,
		ResultBaseGas:    resultBaseGas,
		TriggerBaseGas:   triggerBaseGas,
		MaxGasPerInvoke:  maxGasPerInvoke,
		MaxGasPerTrigger: maxGasPerInvoke,
	}
}

func ApplyBaseGasConfig(cfg GasConfig, base BaseGasConfig) GasConfig {
	if base.DeployBaseGas != 0 {
		cfg.DeployBaseGas = base.DeployBaseGas
	}
	if base.InvokeBaseGas != 0 {
		cfg.InvokeBaseGas = base.InvokeBaseGas
	}
	if base.ResultBaseGas != 0 {
		cfg.ResultBaseGas = base.ResultBaseGas
	}
	if base.TriggerBaseGas != 0 {
		cfg.TriggerBaseGas = base.TriggerBaseGas
	}
	if base.MaxGasPerInvoke != 0 {
		cfg.MaxGasPerInvoke = base.MaxGasPerInvoke
	}
	if base.MaxGasPerTrigger != 0 {
		cfg.MaxGasPerTrigger = base.MaxGasPerTrigger
	}
	return cfg
}

func DefaultGasConfig() GasConfig {
	return GasConfig{
		GasAssetName: contract.GetGasAssetName(),

		GasPriceDenominator:      contract.GasPriceDenominator,
		InitialGasPriceNumerator: contract.InitialGasPriceNumerator,
		GasPriceDecayInterval:    contract.GasPriceDecayInterval,
		GasPriceDecayNumerator:   contract.GasPriceDecayNumerator,
		GasPriceDecayDenominator: contract.GasPriceDecayDenominator,
		GasPriceFloorNumerator:   contract.GasPriceFloorNumerator,

		DeployBaseGas:    int64(contract.DeployBaseGas),
		InvokeBaseGas:    int64(contract.InvokeBaseGas),
		ResultBaseGas:    int64(contract.ResultBaseGas),
		TriggerBaseGas:   int64(contract.TriggerBaseGas),
		MaxGasPerInvoke:  int64(contract.MaxGasPerInvoke),
		MaxGasPerTrigger: int64(contract.MaxGasPerTrigger),
		MaxGasPerBlock:   int64(contract.MaxGasPerBlock),

		FixedGasPrice: 1,
	}
}

func NormalizeGasConfig(cfg GasConfig) GasConfig {
	def := DefaultGasConfig()
	if cfg.GasAssetName == "" {
		cfg.GasAssetName = def.GasAssetName
	}
	if cfg.GasPriceDenominator == 0 {
		if cfg.FixedGasPrice != 0 && cfg.FixedGasPrice != def.FixedGasPrice {
			cfg.GasPriceDenominator = 1
		} else {
			cfg.GasPriceDenominator = def.GasPriceDenominator
		}
	}
	if cfg.InitialGasPriceNumerator == 0 {
		if cfg.FixedGasPrice != 0 && cfg.FixedGasPrice != def.FixedGasPrice {
			cfg.InitialGasPriceNumerator = cfg.FixedGasPrice
		} else {
			cfg.InitialGasPriceNumerator = def.InitialGasPriceNumerator
		}
	}
	if cfg.GasPriceDecayInterval == 0 {
		cfg.GasPriceDecayInterval = def.GasPriceDecayInterval
	}
	if cfg.GasPriceDecayNumerator == 0 {
		cfg.GasPriceDecayNumerator = def.GasPriceDecayNumerator
	}
	if cfg.GasPriceDecayDenominator == 0 {
		cfg.GasPriceDecayDenominator = def.GasPriceDecayDenominator
	}
	if cfg.GasPriceFloorNumerator == 0 {
		cfg.GasPriceFloorNumerator = def.GasPriceFloorNumerator
	}
	if cfg.DeployBaseGas == 0 {
		cfg.DeployBaseGas = def.DeployBaseGas
	}
	if cfg.InvokeBaseGas == 0 {
		cfg.InvokeBaseGas = def.InvokeBaseGas
	}
	if cfg.ResultBaseGas == 0 {
		cfg.ResultBaseGas = cfg.ResultPackingFee
	}
	if cfg.ResultBaseGas == 0 {
		cfg.ResultBaseGas = def.ResultBaseGas
	}
	if cfg.TriggerBaseGas == 0 {
		cfg.TriggerBaseGas = def.TriggerBaseGas
	}
	if cfg.MaxGasPerInvoke == 0 {
		cfg.MaxGasPerInvoke = def.MaxGasPerInvoke
	}
	if cfg.MaxGasPerTrigger == 0 {
		cfg.MaxGasPerTrigger = def.MaxGasPerTrigger
	}
	if cfg.MaxGasPerBlock == 0 {
		cfg.MaxGasPerBlock = def.MaxGasPerBlock
	}
	if cfg.FixedGasPrice == 0 {
		cfg.FixedGasPrice = def.FixedGasPrice
	}
	return cfg
}

func (c GasConfig) Normalize() GasConfig {
	return NormalizeGasConfig(c)
}

func (c GasConfig) Validate() error {
	if c.Normalize().GasAssetName == "" {
		return fmt.Errorf("missing gas asset name")
	}
	if c.Normalize().FixedGasPrice == 0 {
		return fmt.Errorf("fixed gas price must be positive")
	}
	return nil
}

func (c GasConfig) DeployFee(height int64) (*scommon.Decimal, error) {
	return gasFee(c.Normalize().DeployBaseGas, height)
}

func (c GasConfig) InvokeFee(height int64) (*scommon.Decimal, error) {
	return gasFee(c.Normalize().InvokeBaseGas, height)
}

func (c GasConfig) ResultFee(height int64) (*scommon.Decimal, error) {
	return gasFee(c.Normalize().ResultBaseGas, height)
}

func (c GasConfig) CheckedCallFeeDecimal(gasUsed int64) (*scommon.Decimal, error) {
	return c.CheckedCallFeeDecimalAtHeight(gasUsed, 0)
}

func (c GasConfig) CheckedCallFeeDecimalAtHeight(gasUsed int64, height uint64) (*scommon.Decimal, error) {
	cfg := c.Normalize()
	return contract.GasFeeDecimal(gasUsed, cfg.GasPriceNumeratorAtHeight(height), cfg.GasPriceDenominator)
}

func (c GasConfig) CheckedExecutionFee(gasUsed, baseGas int64, height uint64) (*scommon.Decimal, error) {
	effective := gasUsed - baseGas
	if effective < 0 {
		effective = 0
	}
	return c.CheckedCallFeeDecimalAtHeight(effective, height)
}

func (c GasConfig) CheckedResultBaseFee(height uint64) (*scommon.Decimal, error) {
	return c.CheckedCallFeeDecimalAtHeight(c.Normalize().ResultBaseGas, height)
}

func (c GasConfig) BaseGasForKind(kind ExecutionKind) int64 {
	cfg := c.Normalize()
	switch kind {
	case ExecutionKindDeploy:
		return cfg.DeployBaseGas
	case ExecutionKindTrigger:
		return cfg.TriggerBaseGas
	default:
		return cfg.InvokeBaseGas
	}
}

func (c GasConfig) BaseNetworkFee(kind ExecutionKind, height uint64) (*scommon.Decimal, error) {
	return c.CheckedCallFeeDecimalAtHeight(c.BaseGasForKind(kind), height)
}

func (c GasConfig) ExecutionEscrowFee(kind ExecutionKind, gasLimit int64, height uint64) (*scommon.Decimal, error) {
	baseGas := c.BaseGasForKind(kind)
	if kind == ExecutionKindTrigger {
		return c.CheckedCallFeeDecimalAtHeight(gasLimit, height)
	}
	if gasLimit < baseGas {
		return nil, fmt.Errorf("gas limit %d below base gas %d", gasLimit, baseGas)
	}
	return c.CheckedCallFeeDecimalAtHeight(gasLimit-baseGas, height)
}

func (c GasConfig) ResultBaseEscrowFee(height uint64) (*scommon.Decimal, error) {
	return c.CheckedResultBaseFee(height)
}

func (c GasConfig) TotalUserBudgetFee(kind ExecutionKind, gasLimit int64, needsResult bool, height uint64) (*scommon.Decimal, error) {
	baseFee, err := c.BaseNetworkFee(kind, height)
	if err != nil {
		return nil, err
	}
	escrowFee, err := c.ContractFundingFee(kind, gasLimit, needsResult, height)
	if err != nil {
		return nil, err
	}
	return baseFee.AddAlignPrecision(escrowFee), nil
}

func (c GasConfig) ContractFundingFee(kind ExecutionKind, gasLimit int64, needsResult bool, height uint64) (*scommon.Decimal, error) {
	fee, err := c.ExecutionEscrowFee(kind, gasLimit, height)
	if err != nil {
		return nil, err
	}
	if !needsResult {
		return fee, nil
	}
	resultFee, err := c.ResultBaseEscrowFee(height)
	if err != nil {
		return nil, err
	}
	return fee.AddAlignPrecision(resultFee), nil
}

func (c GasConfig) RequiredInvokeFundingDecimal(gasLimit int64, needsResult bool) *scommon.Decimal {
	fee, _ := c.CheckedRequiredInvokeFundingDecimal(gasLimit, needsResult)
	return fee
}

func (c GasConfig) CheckedRequiredInvokeFundingDecimal(gasLimit int64, needsResult bool) (*scommon.Decimal, error) {
	return c.TotalUserBudgetFee(ExecutionKindInvoke, gasLimit, needsResult, 0)
}

func (c GasConfig) GasPriceNumeratorAtHeight(height uint64) uint64 {
	cfg := c.Normalize()
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

func ValidateDeployGasLimit(gasLimit int64, cfg GasConfig) error {
	if gasLimit == 0 {
		return fmt.Errorf("deploy gas limit is zero")
	}
	normalized := cfg.Normalize()
	if gasLimit < normalized.DeployBaseGas {
		return fmt.Errorf("deploy gas limit below deploy base gas")
	}
	if normalized.MaxGasPerInvoke > 0 && gasLimit > normalized.MaxGasPerInvoke {
		return fmt.Errorf("deploy gas limit exceeds maximum")
	}
	return nil
}

func ValidateInvokeGasLimit(gasLimit int64, cfg GasConfig) error {
	if gasLimit == 0 {
		return fmt.Errorf("invoke gas limit is zero")
	}
	normalized := cfg.Normalize()
	if gasLimit < normalized.InvokeBaseGas {
		return fmt.Errorf("invoke gas limit below invoke base gas")
	}
	if normalized.MaxGasPerInvoke > 0 && gasLimit > normalized.MaxGasPerInvoke {
		return fmt.Errorf("invoke gas limit exceeds maximum")
	}
	return nil
}

func ValidateTriggerGasLimit(gasLimit int64, cfg GasConfig) error {
	if gasLimit <= 0 {
		return fmt.Errorf("trigger gas limit must be positive")
	}
	normalized := cfg.Normalize()
	if normalized.MaxGasPerTrigger > 0 && gasLimit > normalized.MaxGasPerTrigger {
		return fmt.Errorf("trigger gas limit exceeds maximum")
	}
	return nil
}

func gasFee(gas int64, height int64) (*scommon.Decimal, error) {
	if height < 0 {
		height = 0
	}
	return contract.GasFeeDecimalAtHeight(gas, uint64(height))
}

func GasUnitsUint64(gas int64) (uint64, error) {
	if gas < 0 {
		return 0, fmt.Errorf("gas units are negative")
	}
	return uint64(gas), nil
}

func GasUnitsInt64(gas uint64) (int64, error) {
	if gas > uint64(math.MaxInt64) {
		return 0, fmt.Errorf("gas units overflow int64")
	}
	return int64(gas), nil
}
