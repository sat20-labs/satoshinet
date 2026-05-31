package template

import (
	"encoding/json"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
)

const runtimeStateKey = "template-runtime-state"

type ApplyInvokeRequest struct {
	Action         string
	Param          []byte
	CallID         string
	Invoker        string
	FundingOutputs []ContractOutput
	Height         int64
	Timestamp      int64
}

type TemplateRuntimeState struct {
	NextItemID  int64        `json:"nextItemId"`
	InvokeCount uint64       `json:"invokeCount"`
	Items       []InvokeItem `json:"items"`
	Running     RunningData  `json:"running"`
}

type InvokeItem struct {
	ID             int64   `json:"id"`
	CallID         string  `json:"callId"`
	Action         string  `json:"action"`
	OrderType      int     `json:"orderType"`
	Height         int64   `json:"height"`
	OrderTime      int64   `json:"orderTime"`
	AssetName      string  `json:"assetName"`
	ServiceFee     int64   `json:"serviceFee"`
	UnitPrice      string  `json:"unitPrice,omitempty"`
	ExpectedAmt    string  `json:"expectedAmt,omitempty"`
	Address        string  `json:"address,omitempty"`
	InUtxos        string  `json:"inUtxos,omitempty"`
	InValue        int64   `json:"inValue"`
	InAmt          string  `json:"inAmt,omitempty"`
	RemainingAmt   string  `json:"remainingAmt,omitempty"`
	RemainingValue int64   `json:"remainingValue"`
	OutTxID        string  `json:"outTxId,omitempty"`
	OutAmt         string  `json:"outAmt,omitempty"`
	OutValue       int64   `json:"outValue"`
	RefundItemIDs  []int64 `json:"refundItemIds,omitempty"`
	Reason         string  `json:"reason"`
	Done           int     `json:"done"`
}

func (i InvokeItem) Finished() bool {
	return i.Done > ItemStatusInit
}

type RunningData struct {
	AssetAmtInPool  string            `json:"assetAmtInPool,omitempty"`
	SatValueInPool  int64             `json:"satValueInPool,omitempty"`
	RequiredAsset   string            `json:"requiredAsset,omitempty"`
	RequiredSat     int64             `json:"requiredSat,omitempty"`
	K               string            `json:"k,omitempty"`
	TradingReady    bool              `json:"tradingReady,omitempty"`
	GasBalance      string            `json:"gasBalance,omitempty"`
	TotalInputAsset string            `json:"totalInputAsset,omitempty"`
	TotalInputGas   int64             `json:"totalInputGas"`
	TotalDealAsset  string            `json:"totalDealAsset,omitempty"`
	TotalDealGas    int64             `json:"totalDealGas"`
	TotalDealCount  int               `json:"totalDealCount"`
	TotalRefundGas  int64             `json:"totalRefundGas"`
	TotalLPTAmt     string            `json:"totalLptAmt,omitempty"`
	LPBalances      map[string]string `json:"lpBalances,omitempty"`
	LPCosts         map[string]int64  `json:"lpCosts,omitempty"`
}

func (r *RunningData) Apply(item *InvokeItem) {
	if item == nil {
		return
	}
	r.TotalInputGas += item.InValue
	r.TotalInputAsset = decimalStringAdd(r.TotalInputAsset, item.InAmt)
	switch item.OrderType {
	case OrderTypeBuy, OrderTypeSell:
		if item.Done == ItemStatusDealt {
			r.TotalDealAsset = decimalStringAdd(r.TotalDealAsset, item.OutAmt)
			r.TotalDealGas += item.OutValue
			r.TotalDealCount++
		} else if item.Done == ItemStatusRefunded || item.Done == ItemStatusCancelled {
			r.TotalRefundGas += item.OutValue + item.RemainingValue
		}
	case OrderTypeRefund:
		r.TotalRefundGas += item.OutValue + item.RemainingValue
	}
}

func (r *ContractRuntime) ApplyDefaultInvoke(req ApplyInvokeRequest) (*InvokeItem, error) {
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	item, err := NewDefaultInvokeItemFromRequest(r.contract, state.NextItemID, state, req)
	if err != nil || item == nil {
		return nil, err
	}
	state.NextItemID++
	state.InvokeCount++
	state.Items = append(state.Items, *item)
	state.Running.Apply(item)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return item, nil
}

