package template

import (
	"fmt"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	AutopayStatusFunding = "funding"
	AutopayStatusActive  = "active"
	AutopayStatusExpired = "expired"
	AutopayStatusClosed  = "closed"

	AutopayReasonPayment   = "autopay"
	AutopayReasonMinerFee  = "miner_fee"
	AutopayMaxCloseOutputs = contractframework.MaxContractResultOutputs
	// Final close settlement can add deployer and bootstrap outputs after
	// delegate refunds. Deployer gas and profit are compacted together.
	AutopayCloseReservedOutputs    = 2
	AutopayMaxCloseDelegateOutputs = AutopayMaxCloseOutputs - AutopayCloseReservedOutputs
	AutopayMaxDelegates            = 10000
	AutopayDefaultBlobKeyLimit     = uint32(1)
	AutopayMaxBlobKeyLimit         = uint32(1024)
)

type AutopayContract struct {
	ServiceName       string `json:"serviceName"`
	Recipient         string `json:"recipient"`
	FeeAssetName      string `json:"feeAssetName"`
	MinAmountPerBlock string `json:"minAmountPerBlock"`
}

func NewAutopayContract(serviceName, recipient, feeAssetName, minAmountPerBlock string) *AutopayContract {
	return &AutopayContract{
		ServiceName:       serviceName,
		Recipient:         recipient,
		FeeAssetName:      feeAssetName,
		MinAmountPerBlock: minAmountPerBlock,
	}
}

func (c *AutopayContract) TemplateName() string {
	return TemplateAutopay
}

func (c *AutopayContract) NetworkExclusive() bool {
	return false
}

func (c *AutopayContract) BaseGasConfig() contractframework.BaseGasConfig {
	return contractframework.BaseGasConfig{}
}

func (c *AutopayContract) Version() uint32 {
	return CurrentTemplateVersion
}

func (c *AutopayContract) Encode() ([]byte, error) {
	if err := c.CheckContent(); err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(c.ServiceName)).
		AddData([]byte(c.Recipient)).
		AddData([]byte(c.FeeAssetName)).
		AddData([]byte(c.MinAmountPerBlock)).
		Script()
}

func (c *AutopayContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing service name")
	}
	c.ServiceName = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing recipient")
	}
	c.Recipient = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing fee asset name")
	}
	c.FeeAssetName = string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing minimum amount per block")
	}
	c.MinAmountPerBlock = string(tokenizer.Data())
	if tokenizer.Next() {
		return fmt.Errorf("unexpected autopay contract content")
	}
	return tokenizer.Err()
}

func (c *AutopayContract) CheckContent() error {
	return (contractcommon.TemplateAutopayContract{
		ServiceName:       c.ServiceName,
		Recipient:         c.Recipient,
		FeeAssetName:      c.FeeAssetName,
		MinAmountPerBlock: c.MinAmountPerBlock,
	}).Check()
}

func (c *AutopayContract) CheckInvoke(action string, param []byte) error {
	switch action {
	case InvokeAPIConfig:
		var config AutopayConfigInvokeParam
		if err := config.Decode(param); err != nil {
			return err
		}
		gas, err := autopayConfigGasAmount(config)
		if err != nil {
			return err
		}
		if config.AmountPerBlock == "" && gas.Sign() > 0 {
			if config.BlobKeyLimit != 0 {
				return fmt.Errorf("gas-only autopay config cannot change blob key limit")
			}
			return nil
		}
		amount, err := contractframework.ParseDecimalAmountString(config.AmountPerBlock)
		if err != nil || amount.Sign() <= 0 {
			return fmt.Errorf("invalid autopay amount per block")
		}
		if amount.Cmp(c.minAmountPerBlock()) < 0 {
			return fmt.Errorf("autopay amount below minimum")
		}
		if config.BlobKeyLimit > AutopayMaxBlobKeyLimit {
			return fmt.Errorf("invalid autopay blob key limit")
		}
		if c.FeeAssetName == SatoshiAssetName {
			if _, err := contractframework.DecimalToInt64(*amount); err != nil {
				return fmt.Errorf("autopay sats amount must be an integer: %w", err)
			}
		}
		return nil
	case InvokeAPICancel:
		return (&CloseInvokeParam{}).Decode(param)
	case InvokeAPIClose:
		return (&CloseInvokeParam{}).Decode(param)
	default:
		return fmt.Errorf("unsupported autopay action %s", action)
	}
}

