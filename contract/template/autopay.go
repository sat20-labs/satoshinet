package template

import (
	"fmt"
	"math/big"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	AutopayScheduleFixed  = contractcommon.AutopayScheduleFixed
	AutopayScheduleLinear = contractcommon.AutopayScheduleLinear

	AutopayStatusFunding = "funding"
	AutopayStatusActive  = "active"
	AutopayStatusExpired = "expired"
	AutopayStatusClosed  = "closed"

	AutopayReasonPayment  = "autopay"
	AutopayReasonMinerFee = "miner_fee"
)

type AutopayContract struct {
	Recipient    string `json:"recipient"`
	FeeAssetName string `json:"feeAssetName"`
	ScheduleMode string `json:"scheduleMode"`
	BaseAmount   string `json:"baseAmount"`
	StepAmount   string `json:"stepAmount,omitempty"`
	EndHeight    int64  `json:"endHeight,omitempty"`
}

func NewAutopayContract(recipient, feeAssetName, scheduleMode, baseAmount, stepAmount string, endHeight int64) *AutopayContract {
	return &AutopayContract{
		Recipient:    recipient,
		FeeAssetName: feeAssetName,
		ScheduleMode: scheduleMode,
		BaseAmount:   baseAmount,
		StepAmount:   stepAmount,
		EndHeight:    endHeight,
	}
}

func (c *AutopayContract) TemplateName() string {
	return TemplateAutopay
}

func (c *AutopayContract) NetworkExclusive() bool {
	return true
}

func (c *AutopayContract) Version() uint32 {
	return CurrentTemplateVersion
}

func (c *AutopayContract) Encode() ([]byte, error) {
	if err := c.CheckContent(); err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(c.Recipient)).
		AddData([]byte(c.FeeAssetName)).
		AddData([]byte(c.ScheduleMode)).
		AddData([]byte(c.BaseAmount)).
		AddData([]byte(c.StepAmount)).
		AddInt64(c.EndHeight).
		Script()
}

func (c *AutopayContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing recipient")
	}
	c.Recipient = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing fee asset name")
	}
	c.FeeAssetName = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing schedule mode")
	}
	c.ScheduleMode = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing base amount")
	}
	c.BaseAmount = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing step amount")
	}
	c.StepAmount = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing end height")
	}
	c.EndHeight = tokenizer.ExtractInt64()
	return tokenizer.Err()
}

func (c *AutopayContract) CheckContent() error {
	return (contractcommon.TemplateAutopayContract{
		Recipient:    c.Recipient,
		FeeAssetName: c.FeeAssetName,
		ScheduleMode: c.ScheduleMode,
		BaseAmount:   c.BaseAmount,
		StepAmount:   c.StepAmount,
		EndHeight:    c.EndHeight,
	}).Check()
}

func (c *AutopayContract) CheckInvoke(action string, param []byte) error {
	switch action {
	case InvokeAPIClose:
		return (&CloseInvokeParam{}).Decode(param)
	default:
		return fmt.Errorf("unsupported autopay action %s", action)
	}
}

func (c *AutopayContract) ApplyFundingState(state *TemplateRuntimeState, output ContractOutput, gasAssetName string) (bool, error) {
	if state == nil {
		return true, nil
	}
	if err := c.addFunding(state, output, gasAssetName); err != nil {
		return true, err
	}
	return true, nil
}

func (c *AutopayContract) ApplyGasFundingState(state *TemplateRuntimeState, output ContractOutput, gasAssetName string) (bool, error) {
	if state == nil || gasAssetName == "" {
		return true, nil
	}
	if c.FeeAssetName == gasAssetName {
		return true, nil
	}
	gas, err := output.AssetAmount(gasAssetName)
	if err != nil {
		return true, err
	}
	state.Running.GasBalance = decimalAddAllowNil(state.Running.GasBalance, gas)
	return true, nil
}