func NewDefaultInvokeItemFromRequest(contract Contract, id int64, state TemplateRuntimeState, req ApplyInvokeRequest) (*InvokeItem, error) {
	assetName := contractAssetName(contract)
	inValue, inAmt, inUtxos, err := defaultInvokeFunding(assetName, req.FundingOutputs)
	if err != nil {
		return nil, err
	}
	orderType, unitPrice := defaultInvokeOrder(contract, state, inValue, inAmt)
	if orderType == OrderTypeNoSpec {
		return nil, nil
	}
	item := &InvokeItem{
		ID:             id,
		CallID:         req.CallID,
		Action:         contractcommon.ContractInvokeAPIDefault,
		OrderType:      orderType,
		Height:         req.Height,
		OrderTime:      req.Timestamp,
		AssetName:      assetName,
		Address:        req.Invoker,
		InUtxos:        inUtxos,
		InValue:        inValue,
		InAmt:          decimalString(inAmt),
		ServiceFee:     0,
		UnitPrice:      unitPrice,
		Reason:         InvokeReasonNormal,
		Done:           ItemStatusInit,
		RemainingValue: inValue,
	}
	if orderType == OrderTypeSell {
		item.RemainingAmt = item.InAmt
		item.RemainingValue = 0
	}
	return item, nil
}

func defaultInvokeFunding(assetName string, outputs []ContractOutput) (int64, *scommon.Decimal, string, error) {
	inValue := int64(0)
	inAmt := scommon.NewDefaultDecimal(0)
	inUtxos := ""
	for i, output := range outputs {
		inValue += output.Value
		if i > 0 {
			inUtxos += ","
		}
		inUtxos += output.OutPoint.String()
		if assetName != "" {
			amt, err := output.AssetAmount(assetName)
			if err != nil {
				return 0, nil, "", err
			}
			inAmt = scommon.DecimalAdd(inAmt, amt)
		}
	}
	return inValue, inAmt, inUtxos, nil
}

func defaultInvokeOrder(contract Contract, state TemplateRuntimeState, inValue int64, inAmt *scommon.Decimal) (int, string) {
	hasValue := inValue > 0
	hasAsset := inAmt != nil && inAmt.Sign() > 0
	if hasValue == hasAsset {
		return OrderTypeNoSpec, ""
	}
	if _, ok := contract.(*AMMContract); ok {
		if hasValue {
			return OrderTypeBuy, ""
		}
		return OrderTypeSell, ""
	}
	if hasValue {
		price := lowestActiveSellPrice(state)
		if price == "" {
			return OrderTypeNoSpec, ""
		}
		return OrderTypeBuy, price
	}
	price := highestActiveBuyPrice(state)
	if price == "" {
		return OrderTypeNoSpec, ""
	}
	return OrderTypeSell, price
}

func lowestActiveSellPrice(state TemplateRuntimeState) string {
	var best *scommon.Decimal
	bestString := ""
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal || item.OrderType != OrderTypeSell {
			continue
		}
		if parseDecimalOrZero(item.RemainingAmt).Sign() <= 0 {
			continue
		}
		price := parseDecimalOrZero(item.UnitPrice)
		if price.Sign() <= 0 {
			continue
		}
		if best == nil || price.Cmp(best) < 0 {
			best = price
			bestString = item.UnitPrice
		}
	}
	return bestString
}

func highestActiveBuyPrice(state TemplateRuntimeState) string {
	var best *scommon.Decimal
	bestString := ""
	for i := range state.Items {
		item := &state.Items[i]
		if item.Finished() || item.Reason != InvokeReasonNormal || item.OrderType != OrderTypeBuy || item.RemainingValue <= 0 {
			continue
		}
		price := parseDecimalOrZero(item.UnitPrice)
		if price.Sign() <= 0 {
			continue
		}
		if best == nil || price.Cmp(best) > 0 {
			best = price
			bestString = item.UnitPrice
		}
	}
	return bestString
}