func (c *AutopayContract) CheckInvokePrecision(action string, param []byte,
	resolve contractframework.AssetPrecisionResolver) error {

	if action != InvokeAPIConfig || c.FeeAssetName == SatoshiAssetName {
		return nil
	}
	var config AutopayConfigInvokeParam
	if err := config.Decode(param); err != nil {
		return err
	}
	if config.AmountPerBlock == "" && parseDecimalOrZero(config.GasFundingAmount).Sign() > 0 {
		return nil
	}
	amount, err := contractframework.ParseDecimalAmountString(config.AmountPerBlock)
	if err != nil {
		return err
	}
	if resolve == nil {
		return fmt.Errorf("missing autopay asset precision resolver")
	}
	precision, ok := resolve(c.FeeAssetName)
	if !ok || precision < 0 {
		return fmt.Errorf("unknown autopay asset precision for %s", c.FeeAssetName)
	}
	canonical := amount.NewPrecision(precision)
	if canonical.Sign() <= 0 || canonical.Cmp(amount) != 0 {
		return fmt.Errorf("autopay amount %s is not exactly representable at precision %d",
			amount.String(), precision)
	}
	return nil
}

func autopayConfigGasAmount(param AutopayConfigInvokeParam) (*scommon.Decimal, error) {
	if param.GasFundingAmount == "" {
		return parseDecimalOrZero("0"), nil
	}
	gas, err := contractframework.ParseDecimalAmountString(param.GasFundingAmount)
	if err != nil || gas.Sign() < 0 {
		return nil, fmt.Errorf("invalid autopay gas funding amount")
	}
	return gas, nil
}

func autopayGasOnlyConfig(contract Contract, action string, raw []byte) bool {
	if _, ok := contract.(*AutopayContract); !ok || action != InvokeAPIConfig {
		return false
	}
	var param AutopayConfigInvokeParam
	return param.Decode(raw) == nil && param.AmountPerBlock == "" &&
		parseDecimalOrZero(param.GasFundingAmount).Sign() > 0
}

// splitAutopayGasFunding operates on funding after the current Result fee.
// Validation precedes every mutation, including on the invalid/refund path.
func (r *ContractRuntime) splitAutopayGasFunding(req ApplyInvokeRequest) (ContractOutput, error) {
	output := req.FundingOutput
	contract, ok := r.Contract().(*AutopayContract)
	if !ok || req.Action != InvokeAPIConfig {
		return output, nil
	}
	var param AutopayConfigInvokeParam
	if err := param.Decode(req.Param); err != nil {
		return output, err
	}
	gas, err := autopayConfigGasAmount(param)
	if err != nil || gas.Sign() == 0 {
		return output, err
	}
	if req.Invoker == "" || req.Invoker != r.base.Deployer() {
		return output, fmt.Errorf("only autopay deployer may fund operating gas")
	}
	if req.GasAssetName == "" {
		return output, fmt.Errorf("missing autopay gas asset")
	}
	precision := 0
	if req.GasAssetName != SatoshiAssetName {
		if req.AssetPrecision == nil {
			return output, fmt.Errorf("missing autopay gas asset precision resolver")
		}
		var known bool
		precision, known = req.AssetPrecision(req.GasAssetName)
		if !known || precision < 0 || precision > scommon.MAX_PRECISION {
			return output, fmt.Errorf("unknown autopay gas asset precision")
		}
	}
	if gas.NewPrecision(precision).Cmp(gas) != 0 {
		return output, fmt.Errorf("autopay gas funding is not exactly representable at precision %d", precision)
	}
	available, err := output.AssetAmount(req.GasAssetName)
	if err != nil {
		return output, err
	}
	if available.Cmp(gas) < 0 {
		return output, fmt.Errorf("autopay gas funding exceeds net funding")
	}
	if err := output.SubAssetAmount(req.GasAssetName, gas); err != nil {
		return output, err
	}
	if param.AmountPerBlock == "" {
		fee, err := output.AssetAmount(contract.FeeAssetName)
		if err != nil {
			return output, err
		}
		if fee.Sign() != 0 {
			return output, fmt.Errorf("gas-only autopay config contains delegate funding")
		}
	}
	return output, nil
}

func (c *AutopayContract) ApplyFundingState(state *TemplateRuntimeState, output ContractOutput, gasAssetName string) (bool, error) {
	return c.ApplyFundingStateForAddress(state, "", output, gasAssetName)
}