func (c *AutopayContract) ApplyRunningData(running *RunningData, item *InvokeItem) bool {
	if running == nil || item == nil {
		return true
	}
	if item.Reason == InvokeReasonInvalid {
		return true
	}
	switch item.OrderType {
	case OrderTypeFund:
		running.FeeBalance = decimalAddAllowNil(running.FeeBalance, item.InAmt)
	case OrderTypeClose:
	default:
		running.Apply(item)
	}
	return true
}

func (c *AutopayContract) addFunding(state *TemplateRuntimeState, output ContractOutput, gasAssetName string) error {
	fee, err := output.AssetAmount(c.FeeAssetName)
	if err != nil {
		return err
	}
	state.Running.FeeBalance = decimalAddAllowNil(state.Running.FeeBalance, fee)
	if gasAssetName != "" && gasAssetName != c.FeeAssetName {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return err
		}
		state.Running.GasBalance = decimalAddAllowNil(state.Running.GasBalance, gas)
	}
	if state.Running.AutopayStatus == "" {
		state.Running.AutopayStatus = AutopayStatusFunding
	}
	return nil
}

func (c *AutopayContract) settleAutopay(runtime *ContractRuntime, state *TemplateRuntimeState,
	height int64, gasConfig GasConfig) (*SettlementPlan, error) {

	addr := runtime.Address()
	plan := &SettlementPlan{Contract: addr.EncodeAddress(), Height: height}
	if state == nil {
		return plan, nil
	}
	if c.applyAutopayClose(runtime, state, plan, height, gasConfig) {
		return plan, nil
	}
	if state.Running.Closed || state.Running.AutopayStatus == AutopayStatusClosed ||
		state.Running.AutopayStatus == AutopayStatusExpired {
		return plan, nil
	}

	gasConfig = gasConfig.Normalize()
	triggerGasFee, err := gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, uint64(height))
	if err != nil {
		return nil, err
	}
	if err := c.rebalanceGasReserve(state, gasConfig, height); err != nil {
		return nil, err
	}
	next := state.Running.NextPayHeight
	if next == 0 {
		state.Running.ActiveHeight = height
		state.Running.NextPayHeight = height + 1
		next = state.Running.NextPayHeight
	}
	if c.EndHeight > 0 && next > c.EndHeight {
		state.Running.AutopayStatus = AutopayStatusExpired
		return plan, nil
	}
	if height < next {
		state.Running.AutopayStatus = c.autopayFundingStatus(state, gasConfig, height)
		return plan, nil
	}
	fee := c.feeAmountForHeight(state.Running.ActiveHeight, height)
	if !c.hasFeeAndGas(state, fee, triggerGasFee) {
		state.Running.AutopayStatus = AutopayStatusFunding
		return plan, nil
	}
	if err := c.deductFeeBalance(state, fee); err != nil {
		return nil, err
	}
	state.Running.GasBalance = decimalSubAllowNil(state.Running.GasBalance, triggerGasFee)
	plan.GasFee = triggerGasFee.Clone()
	plan.Transfers = append(plan.Transfers, c.autopayPaymentTransfer(fee))
	state.Running.PaidBlockCount++
	state.Running.LastPayHeight = height
	state.Running.NextPayHeight = height + 1
	if c.EndHeight > 0 && state.Running.NextPayHeight > c.EndHeight {
		state.Running.AutopayStatus = AutopayStatusExpired
	} else {
		state.Running.AutopayStatus = c.autopayFundingStatus(state, gasConfig, height)
	}
	return plan, nil
}

func (c *AutopayContract) applyAutopayClose(runtime *ContractRuntime, state *TemplateRuntimeState,
	plan *SettlementPlan, height int64, gasConfig GasConfig) bool {

	if state == nil || plan == nil || state.Running.Closed {
		return false
	}
	deployer := runtime.RuntimeBase().Deployer()
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal ||
			item.OrderType != OrderTypeClose || item.Height > height {
			continue
		}
		addSettlementInputs(plan, item)
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		if item.Address != deployer {
			item.Reason = InvokeReasonInvalid
			item.Done = ItemStatusClosedDirectly
			return true
		}
		gasAssetName := runtimeGasAssetName(gasConfig)
		if c.FeeAssetName == gasAssetName {
			c.appendBalanceTransfer(plan, deployer, c.FeeAssetName,
				decimalAddAllowNil(state.Running.FeeBalance, state.Running.GasBalance), false)
		} else {
			c.appendBalanceTransfer(plan, deployer, c.FeeAssetName, state.Running.FeeBalance, false)
			c.appendBalanceTransfer(plan, deployer, gasAssetName, state.Running.GasBalance, true)
		}
		state.Running.FeeBalance = nil
		state.Running.GasBalance = nil
		state.Running.Closed = true
		state.Running.AutopayStatus = AutopayStatusClosed
		item.Done = ItemStatusDealt
		return true
	}
	return false
}