func NewInvokeItemFromRequest(contract Contract, id int64, req ApplyInvokeRequest) (*InvokeItem, error) {
	inValue := int64(0)
	inAmt := scommon.NewDefaultDecimal(0)
	inUtxos := ""
	assetName := contractAssetName(contract)
	for i, output := range req.FundingOutputs {
		inValue += output.Value
		if i > 0 {
			inUtxos += ","
		}
		inUtxos += output.OutPoint.String()
		if assetName != "" {
			amt, err := output.AssetAmount(assetName)
			if err != nil {
				return nil, err
			}
			inAmt = scommon.DecimalAdd(inAmt, amt)
		}
	}

	item := &InvokeItem{
		ID:         id,
		CallID:     req.CallID,
		Action:     req.Action,
		Height:     req.Height,
		OrderTime:  req.Timestamp,
		AssetName:  assetName,
		Address:    req.Invoker,
		InUtxos:    inUtxos,
		InValue:    inValue,
		InAmt:      decimalString(inAmt),
		ServiceFee: SwapInvokeFee,
		Reason:     InvokeReasonNormal,
		Done:       ItemStatusInit,
	}

	switch req.Action {
	case InvokeAPISwap:
		var param LimitOrderInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		_, isAMM := contract.(*AMMContract)
		item.OrderType = param.OrderType
		item.AssetName = firstNonEmpty(param.AssetName, assetName)
		item.UnitPrice = param.UnitPrice
		item.ExpectedAmt = param.Amt
		item.RemainingValue = inValue
		if item.OrderType == OrderTypeSell {
			item.RemainingAmt = item.InAmt
			item.RemainingValue = 0
		} else {
			item.RemainingAmt = param.Amt
			if !isAMM {
				item.ServiceFee = calcSwapFee(calcLimitOrderTradingValue(param.Amt, param.UnitPrice))
			}
			item.RemainingValue = inValue - item.ServiceFee
			if item.RemainingValue < 0 {
				item.RemainingValue = 0
				item.Reason = InvokeReasonInvalid
			}
		}
		item.applySwapFundingValidation(param, isAMM)
	case InvokeAPIRefund:
		var param RefundInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		item.OrderType = OrderTypeRefund
		item.AssetName = ""
		item.InAmt = ""
		item.RemainingValue = inValue
		item.RefundItemIDs = append([]int64(nil), param.ItemIDs...)
	case InvokeAPIAddLiquidity:
		var param AddLiquidityInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		item.OrderType = param.OrderType
		item.AssetName = firstNonEmpty(param.AssetName, assetName)
		item.ExpectedAmt = param.Amt
		item.RemainingAmt = param.Amt
		item.RemainingValue = param.Value
	case InvokeAPIRemoveLiquidity:
		var param RemoveLiquidityInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		item.OrderType = param.OrderType
		item.AssetName = firstNonEmpty(param.AssetName, assetName)
		item.ExpectedAmt = param.LptAmt
		item.RemainingAmt = param.LptAmt
	default:
		return nil, fmt.Errorf("unsupported template action %s", req.Action)
	}
	return item, nil
}

func (i *InvokeItem) applySwapFundingValidation(param LimitOrderInvokeParam, isAMM bool) {
	if i.Reason != InvokeReasonNormal {
		return
	}
	switch i.OrderType {
	case OrderTypeBuy:
		requiredValue := calcLimitOrderTradingValue(param.Amt, param.UnitPrice)
		if isAMM {
			requiredValue = parseDecimalOrZero(param.UnitPrice).Int64()
		}
		expected := requiredValue + i.ServiceFee
		if expected <= 0 || !valueWithinTolerance(i.InValue, expected, 5) {
			i.Reason = InvokeReasonInvalid
			i.RemainingValue = 0
		}
	case OrderTypeSell:
		if i.InValue < i.ServiceFee {
			i.Reason = InvokeReasonInvalid
			i.RemainingAmt = ""
			return
		}
		inAmt := parseDecimalOrZero(i.InAmt)
		if inAmt.Sign() == 0 {
			i.Reason = InvokeReasonInvalid
			return
		}
		if !isAMM && inAmt.Cmp(parseDecimalOrZero(param.Amt)) != 0 {
			i.Reason = InvokeReasonInvalid
			i.RemainingAmt = ""
		}
	}
}

func valueWithinTolerance(got, expected int64, percent int64) bool {
	if expected < 0 || percent < 0 {
		return false
	}
	min := expected * (100 - percent) / 100
	max := expected * (100 + percent) / 100
	return got >= min && got <= max
}