func (c *AutopayContract) ApplyFundingStateForAddress(state *TemplateRuntimeState, address string, output ContractOutput, gasAssetName string) (bool, error) {
	if state == nil {
		return true, nil
	}
	if err := c.addFunding(state, address, output, gasAssetName); err != nil {
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
	state.AutopayData().GasBalance = decimalAddAllowNil(state.AutopayData().GasBalance, gas)
	return true, nil
}

func (c *AutopayContract) ApplyRunningData(state *TemplateRuntimeState, item *InvokeItem) bool {
	if state == nil || item == nil {
		return true
	}
	if item.Reason == InvokeReasonInvalid {
		return true
	}
	autopay := state.AutopayData()
	switch item.OrderType {
	case OrderTypeFund:
		c.addDelegateBalance(autopay, item.Address, item.InAmt)
	case OrderTypeValidate:
		param, err := decodeAutopayConfigItemParam(item)
		if err != nil {
			item.Reason = InvokeReasonInvalid
			return true
		}
		autopay.GasBalance = decimalAddAllowNil(autopay.GasBalance, parseDecimalOrZero(param.GasFundingAmount))
		if param.AmountPerBlock == "" {
			return true // Pure operator funding must not create or reconfigure a delegate.
		}
		c.setDelegateConfig(autopay, item.Address, parseDecimalOrZero(param.AmountPerBlock), param.BlobKeyLimit)
		// A config invoke may also fund the delegate. The backend has already
		// removed this invoke's Result fee from InAmt when it is the fee asset.
		c.addDelegateBalance(autopay, item.Address, item.InAmt)
	case OrderTypeCancel:
	case OrderTypeClose:
	default:
	}
	return true
}

func (c *AutopayContract) addFunding(state *TemplateRuntimeState, address string, output ContractOutput, gasAssetName string) error {
	fee, err := output.AssetAmount(c.FeeAssetName)
	if err != nil {
		return err
	}
	if address != "" {
		c.addDelegateBalance(state.AutopayData(), address, fee)
	}
	if gasAssetName != "" && gasAssetName != c.FeeAssetName {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return err
		}
		state.AutopayData().GasBalance = decimalAddAllowNil(state.AutopayData().GasBalance, gas)
	}
	if state.AutopayData().AutopayStatus == "" {
		state.AutopayData().AutopayStatus = AutopayStatusFunding
	}
	return nil
}

func (c *AutopayContract) ensureDelegate(running *AutopayRunningData, address string) AutopayDelegate {
	if running.AutopayDelegates == nil {
		running.AutopayDelegates = make(map[string]AutopayDelegate)
	}
	delegate := running.AutopayDelegates[address]
	if delegate.AmountPerBlock == nil {
		delegate.AmountPerBlock = c.minAmountPerBlock()
	}
	if delegate.BlobKeyLimit == 0 {
		delegate.BlobKeyLimit = AutopayDefaultBlobKeyLimit
	}
	if delegate.Status == "" {
		delegate.Status = AutopayStatusFunding
	}
	return delegate
}

func normalizeAutopayBlobKeyLimit(limit uint32) uint32 {
	if limit == 0 {
		return AutopayDefaultBlobKeyLimit
	}
	if limit > AutopayMaxBlobKeyLimit {
		return AutopayMaxBlobKeyLimit
	}
	return limit
}

func (c *AutopayContract) setDelegateConfig(running *AutopayRunningData, address string, amount *scommon.Decimal, blobKeyLimit uint32) {
	if running == nil || address == "" || amount == nil || amount.Sign() <= 0 {
		return
	}
	delegate := c.ensureDelegate(running, address)
	delegate.AmountPerBlock = amount.Clone()
	delegate.BlobKeyLimit = normalizeAutopayBlobKeyLimit(blobKeyLimit)
	delegate.Status = c.delegateStatus(delegate)
	running.AutopayDelegates[address] = delegate
}

func (c *AutopayContract) addDelegateBalance(running *AutopayRunningData, address string, amount *scommon.Decimal) {
	if running == nil || address == "" || amount == nil || amount.Sign() <= 0 {
		return
	}
	delegate := c.ensureDelegate(running, address)
	delegate.Balance = decimalAddAllowNil(delegate.Balance, amount)
	delegate.Status = c.delegateStatus(delegate)
	running.AutopayDelegates[address] = delegate
	running.FeeBalance = c.totalDelegateBalance(running)
}