func (c *AutopayContract) appendBalanceTransfer(plan *SettlementPlan, to, assetName string,
	amount *scommon.Decimal, gas bool) {

	if plan == nil || to == "" || assetName == "" || amount == nil || amount.Sign() <= 0 {
		return
	}
	if c.FeeAssetName == assetName && gas {
		return
	}
	plan.Transfers = append(plan.Transfers, autopayTransfer(to, assetName, amount))
}

func (c *AutopayContract) autopayFundingStatus(state *TemplateRuntimeState, gasConfig GasConfig, height int64) string {
	if state == nil {
		return AutopayStatusFunding
	}
	next := state.Running.NextPayHeight
	if next == 0 {
		next = height + 1
	}
	if c.EndHeight > 0 && next > c.EndHeight {
		return AutopayStatusExpired
	}
	fee, err := c.requiredFeeReserve(state, height)
	if err != nil {
		return AutopayStatusFunding
	}
	gasFee, err := c.requiredGasReserve(state, gasConfig, height)
	if err != nil {
		return AutopayStatusFunding
	}
	if c.hasFeeAndGas(state, fee, gasFee) {
		return AutopayStatusActive
	}
	return AutopayStatusFunding
}

func (c *AutopayContract) rebalanceGasReserve(state *TemplateRuntimeState, gasConfig GasConfig, height int64) error {
	if state == nil || c.FeeAssetName != gasConfig.GasAssetName {
		return nil
	}
	needed, err := c.requiredGasReserve(state, gasConfig, height)
	if err != nil {
		return err
	}
	current := decimalOrZero(state.Running.GasBalance)
	if current.Cmp(needed) >= 0 {
		return nil
	}
	missing := needed.SubAlignPrecision(current)
	feeBalance := decimalOrZero(state.Running.FeeBalance)
	if feeBalance.Sign() <= 0 {
		return nil
	}
	if feeBalance.Cmp(missing) < 0 {
		missing = feeBalance.Clone()
	}
	state.Running.FeeBalance = feeBalance.SubAlignPrecision(missing)
	state.Running.GasBalance = decimalAddAllowNil(state.Running.GasBalance, missing)
	return nil
}

func (c *AutopayContract) requiredGasReserve(state *TemplateRuntimeState, gasConfig GasConfig, height int64) (*scommon.Decimal, error) {
	next := state.Running.NextPayHeight
	if next == 0 {
		next = height + 1
	}
	if c.EndHeight == 0 {
		return gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, uint64(next))
	}
	total := parseDecimalOrZero("0")
	for h := next; h <= c.EndHeight; h++ {
		fee, err := gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, uint64(h))
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(fee)
	}
	return total, nil
}

func (c *AutopayContract) requiredFeeReserve(state *TemplateRuntimeState, height int64) (*scommon.Decimal, error) {
	if state == nil {
		return parseDecimalOrZero("0"), nil
	}
	next := state.Running.NextPayHeight
	if next == 0 {
		next = height + 1
	}
	if c.EndHeight == 0 {
		return c.feeAmountForHeight(state.Running.ActiveHeight, next), nil
	}
	total := parseDecimalOrZero("0")
	for h := next; h <= c.EndHeight; h++ {
		total = total.AddAlignPrecision(c.feeAmountForHeight(state.Running.ActiveHeight, h))
	}
	return total, nil
}