func (r *ContractRuntime) RuntimeState() (TemplateRuntimeState, error) {
	return r.loadRuntimeState()
}

func (r *ContractRuntime) initializeRuntimeState() error {
	switch c := r.contract.(type) {
	case *AMMContract:
		state := TemplateRuntimeState{
			Running: RunningData{
				RequiredAsset: c.AssetAmt,
				RequiredSat:   c.SatValue,
				K:             c.K,
			},
		}
		return r.saveRuntimeState(state)
	default:
		return nil
	}
}

func (r *ContractRuntime) ApplyFunding(outputs []ContractOutput, gasAssetName string) error {
	if len(outputs) == 0 {
		return nil
	}
	state, err := r.loadRuntimeState()
	if err != nil {
		return err
	}
	assetName := contractAssetName(r.contract)
	for _, output := range outputs {
		if _, ok := r.contract.(*AMMContract); ok {
			state.Running.SatValueInPool += output.Value
			if assetName != "" {
				amt, err := output.AssetAmount(assetName)
				if err != nil {
					return err
				}
				state.Running.AssetAmtInPool = decimalStringAdd(state.Running.AssetAmtInPool, amt.String())
			}
		}
		if gasAssetName != "" {
			gas, err := output.AssetAmount(gasAssetName)
			if err != nil {
				return err
			}
			state.Running.GasBalance = decimalStringAdd(state.Running.GasBalance, gas.String())
		}
	}
	if !state.Running.TradingReady {
		state.Running.TradingReady = state.Running.ammTradingReady()
	}
	return r.saveRuntimeState(state)
}

func (r *ContractRuntime) ApplyGasFunding(outputs []ContractOutput, gasAssetName string) error {
	if len(outputs) == 0 || gasAssetName == "" {
		return nil
	}
	state, err := r.loadRuntimeState()
	if err != nil {
		return err
	}
	for _, output := range outputs {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return err
		}
		state.Running.GasBalance = decimalStringAdd(state.Running.GasBalance, gas.String())
	}
	return r.saveRuntimeState(state)
}

func (r *ContractRuntime) loadRuntimeState() (TemplateRuntimeState, error) {
	data, ok := r.GetState(runtimeStateKey)
	if !ok || len(data) == 0 {
		return TemplateRuntimeState{}, nil
	}
	var state TemplateRuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return TemplateRuntimeState{}, err
	}
	return state, nil
}

func (r *ContractRuntime) saveRuntimeState(state TemplateRuntimeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	r.SetState(runtimeStateKey, data)
	return nil
}

func contractAssetName(contract Contract) string {
	switch c := contract.(type) {
	case *LimitOrderContract:
		return c.AssetName
	case *AMMContract:
		return c.AssetName
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func decimalString(d *scommon.Decimal) string {
	if d == nil || d.Sign() == 0 {
		return ""
	}
	return d.String()
}

func decimalStringAdd(a, b string) string {
	da := parseDecimalOrZero(a)
	db := parseDecimalOrZero(b)
	return decimalString(scommon.DecimalAdd(da, db))
}

func decimalStringSub(a, b string) string {
	da := parseDecimalOrZero(a)
	db := parseDecimalOrZero(b)
	return decimalString(scommon.DecimalSub(da, db))
}

func parseDecimalOrZero(value string) *scommon.Decimal {
	if value == "" {
		return scommon.NewDefaultDecimal(0)
	}
	d, err := scommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return scommon.NewDefaultDecimal(0)
	}
	return d
}

func (r RunningData) ammTradingReady() bool {
	requiredAsset := parseDecimalOrZero(r.RequiredAsset)
	if requiredAsset.Sign() == 0 {
		return true
	}
	asset := parseDecimalOrZero(r.AssetAmtInPool)
	if asset.Cmp(requiredAsset) < 0 || r.SatValueInPool < r.RequiredSat {
		return false
	}
	k := parseDecimalOrZero(r.K)
	if k.Sign() == 0 {
		return true
	}
	currentK := scommon.DecimalMul(asset, scommon.NewDefaultDecimal(r.SatValueInPool))
	return currentK.Cmp(k) >= 0
}