func (c *AutopayContract) delegateStatus(delegate AutopayDelegate) string {
	if decimalOrZero(delegate.Balance).Cmp(decimalOrZero(delegate.AmountPerBlock)) >= 0 {
		return AutopayStatusActive
	}
	return AutopayStatusFunding
}

func (c *AutopayContract) minAmountPerBlock() *scommon.Decimal {
	return parseDecimalOrZero(c.MinAmountPerBlock)
}

func (c *AutopayContract) totalDelegateBalance(running *AutopayRunningData) *scommon.Decimal {
	total := parseDecimalOrZero("0")
	if running == nil {
		return total
	}
	for _, delegate := range running.AutopayDelegates {
		total = total.AddAlignPrecision(decimalOrZero(delegate.Balance))
	}
	return total
}

func (c *AutopayContract) settleAutopay(runtime *ContractRuntime, state *TemplateRuntimeState,
	height int64, gasConfig GasConfig) (*SettlementPlan, error) {

	addr := runtime.Address()
	plan := &SettlementPlan{Contract: addr.EncodeAddress(), Height: height}
	if state == nil {
		return plan, nil
	}
	// Refund newly rejected invokes before any operating-gas or closed-state
	// gate. Older invalid items may reference inputs consumed by past Results;
	// this fixes new calls without replaying historical refunds.
	if _, err := applyInvalidItemsInRange(c, state, plan, height, height); err != nil {
		return nil, err
	}
	closed, err := c.applyAutopayClose(runtime, state, plan, height, gasConfig)
	if err != nil {
		return nil, err
	}
	if closed {
		state.AutopayData().FeeBalance = c.totalDelegateBalance(state.AutopayData())
		return plan, nil
	}
	cancelled, err := c.applyAutopayCancels(state, plan, height)
	if err != nil {
		return nil, err
	}
	if cancelled {
		state.AutopayData().FeeBalance = c.totalDelegateBalance(state.AutopayData())
		state.AutopayData().AutopayStatus = c.autopayFundingStatus(state, gasConfig, height)
		return plan, nil
	}
	if state.AutopayData().Closed || state.AutopayData().AutopayStatus == AutopayStatusClosed ||
		state.AutopayData().AutopayStatus == AutopayStatusExpired {
		return plan, nil
	}

	gasConfig = gasConfig.Normalize()
	triggerGasFee, err := gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, uint64(height))
	if err != nil {
		return nil, err
	}
	next := state.AutopayData().NextPayHeight
	if next == 0 {
		state.AutopayData().ActiveHeight = height
		state.AutopayData().NextPayHeight = height + 1
		next = state.AutopayData().NextPayHeight
	}
	if height < next {
		state.AutopayData().AutopayStatus = c.autopayFundingStatus(state, gasConfig, height)
		return plan, nil
	}
	if decimalOrZero(state.AutopayData().GasBalance).Cmp(triggerGasFee) < 0 {
		state.AutopayData().AutopayStatus = AutopayStatusFunding
		return plan, nil
	}
	total, err := c.collectAutopayFees(state, height)
	if err != nil {
		return nil, err
	}
	if total.Sign() <= 0 {
		state.AutopayData().AutopayStatus = AutopayStatusFunding
		return plan, nil
	}
	state.AutopayData().GasBalance = decimalSubAllowNil(state.AutopayData().GasBalance, triggerGasFee)
	plan.GasFee = triggerGasFee.Clone()
	transfer, err := c.autopayPaymentTransfer(total)
	if err != nil {
		return nil, err
	}
	plan.Transfers = append(plan.Transfers, transfer)
	state.AutopayData().PaidBlockCount++
	state.AutopayData().LastPayHeight = height
	state.AutopayData().NextPayHeight = height + 1
	state.AutopayData().FeeBalance = c.totalDelegateBalance(state.AutopayData())
	state.AutopayData().AutopayStatus = c.autopayFundingStatus(state, gasConfig, height)
	return plan, nil
}