func (c *AutopayContract) hasFeeAndGas(state *TemplateRuntimeState, fee, gasFee *scommon.Decimal) bool {
	if state == nil {
		return false
	}
	return decimalOrZero(state.Running.FeeBalance).Cmp(decimalOrZero(fee)) >= 0 &&
		decimalOrZero(state.Running.GasBalance).Cmp(decimalOrZero(gasFee)) >= 0
}

func (c *AutopayContract) deductFeeBalance(state *TemplateRuntimeState, fee *scommon.Decimal) error {
	if state == nil || fee == nil || fee.Sign() <= 0 {
		return nil
	}
	if c.FeeAssetName == SatoshiAssetName {
		if _, err := contractframework.DecimalToInt64(*fee); err != nil {
			return err
		}
	}
	state.Running.FeeBalance = decimalSubAllowNil(state.Running.FeeBalance, fee)
	return nil
}

func (c *AutopayContract) feeAmountForHeight(activeHeight, height int64) *scommon.Decimal {
	base := parseDecimalOrZero(c.BaseAmount)
	if c.ScheduleMode != AutopayScheduleLinear {
		return base
	}
	offset := height - (activeHeight + 1)
	if offset < 0 {
		offset = 0
	}
	step := parseDecimalOrZero("0")
	if c.StepAmount != "" {
		step = parseDecimalOrZero(c.StepAmount)
	}
	return base.AddAlignPrecision(step.MulBigInt(big.NewInt(offset)))
}

func autopayTransfer(to, assetName string, amount *scommon.Decimal) SettlementTransfer {
	transfer := SettlementTransfer{
		To:        to,
		AssetName: assetName,
		Reason:    AutopayReasonPayment,
	}
	if assetName == SatoshiAssetName {
		value, err := contractframework.DecimalToInt64(*decimalOrZero(amount))
		if err == nil {
			transfer.SatValue = value
		}
		return transfer
	}
	transfer.AssetAmt = decimalString(amount)
	return transfer
}

func (c *AutopayContract) autopayPaymentTransfer(amount *scommon.Decimal) SettlementTransfer {
	if c.Recipient != "" {
		return autopayTransfer(c.Recipient, c.FeeAssetName, amount)
	}
	transfer := autopayTransfer("", c.FeeAssetName, amount)
	transfer.Reason = AutopayReasonMinerFee
	transfer.AsFee = true
	return transfer
}

func NewAutopayDefaultInvokeItem(contract *AutopayContract, id int64, req ApplyInvokeRequest) (*InvokeItem, error) {
	amount, err := req.FundingOutput.AssetAmount(contract.FeeAssetName)
	if err != nil {
		return nil, err
	}
	amount = parseDecimalOrZero(amount.String())
	if amount.Sign() <= 0 {
		return nil, nil
	}
	return &InvokeItem{
		ID:             id,
		CallID:         req.CallID,
		Action:         contractcommon.ContractInvokeAPIDefault,
		OrderType:      OrderTypeFund,
		Height:         req.Height,
		OrderTime:      req.Timestamp,
		AssetName:      contract.FeeAssetName,
		Address:        req.Invoker,
		InUtxos:        req.FundingOutput.OutPoint.String(),
		InValue:        req.FundingOutput.PlainValue(),
		InAmt:          amount,
		RemainingAmt:   amount.Clone(),
		Reason:         InvokeReasonNormal,
		Done:           ItemStatusDealt,
		RemainingValue: req.FundingOutput.PlainValue(),
	}, nil
}

func decimalOrZero(value *scommon.Decimal) *scommon.Decimal {
	if value == nil {
		return parseDecimalOrZero("0")
	}
	return value.Clone()
}

func decimalSubAllowNil(a, b *scommon.Decimal) *scommon.Decimal {
	base := decimalOrZero(a)
	if b == nil || b.Sign() == 0 {
		return base
	}
	return base.SubAlignPrecision(b)
}

func runtimeGasAssetName(gasConfig GasConfig) string {
	gasConfig = gasConfig.Normalize()
	if gasConfig.GasAssetName == "" {
		return DefaultGasConfig().GasAssetName
	}
	return gasConfig.GasAssetName
}
