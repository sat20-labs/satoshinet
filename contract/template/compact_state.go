package template

import (
	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

// Fixed-order binary fields shared by persistence and state roots.
func writeCompactTemplateRuntimeState(e *contractcommon.StateEncoder, v TemplateRuntimeState) {
	e.I64(v.NextItemID)
	e.U64(v.InvokeCount)
	e.U64(uint64(len(v.Items)))
	for _, item := range v.Items {
		writeCompactInvokeItem(e, item)
	}
	e.Bool(v.LimitOrder != nil)
	if v.LimitOrder != nil {
		writeCompactLimitOrderRunningData(e, (*v.LimitOrder))
	}
	e.Bool(v.AMM != nil)
	if v.AMM != nil {
		writeCompactAMMRunningData(e, (*v.AMM))
	}
	e.Bool(v.Exchange != nil)
	if v.Exchange != nil {
		writeCompactExchangeRunningData(e, (*v.Exchange))
	}
	e.Bool(v.Autopay != nil)
	if v.Autopay != nil {
		writeCompactAutopayRunningData(e, (*v.Autopay))
	}
}
func readCompactTemplateRuntimeState(d *contractcommon.StateDecoder) (v TemplateRuntimeState) {
	v.NextItemID = d.I64()
	v.InvokeCount = d.U64()
	{
		count := d.CountEntries(24)
		if count > 0 {
			v.Items = make([]InvokeItem, count)
		}
		for i := 0; i < count && d.Err == nil; i++ {
			v.Items[i] = readCompactInvokeItem(d)
		}
	}
	if d.Bool() {
		var value LimitOrderRunningData
		value = readCompactLimitOrderRunningData(d)
		v.LimitOrder = &value
	}
	if d.Bool() {
		var value AMMRunningData
		value = readCompactAMMRunningData(d)
		v.AMM = &value
	}
	if d.Bool() {
		var value ExchangeRunningData
		value = readCompactExchangeRunningData(d)
		v.Exchange = &value
	}
	if d.Bool() {
		var value AutopayRunningData
		value = readCompactAutopayRunningData(d)
		v.Autopay = &value
	}
	return v
}

func writeCompactInvokeItem(e *contractcommon.StateEncoder, v InvokeItem) {
	if e.Err != nil {
		return
	}
	if err := normalizeCompactInvokeItem(&v); err != nil {
		e.Err = err
		return
	}
	writeCompactInvokeItemFields(e, v)
}

// The state reader already validates and normalizes these fields before hashing.
func writeCompactInvokeItemFields(e *contractcommon.StateEncoder, v InvokeItem) {
	e.I64(v.ID)
	e.Text(v.CallID)
	e.Text(v.Action)
	e.I64(int64(v.OrderType))
	e.I64(v.Height)
	e.I64(v.OrderTime)
	e.Text(v.AssetName)
	e.I64(v.ServiceFee)
	e.Decimal(v.GasFee)
	e.Blob(v.Param)
	e.Text(v.Address)
	e.Text(v.InUtxos)
	e.I64(v.InValue)
	e.Decimal(v.InAmt)
	e.Decimal(v.RetainedAssetA)
	e.Decimal(v.RetainedAssetB)
	e.Decimal(v.RemainingAmt)
	e.I64(v.RemainingValue)
	e.Text(v.OutTxID)
	e.Decimal(v.OutAmt)
	e.I64(v.OutValue)
	e.Text(v.Reason)
	e.I64(int64(v.Done))
	e.Bool(v.StatsApplied)
}
func readCompactInvokeItem(d *contractcommon.StateDecoder) (v InvokeItem) {
	v.ID = d.I64()
	v.CallID = d.Text()
	v.Action = d.Text()
	v.OrderType = d.Int()
	v.Height = d.I64()
	v.OrderTime = d.I64()
	v.AssetName = d.Text()
	v.ServiceFee = d.I64()
	v.GasFee = d.Decimal()
	v.Param = d.Blob()
	v.Address = d.Text()
	v.InUtxos = d.Text()
	v.InValue = d.I64()
	v.InAmt = d.Decimal()
	v.RetainedAssetA = d.Decimal()
	v.RetainedAssetB = d.Decimal()
	v.RemainingAmt = d.Decimal()
	v.RemainingValue = d.I64()
	v.OutTxID = d.Text()
	v.OutAmt = d.Decimal()
	v.OutValue = d.I64()
	v.Reason = d.Text()
	v.Done = d.Int()
	v.StatsApplied = d.Bool()
	if d.Err == nil {
		d.Fail(normalizeCompactInvokeItem(&v))
	}
	return v
}

func writeCompactLimitOrderRunningData(e *contractcommon.StateEncoder, v LimitOrderRunningData) {
	e.Decimal(v.AssetAInPool)
	e.Decimal(v.AssetBInPool)
	e.Bool(v.TradingReady)
	e.Decimal(v.GasBalance)
	e.Decimal(v.TotalInputAssetA)
	e.Decimal(v.TotalInputAssetB)
	e.Decimal(v.TotalDealAssetA)
	e.Decimal(v.TotalDealAssetB)
	e.I64(int64(v.TotalDealCount))
	e.Decimal(v.TotalRefundAssetB)
	e.Bool(v.Closed)
}
func readCompactLimitOrderRunningData(d *contractcommon.StateDecoder) (v LimitOrderRunningData) {
	v.AssetAInPool = d.Decimal()
	v.AssetBInPool = d.Decimal()
	v.TradingReady = d.Bool()
	v.GasBalance = d.Decimal()
	v.TotalInputAssetA = d.Decimal()
	v.TotalInputAssetB = d.Decimal()
	v.TotalDealAssetA = d.Decimal()
	v.TotalDealAssetB = d.Decimal()
	v.TotalDealCount = d.Int()
	v.TotalRefundAssetB = d.Decimal()
	v.Closed = d.Bool()
	return v
}

func writeCompactAMMRunningData(e *contractcommon.StateEncoder, v AMMRunningData) {
	e.Decimal(v.AssetAInPool)
	e.Decimal(v.AssetBInPool)
	e.Decimal(v.RequiredAssetA)
	e.Decimal(v.RequiredAssetB)
	e.Decimal(v.RequiredK)
	e.Decimal(v.K)
	e.Bool(v.TradingReady)
	e.Decimal(v.GasBalance)
	e.Decimal(v.TotalInputAssetA)
	e.Decimal(v.TotalInputAssetB)
	e.Decimal(v.TotalDealAssetA)
	e.Decimal(v.TotalDealAssetB)
	e.I64(int64(v.TotalDealCount))
	e.Decimal(v.TotalRefundAssetB)
	e.Decimal(v.TotalLPTAmt)
	e.U64(uint64(len(v.LPBalances)))
	for _, key := range contractcommon.SortedStateKeys(v.LPBalances) {
		e.Text(key)
		value := v.LPBalances[key]
		e.Decimal(value)
	}
	e.U64(uint64(len(v.LPCosts)))
	for _, key := range contractcommon.SortedStateKeys(v.LPCosts) {
		e.Text(key)
		value := v.LPCosts[key]
		e.I64(value)
	}
	e.Bool(v.Closed)
}
func readCompactAMMRunningData(d *contractcommon.StateDecoder) (v AMMRunningData) {
	v.AssetAInPool = d.Decimal()
	v.AssetBInPool = d.Decimal()
	v.RequiredAssetA = d.Decimal()
	v.RequiredAssetB = d.Decimal()
	v.RequiredK = d.Decimal()
	v.K = d.Decimal()
	v.TradingReady = d.Bool()
	v.GasBalance = d.Decimal()
	v.TotalInputAssetA = d.Decimal()
	v.TotalInputAssetB = d.Decimal()
	v.TotalDealAssetA = d.Decimal()
	v.TotalDealAssetB = d.Decimal()
	v.TotalDealCount = d.Int()
	v.TotalRefundAssetB = d.Decimal()
	v.TotalLPTAmt = d.Decimal()
	{
		count := d.CountEntries(2)
		if count > 0 {
			v.LPBalances = make(map[string]*scommon.Decimal, count)
		}
		previous := ""
		for i := 0; i < count && d.Err == nil; i++ {
			key := d.MapKey(previous, i)
			previous = key
			var value *scommon.Decimal
			value = d.Decimal()
			v.LPBalances[key] = value
		}
	}
	{
		count := d.CountEntries(2)
		if count > 0 {
			v.LPCosts = make(map[string]int64, count)
		}
		previous := ""
		for i := 0; i < count && d.Err == nil; i++ {
			key := d.MapKey(previous, i)
			previous = key
			var value int64
			value = d.I64()
			v.LPCosts[key] = value
		}
	}
	v.Closed = d.Bool()
	return v
}

func writeCompactExchangeRunningData(e *contractcommon.StateEncoder, v ExchangeRunningData) {
	e.Decimal(v.AssetAInPool)
	e.Decimal(v.AssetBInPool)
	e.Decimal(v.GasBalance)
	e.Decimal(v.TotalInputAssetA)
	e.Decimal(v.TotalInputAssetB)
	e.Decimal(v.TotalDealAssetA)
	e.Decimal(v.TotalDealAssetB)
	e.I64(int64(v.TotalDealCount))
	e.Decimal(v.TotalRefundAssetB)
	e.Bool(v.Closed)
}
func readCompactExchangeRunningData(d *contractcommon.StateDecoder) (v ExchangeRunningData) {
	v.AssetAInPool = d.Decimal()
	v.AssetBInPool = d.Decimal()
	v.GasBalance = d.Decimal()
	v.TotalInputAssetA = d.Decimal()
	v.TotalInputAssetB = d.Decimal()
	v.TotalDealAssetA = d.Decimal()
	v.TotalDealAssetB = d.Decimal()
	v.TotalDealCount = d.Int()
	v.TotalRefundAssetB = d.Decimal()
	v.Closed = d.Bool()
	return v
}

func writeCompactAutopayRunningData(e *contractcommon.StateEncoder, v AutopayRunningData) {
	e.Decimal(v.GasBalance)
	e.Bool(v.Closed)
	e.Decimal(v.FeeBalance)
	e.Text(v.AutopayStatus)
	e.I64(v.ActiveHeight)
	e.I64(v.NextPayHeight)
	e.I64(v.LastPayHeight)
	e.I64(v.PaidBlockCount)
	e.Bool(v.AutopayCloseStarted)
	e.U64(uint64(len(v.AutopayDelegates)))
	for _, key := range contractcommon.SortedStateKeys(v.AutopayDelegates) {
		e.Text(key)
		value := v.AutopayDelegates[key]
		writeCompactAutopayDelegate(e, value)
	}
}
func readCompactAutopayRunningData(d *contractcommon.StateDecoder) (v AutopayRunningData) {
	v.GasBalance = d.Decimal()
	v.Closed = d.Bool()
	v.FeeBalance = d.Decimal()
	v.AutopayStatus = d.Text()
	v.ActiveHeight = d.I64()
	v.NextPayHeight = d.I64()
	v.LastPayHeight = d.I64()
	v.PaidBlockCount = d.I64()
	v.AutopayCloseStarted = d.Bool()
	{
		count := d.CountEntries(8)
		if count > 0 {
			v.AutopayDelegates = make(map[string]AutopayDelegate, count)
		}
		previous := ""
		for i := 0; i < count && d.Err == nil; i++ {
			key := d.MapKey(previous, i)
			previous = key
			var value AutopayDelegate
			value = readCompactAutopayDelegate(d)
			v.AutopayDelegates[key] = value
		}
	}
	return v
}

func writeCompactAutopayDelegate(e *contractcommon.StateEncoder, v AutopayDelegate) {
	e.Decimal(v.AmountPerBlock)
	e.U64(uint64(v.BlobKeyLimit))
	e.Decimal(v.Balance)
	e.Decimal(v.TotalPaid)
	e.I64(v.PaidBlockCount)
	e.I64(v.LastPayHeight)
	e.Text(v.Status)
}
func readCompactAutopayDelegate(d *contractcommon.StateDecoder) (v AutopayDelegate) {
	v.AmountPerBlock = d.Decimal()
	v.BlobKeyLimit = d.U32()
	v.Balance = d.Decimal()
	v.TotalPaid = d.Decimal()
	v.PaidBlockCount = d.I64()
	v.LastPayHeight = d.I64()
	v.Status = d.Text()
	return v
}

func writeCompactruntimeSnapshot(e *contractcommon.StateEncoder, v runtimeSnapshot) {
	e.Text(v.Address)
	e.Text(v.TemplateName)
	e.U64(uint64(v.TemplateVersion))
	e.Text(v.Deployer)
	e.U64(v.DeployNonce)
	e.U64(uint64(v.Flags))
	e.Bool(v.ManagedBalance != nil)
	if v.ManagedBalance != nil {
		raw, err := (*v.ManagedBalance).MarshalBinary()
		if err != nil && e.Err == nil {
			e.Err = err
		}
		e.Blob(raw)
	}
	e.Blob(v.ContractContent)
	e.I64(v.CurrentBlock)
	e.U64(v.InvokeCount)
	e.U64(uint64(len(v.State)))
	for _, key := range contractcommon.SortedStateKeys(v.State) {
		e.Text(key)
		value := v.State[key]
		e.Blob(value)
	}
}
func readCompactruntimeSnapshot(d *contractcommon.StateDecoder) (v runtimeSnapshot) {
	v.Address = d.Text()
	v.TemplateName = d.Text()
	v.TemplateVersion = d.U32()
	v.Deployer = d.Text()
	v.DeployNonce = d.U64()
	v.Flags = ContractFlags(d.U32())
	if d.Bool() {
		var value contractcommon.ManagedBalance
		if d.Err == nil {
			d.Fail(value.UnmarshalBinary(d.Blob()))
		}
		v.ManagedBalance = &value
	}
	v.ContractContent = d.Blob()
	v.CurrentBlock = d.I64()
	v.InvokeCount = d.U64()
	{
		count := d.CountEntries(2)
		if count > 0 {
			v.State = make(map[string][]byte, count)
		}
		previous := ""
		for i := 0; i < count && d.Err == nil; i++ {
			key := d.MapKey(previous, i)
			previous = key
			var value []byte
			value = d.Blob()
			v.State[key] = value
		}
	}
	return v
}

func writeCompactruntimeStoreSnapshot(e *contractcommon.StateEncoder, v runtimeStoreSnapshot) {
	e.U64(uint64(len(v.Runtimes)))
	for _, item := range v.Runtimes {
		writeCompactruntimeSnapshot(e, item)
	}
}
func readCompactruntimeStoreSnapshot(d *contractcommon.StateDecoder) (v runtimeStoreSnapshot) {
	{
		count := d.CountEntries(11)
		if count > 0 {
			v.Runtimes = make([]runtimeSnapshot, count)
		}
		for i := 0; i < count && d.Err == nil; i++ {
			v.Runtimes[i] = readCompactruntimeSnapshot(d)
		}
	}
	return v
}

const templateRuntimeStateHeader = "TRS\x01"
const templateStoreHeader = "TSS\x01"

func encodeTemplateRuntimeState(state TemplateRuntimeState) ([]byte, error) {
	e := contractcommon.NewStateEncoder(templateRuntimeStateHeader)
	writeCompactTemplateRuntimeState(e, state)
	return e.Data()
}
func decodeTemplateRuntimeState(data []byte) (TemplateRuntimeState, error) {
	d := contractcommon.NewStateDecoder(data, templateRuntimeStateHeader)
	state := readCompactTemplateRuntimeState(d)
	if err := d.End(); err != nil {
		return TemplateRuntimeState{}, err
	}
	return state, nil
}