func (c *AutopayContract) applyAutopayClose(runtime *ContractRuntime, state *TemplateRuntimeState,
	plan *SettlementPlan, height int64, gasConfig GasConfig) (bool, error) {

	if state == nil || plan == nil || state.AutopayData().Closed {
		return false, nil
	}
	deployer := runtime.RuntimeBase().Deployer()
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal ||
			item.OrderType != OrderTypeClose || item.Height > height {
			continue
		}
		if !state.AutopayData().AutopayCloseStarted {
			addSettlementInputs(plan, item)
		}
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		if item.Address != deployer {
			item.Reason = InvokeReasonInvalid
			item.Done = ItemStatusClosedDirectly
			return true, nil
		}
		gasFee, err := c.closeBatchGasFee(state, item, gasConfig, height)
		if err != nil {
			return false, err
		}
		if gasFee != nil && gasFee.Sign() > 0 {
			plan.GasFee = gasFee
			state.AutopayData().GasBalance = decimalSubAllowNil(state.AutopayData().GasBalance, gasFee)
		}
		state.AutopayData().AutopayCloseStarted = true
		processed := 0
		for _, address := range c.sortedDelegateAddresses(state.AutopayData()) {
			if processed >= AutopayMaxCloseDelegateOutputs {
				break
			}
			delegate := state.AutopayData().AutopayDelegates[address]
			balance := decimalOrZero(delegate.Balance)
			if balance.Sign() <= 0 {
				continue
			}
			if err := c.appendBalanceTransfer(plan, address, c.FeeAssetName, balance, false); err != nil {
				return false, err
			}
			delegate.Balance = nil
			delegate.Status = AutopayStatusClosed
			state.AutopayData().AutopayDelegates[address] = delegate
			processed++
		}
		state.AutopayData().FeeBalance = c.totalDelegateBalance(state.AutopayData())
		if !c.hasPendingDelegateBalance(state.AutopayData()) {
			gasAssetName := runtimeGasAssetName(gasConfig)
			if err := c.appendBalanceTransfer(plan, deployer, gasAssetName, state.AutopayData().GasBalance, true); err != nil {
				return false, err
			}
			state.AutopayData().GasBalance = nil
			state.AutopayData().Closed = true
			state.AutopayData().AutopayStatus = AutopayStatusClosed
			item.Done = ItemStatusDealt
		} else {
			state.AutopayData().AutopayStatus = AutopayStatusActive
		}
		return true, nil
	}
	return false, nil
}

func (c *AutopayContract) applyAutopayCancels(state *TemplateRuntimeState, plan *SettlementPlan, height int64) (bool, error) {
	if state == nil || plan == nil {
		return false, nil
	}
	changed := false
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal ||
			item.OrderType != OrderTypeCancel || item.Height > height {
			continue
		}
		addSettlementInputs(plan, item)
		plan.ItemIDs = appendPlanItemID(plan.ItemIDs, item.ID)
		delegate := state.AutopayData().AutopayDelegates[item.Address]
		balance := decimalOrZero(delegate.Balance)
		if balance.Sign() > 0 {
			if err := c.appendBalanceTransfer(plan, item.Address, c.FeeAssetName, balance, false); err != nil {
				return false, err
			}
		}
		delegate.Balance = nil
		delegate.Status = AutopayStatusClosed
		if state.AutopayData().AutopayDelegates == nil {
			state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate)
		}
		state.AutopayData().AutopayDelegates[item.Address] = delegate
		item.Done = ItemStatusCancelled
		changed = true
	}
	return changed, nil
}

func (c *AutopayContract) appendBalanceTransfer(plan *SettlementPlan, to, assetName string,
	amount *scommon.Decimal, gas bool) error {

	if plan == nil || to == "" || assetName == "" || amount == nil || amount.Sign() <= 0 {
		return nil
	}
	transfer, err := autopayTransfer(to, assetName, amount)
	if err != nil {
		return err
	}
	plan.Transfers = append(plan.Transfers, transfer)
	return nil
}

func (c *AutopayContract) autopayFundingStatus(state *TemplateRuntimeState, gasConfig GasConfig, height int64) string {
	if state == nil {
		return AutopayStatusFunding
	}
	gasFee, err := c.requiredGasReserve(state, gasConfig, height)
	if err != nil {
		return AutopayStatusFunding
	}
	if decimalOrZero(state.AutopayData().GasBalance).Cmp(gasFee) >= 0 && c.hasActiveDelegate(state.AutopayData()) {
		return AutopayStatusActive
	}
	return AutopayStatusFunding
}

func (c *AutopayContract) requiredGasReserve(state *TemplateRuntimeState, gasConfig GasConfig, height int64) (*scommon.Decimal, error) {
	next := state.AutopayData().NextPayHeight
	if next == 0 {
		next = height + 1
	}
	return gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, uint64(next))
}

func (c *AutopayContract) closeBatchGasFee(state *TemplateRuntimeState, item *InvokeItem, gasConfig GasConfig, height int64) (*scommon.Decimal, error) {
	if state == nil || item == nil {
		return nil, nil
	}
	if !state.AutopayData().AutopayCloseStarted && item.GasFee != nil && item.GasFee.Sign() > 0 {
		return nil, nil
	}
	fee, err := gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, uint64(height))
	if err != nil {
		return nil, err
	}
	if decimalOrZero(state.AutopayData().GasBalance).Cmp(fee) < 0 {
		return nil, fmt.Errorf("insufficient autopay gas for close batch")
	}
	return fee, nil
}

func (c *AutopayContract) collectAutopayFees(state *TemplateRuntimeState, height int64) (*scommon.Decimal, error) {
	total := parseDecimalOrZero("0")
	if state == nil {
		return total, nil
	}
	for _, address := range c.sortedDelegateAddresses(state.AutopayData()) {
		delegate := state.AutopayData().AutopayDelegates[address]
		amount := decimalOrZero(delegate.AmountPerBlock)
		if amount.Sign() <= 0 {
			amount = c.minAmountPerBlock()
		}
		balance := decimalOrZero(delegate.Balance)
		if balance.Cmp(amount) < 0 {
			delegate.Status = AutopayStatusFunding
			state.AutopayData().AutopayDelegates[address] = delegate
			continue
		}
		if c.FeeAssetName == SatoshiAssetName {
			if _, err := contractframework.DecimalToInt64(*amount); err != nil {
				return nil, err
			}
		}
		delegate.Balance = balance.SubAlignPrecision(amount)
		delegate.TotalPaid = decimalAddAllowNil(delegate.TotalPaid, amount)
		delegate.PaidBlockCount++
		delegate.LastPayHeight = height
		delegate.Status = c.delegateStatus(delegate)
		state.AutopayData().AutopayDelegates[address] = delegate
		total = total.AddAlignPrecision(amount)
	}
	return total, nil
}

func (c *AutopayContract) sortedDelegateAddresses(running *AutopayRunningData) []string {
	if running == nil || len(running.AutopayDelegates) == 0 {
		return nil
	}
	addresses := make([]string, 0, len(running.AutopayDelegates))
	for address := range running.AutopayDelegates {
		if address != "" {
			addresses = append(addresses, address)
		}
	}
	sort.Strings(addresses)
	return addresses
}

func (c *AutopayContract) hasActiveDelegate(running *AutopayRunningData) bool {
	if running == nil {
		return false
	}
	for _, delegate := range running.AutopayDelegates {
		if decimalOrZero(delegate.Balance).Cmp(decimalOrZero(delegate.AmountPerBlock)) >= 0 {
			return true
		}
	}
	return false
}

func (c *AutopayContract) hasPendingDelegateBalance(running *AutopayRunningData) bool {
	if running == nil {
		return false
	}
	for _, delegate := range running.AutopayDelegates {
		if decimalOrZero(delegate.Balance).Sign() > 0 {
			return true
		}
	}
	return false
}

func autopayTransfer(to, assetName string, amount *scommon.Decimal) (SettlementTransfer, error) {
	transfer := SettlementTransfer{
		To:        to,
		AssetName: assetName,
		Reason:    AutopayReasonPayment,
	}
	if assetName == SatoshiAssetName {
		value, err := contractframework.DecimalToInt64(*decimalOrZero(amount))
		if err != nil {
			return SettlementTransfer{}, err
		}
		transfer.SatValue = value
		return transfer, nil
	}
	transfer.AssetAmt = decimalString(amount)
	return transfer, nil
}

func (c *AutopayContract) autopayPaymentTransfer(amount *scommon.Decimal) (SettlementTransfer, error) {
	if c.Recipient != "" {
		return autopayTransfer(c.Recipient, c.FeeAssetName, amount)
	}
	transfer, err := autopayTransfer("", c.FeeAssetName, amount)
	if err != nil {
		return SettlementTransfer{}, err
	}
	transfer.Reason = AutopayReasonMinerFee
	transfer.AsFee = true
	return transfer, nil
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
		Param:          contractframework.CloneBytes(req.Param),
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
