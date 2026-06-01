package template

import (
	"encoding/json"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const runtimeStateKey = "template-runtime-state"

type ApplyInvokeRequest struct {
	Action                string
	Param                 []byte
	CallID                string
	Invoker               string
	FundingOutputs        []ContractOutput
	Height                int64
	Timestamp             int64
	ResultGasFee          uint64
	ApplyDefaultRetention bool
}

type TemplateRuntimeState struct {
	NextItemID  int64        `json:"nextItemId"`
	InvokeCount uint64       `json:"invokeCount"`
	Items       []InvokeItem `json:"items"`
	Running     RunningData  `json:"running"`
}

type InvokeItem struct {
	ID             int64            `json:"id"`
	CallID         string           `json:"callId"`
	Action         string           `json:"action"`
	OrderType      int              `json:"orderType"`
	Height         int64            `json:"height"`
	OrderTime      int64            `json:"orderTime"`
	AssetName      string           `json:"assetName"`
	ServiceFee     int64            `json:"serviceFee"`
	UnitPrice      string           `json:"unitPrice,omitempty"`
	ExpectedAmt    *scommon.Decimal `json:"expectedAmt,omitempty"`
	Address        string           `json:"address,omitempty"`
	InUtxos        string           `json:"inUtxos,omitempty"`
	InValue        int64            `json:"inValue"`
	InAmt          *scommon.Decimal `json:"inAmt,omitempty"`
	RetainedAssetA *scommon.Decimal `json:"retainedAssetA,omitempty"`
	RetainedAssetB *scommon.Decimal `json:"retainedAssetB,omitempty"`
	RemainingAmt   *scommon.Decimal `json:"remainingAmt,omitempty"`
	RemainingValue int64            `json:"remainingValue"`
	OutTxID        string           `json:"outTxId,omitempty"`
	OutAmt         *scommon.Decimal `json:"outAmt,omitempty"`
	OutValue       int64            `json:"outValue"`
	RefundItemIDs  []int64          `json:"refundItemIds,omitempty"`
	Reason         string           `json:"reason"`
	Done           int              `json:"done"`
}

func (i InvokeItem) Finished() bool {
	return i.Done > ItemStatusInit
}

type invokeItemJSON struct {
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
	RetainedAssetA string  `json:"retainedAssetA,omitempty"`
	RetainedAssetB string  `json:"retainedAssetB,omitempty"`
	RemainingAmt   string  `json:"remainingAmt,omitempty"`
	RemainingValue int64   `json:"remainingValue"`
	OutTxID        string  `json:"outTxId,omitempty"`
	OutAmt         string  `json:"outAmt,omitempty"`
	OutValue       int64   `json:"outValue"`
	RefundItemIDs  []int64 `json:"refundItemIds,omitempty"`
	Reason         string  `json:"reason"`
	Done           int     `json:"done"`
}

func (i InvokeItem) MarshalJSON() ([]byte, error) {
	return json.Marshal(invokeItemJSON{
		ID:             i.ID,
		CallID:         i.CallID,
		Action:         i.Action,
		OrderType:      i.OrderType,
		Height:         i.Height,
		OrderTime:      i.OrderTime,
		AssetName:      i.AssetName,
		ServiceFee:     i.ServiceFee,
		UnitPrice:      i.UnitPrice,
		ExpectedAmt:    decimalString(i.ExpectedAmt),
		Address:        i.Address,
		InUtxos:        i.InUtxos,
		InValue:        i.InValue,
		InAmt:          decimalString(i.InAmt),
		RetainedAssetA: decimalString(i.RetainedAssetA),
		RetainedAssetB: decimalString(i.RetainedAssetB),
		RemainingAmt:   decimalString(i.RemainingAmt),
		RemainingValue: i.RemainingValue,
		OutTxID:        i.OutTxID,
		OutAmt:         decimalString(i.OutAmt),
		OutValue:       i.OutValue,
		RefundItemIDs:  i.RefundItemIDs,
		Reason:         i.Reason,
		Done:           i.Done,
	})
}

func (i *InvokeItem) UnmarshalJSON(data []byte) error {
	var item invokeItemJSON
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	i.ID = item.ID
	i.CallID = item.CallID
	i.Action = item.Action
	i.OrderType = item.OrderType
	i.Height = item.Height
	i.OrderTime = item.OrderTime
	i.AssetName = item.AssetName
	i.ServiceFee = item.ServiceFee
	i.UnitPrice = item.UnitPrice
	i.ExpectedAmt = nil
	if item.ExpectedAmt != "" {
		var err error
		i.ExpectedAmt, err = parseStateDecimal("expectedAmt", item.ExpectedAmt)
		if err != nil {
			return err
		}
	}
	i.Address = item.Address
	i.InUtxos = item.InUtxos
	i.InValue = item.InValue
	i.InAmt = nil
	if item.InAmt != "" {
		var err error
		i.InAmt, err = parseStateDecimal("inAmt", item.InAmt)
		if err != nil {
			return err
		}
	}
	i.RetainedAssetA = nil
	if item.RetainedAssetA != "" {
		var err error
		i.RetainedAssetA, err = parseStateDecimal("retainedAssetA", item.RetainedAssetA)
		if err != nil {
			return err
		}
	}
	i.RetainedAssetB = nil
	if item.RetainedAssetB != "" {
		var err error
		i.RetainedAssetB, err = parseStateDecimal("retainedAssetB", item.RetainedAssetB)
		if err != nil {
			return err
		}
	}
	i.RemainingAmt = nil
	if item.RemainingAmt != "" {
		var err error
		i.RemainingAmt, err = parseStateDecimal("remainingAmt", item.RemainingAmt)
		if err != nil {
			return err
		}
	}
	i.RemainingValue = item.RemainingValue
	i.OutTxID = item.OutTxID
	i.OutAmt = nil
	if item.OutAmt != "" {
		var err error
		i.OutAmt, err = parseStateDecimal("outAmt", item.OutAmt)
		if err != nil {
			return err
		}
	}
	i.OutValue = item.OutValue
	i.RefundItemIDs = item.RefundItemIDs
	i.Reason = item.Reason
	i.Done = item.Done
	return nil
}

type RunningData struct {
	AssetAInPool      *scommon.Decimal            `json:"assetAInPool,omitempty"`
	AssetBInPool      *scommon.Decimal            `json:"assetBInPool,omitempty"`
	RequiredAssetA    *scommon.Decimal            `json:"requiredAssetA,omitempty"`
	RequiredAssetB    *scommon.Decimal            `json:"requiredAssetB,omitempty"`
	K                 *scommon.Decimal            `json:"k,omitempty"`
	TradingReady      bool                        `json:"tradingReady,omitempty"`
	GasBalance        int64                       `json:"gasBalance,omitempty"`
	TotalInputAssetA  *scommon.Decimal            `json:"totalInputAssetA,omitempty"`
	TotalInputAssetB  *scommon.Decimal            `json:"totalInputAssetB,omitempty"`
	TotalDealAssetA   *scommon.Decimal            `json:"totalDealAssetA,omitempty"`
	TotalDealAssetB   *scommon.Decimal            `json:"totalDealAssetB,omitempty"`
	TotalDealCount    int                         `json:"totalDealCount"`
	TotalRefundAssetB *scommon.Decimal            `json:"totalRefundAssetB,omitempty"`
	TotalLPTAmt       *scommon.Decimal            `json:"totalLptAmt,omitempty"`
	LPBalances        map[string]*scommon.Decimal `json:"lpBalances,omitempty"`
	LPCosts           map[string]int64            `json:"lpCosts,omitempty"`
	Closed            bool                        `json:"closed,omitempty"`
}

func (r *RunningData) Apply(item *InvokeItem) {
	if item == nil {
		return
	}
	r.applyDefaultInvokeRetention(item)
	if item.Reason == InvokeReasonInvalid {
		return
	}
	switch item.OrderType {
	}
	if r.TotalInputAssetB == nil {
		r.TotalInputAssetB = parseDecimalOrZero("0")
	}
	r.TotalInputAssetB = scommon.DecimalAdd(r.TotalInputAssetB, scommon.NewDefaultDecimal(item.InValue))
	if item.InAmt != nil {
		if r.TotalInputAssetA == nil {
			r.TotalInputAssetA = parseDecimalOrZero("0")
		}
		r.TotalInputAssetA = scommon.DecimalAdd(r.TotalInputAssetA, item.InAmt)
	}
	switch item.OrderType {
	case OrderTypeBuy, OrderTypeSell:
		if item.Done == ItemStatusDealt {
			if item.OutAmt != nil {
				if r.TotalDealAssetA == nil {
					r.TotalDealAssetA = parseDecimalOrZero("0")
				}
				r.TotalDealAssetA = scommon.DecimalAdd(r.TotalDealAssetA, item.OutAmt)
			}
			if r.TotalDealAssetB == nil {
				r.TotalDealAssetB = parseDecimalOrZero("0")
			}
			r.TotalDealAssetB = scommon.DecimalAdd(r.TotalDealAssetB, scommon.NewDefaultDecimal(item.OutValue))
			r.TotalDealCount++
		} else if item.Done == ItemStatusRefunded || item.Done == ItemStatusCancelled {
			if r.TotalRefundAssetB == nil {
				r.TotalRefundAssetB = parseDecimalOrZero("0")
			}
			r.TotalRefundAssetB = scommon.DecimalAdd(r.TotalRefundAssetB, scommon.NewDefaultDecimal(item.OutValue+item.RemainingValue))
		}
	case OrderTypeRefund:
		if r.TotalRefundAssetB == nil {
			r.TotalRefundAssetB = parseDecimalOrZero("0")
		}
		r.TotalRefundAssetB = scommon.DecimalAdd(r.TotalRefundAssetB, scommon.NewDefaultDecimal(item.OutValue+item.RemainingValue))
	}
}

func (r *RunningData) ApplyForContract(contract Contract, item *InvokeItem) {
	if applier, ok := contract.(RunningDataApplier); ok && applier.ApplyRunningData(r, item) {
		return
	}
	r.Apply(item)
}

func (r *RunningData) applyDefaultInvokeRetention(item *InvokeItem) {
	if item.RetainedAssetA != nil && item.RetainedAssetA.Sign() > 0 {
		if r.AssetAInPool == nil {
			r.AssetAInPool = parseDecimalOrZero("0")
		}
		r.AssetAInPool = scommon.DecimalAdd(r.AssetAInPool, item.RetainedAssetA)
	}
	if item.RetainedAssetB != nil && item.RetainedAssetB.Sign() > 0 {
		if r.AssetBInPool == nil {
			r.AssetBInPool = parseDecimalOrZero("0")
		}
		r.AssetBInPool = scommon.DecimalAdd(r.AssetBInPool, item.RetainedAssetB)
	}
}

func (r *ContractRuntime) ApplyDefaultInvoke(req ApplyInvokeRequest) (*InvokeItem, error) {
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	retention := defaultInvokeRetention{}
	if req.ApplyDefaultRetention {
		var fundingOutputs []ContractOutput
		retention, fundingOutputs, err = retainDefaultInvokeFunding(r.contract, req.FundingOutputs)
		if err != nil {
			return nil, err
		}
		req.FundingOutputs = fundingOutputs
	}
	item, err := NewDefaultInvokeItemFromRequest(r.contract, state.NextItemID, state, req)
	if err != nil || item == nil {
		return nil, err
	}
	item.RetainedAssetA = retention.AssetA
	item.RetainedAssetB = retention.AssetB
	state.NextItemID++
	state.InvokeCount++
	state.Items = append(state.Items, *item)
	state.Running.ApplyForContract(r.contract, item)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *ContractRuntime) ApplyInvalidInvoke(req ApplyInvokeRequest, gasAssetName string) (*InvokeItem, error) {
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	retention, gasBalance, inValue, inAmt, inUtxos, err := invalidInvokeFunding(
		r.contract, req.FundingOutputs, gasAssetName, req.ResultGasFee)
	if err != nil {
		return nil, err
	}
	item := &InvokeItem{
		ID:             state.NextItemID,
		CallID:         req.CallID,
		Action:         req.Action,
		OrderType:      OrderTypeUnused,
		Height:         req.Height,
		OrderTime:      req.Timestamp,
		AssetName:      contractAssetName(r.contract),
		Address:        req.Invoker,
		InUtxos:        inUtxos,
		InValue:        inValue,
		InAmt:          inAmt,
		RetainedAssetA: retention.AssetA,
		RetainedAssetB: retention.AssetB,
		ServiceFee:     int64(req.ResultGasFee),
		Reason:         InvokeReasonInvalid,
		Done:           ItemStatusInit,
	}
	state.NextItemID++
	state.InvokeCount++
	state.Items = append(state.Items, *item)
	state.Running.ApplyForContract(r.contract, item)
	state.Running.GasBalance += gasBalance
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return item, nil
}

type defaultInvokeRetention struct {
	AssetA *scommon.Decimal
	AssetB *scommon.Decimal
}

func retainDefaultInvokeFunding(contract Contract, outputs []ContractOutput) (defaultInvokeRetention, []ContractOutput, error) {
	assetA, assetB := defaultInvokePoolAssets(contract)
	retention := defaultInvokeRetention{}
	adjusted := make([]ContractOutput, 0, len(outputs))
	for _, output := range outputs {
		next := output
		next.Assets = output.Assets.Clone()
		if assetA != "" {
			fee, err := retainDefaultInvokeAsset(&next, assetA)
			if err != nil {
				return defaultInvokeRetention{}, nil, err
			}
			retention.AssetA = decimalAddAllowNil(retention.AssetA, fee)
		}
		if assetB == SatoshiAssetName {
			fee := next.Value / 100
			if fee > 0 {
				next.Value -= fee
				retention.AssetB = decimalAddAllowNil(retention.AssetB, scommon.NewDefaultDecimal(fee))
			}
		} else if assetB != "" && assetB != assetA {
			fee, err := retainDefaultInvokeAsset(&next, assetB)
			if err != nil {
				return defaultInvokeRetention{}, nil, err
			}
			retention.AssetB = decimalAddAllowNil(retention.AssetB, fee)
		}
		adjusted = append(adjusted, next)
	}
	return retention, adjusted, nil
}

func invalidInvokeFunding(contract Contract, outputs []ContractOutput, gasAssetName string, resultGasFee uint64) (
	defaultInvokeRetention, int64, int64, *scommon.Decimal, string, error) {

	assetA, assetB := defaultInvokePoolAssets(contract)
	retention := defaultInvokeRetention{}
	gasBalance := int64(0)
	inValue := int64(0)
	inAmt := parseDecimalOrZero("0")
	inUtxos := ""
	for i, output := range outputs {
		inValue += output.Value
		if i > 0 {
			inUtxos += ","
		}
		inUtxos += output.OutPoint.String()
		if assetA != "" {
			amt, err := output.AssetAmount(assetA)
			if err != nil {
				return defaultInvokeRetention{}, 0, 0, nil, "", err
			}
			amt = parseDecimalOrZero(amt.String())
			retention.AssetA = decimalAddAllowNil(retention.AssetA, amt)
			inAmt = scommon.DecimalAdd(inAmt, amt)
		}
		if assetB == SatoshiAssetName {
			retention.AssetB = decimalAddAllowNil(retention.AssetB, scommon.NewDefaultDecimal(output.Value))
		} else if assetB != "" && assetB != assetA {
			amt, err := output.AssetAmount(assetB)
			if err != nil {
				return defaultInvokeRetention{}, 0, 0, nil, "", err
			}
			amt = parseDecimalOrZero(amt.String())
			retention.AssetB = decimalAddAllowNil(retention.AssetB, amt)
		}
		if gasAssetName != "" && gasAssetName != assetA && gasAssetName != assetB {
			gas, err := output.AssetAmount(gasAssetName)
			if err != nil {
				return defaultInvokeRetention{}, 0, 0, nil, "", err
			}
			gasBalance += gas.Int64()
		}
	}
	if resultGasFee != 0 {
		fee := scommon.NewDefaultDecimal(int64(resultGasFee))
		switch gasAssetName {
		case assetA:
			var err error
			retention.AssetA, err = subtractRetainedGasFee("asset A", retention.AssetA, fee)
			if err != nil {
				return defaultInvokeRetention{}, 0, 0, nil, "", err
			}
		case assetB:
			var err error
			retention.AssetB, err = subtractRetainedGasFee("asset B", retention.AssetB, fee)
			if err != nil {
				return defaultInvokeRetention{}, 0, 0, nil, "", err
			}
		default:
			if gasBalance < int64(resultGasFee) {
				return defaultInvokeRetention{}, 0, 0, nil, "", fmt.Errorf("insufficient invalid invoke gas balance")
			}
			gasBalance -= int64(resultGasFee)
		}
	}
	if inAmt.Sign() == 0 {
		inAmt = nil
	}
	return retention, gasBalance, inValue, inAmt, inUtxos, nil
}

func subtractRetainedGasFee(label string, amt, fee *scommon.Decimal) (*scommon.Decimal, error) {
	if fee == nil || fee.Sign() <= 0 {
		return amt, nil
	}
	if amt == nil {
		amt = parseDecimalOrZero("0")
	}
	if amt.Cmp(fee) < 0 {
		return nil, fmt.Errorf("insufficient invalid invoke %s for result gas", label)
	}
	return scommon.DecimalSub(amt, fee), nil
}

func defaultInvokePoolAssets(contract Contract) (string, string) {
	assetA := contractAssetName(contract)
	assetB := SatoshiAssetName
	if c, ok := contract.(*ExchangeContract); ok {
		assetA = c.AssetAName
		assetB = c.AssetBName
	}
	return assetA, assetB
}

func retainDefaultInvokeAsset(output *ContractOutput, assetName string) (*scommon.Decimal, error) {
	amt, err := output.AssetAmount(assetName)
	if err != nil {
		return nil, err
	}
	if amt == nil || amt.Sign() <= 0 {
		return nil, nil
	}
	fee := defaultInvokeAssetRetainFee(amt)
	if fee == nil || fee.Sign() <= 0 {
		return nil, nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil, ErrInvalidAsset
	}
	if err := output.Assets.Split(wire.TxAssets{{Name: *name, Amount: *fee}}); err != nil {
		return nil, err
	}
	return fee, nil
}

func defaultInvokeAssetRetainFee(amt *scommon.Decimal) *scommon.Decimal {
	if amt == nil || amt.Sign() <= 0 {
		return nil
	}
	return scommon.DecimalMulV2(amt, scommon.NewDecimal(1, amt.Precision+2)).
		Div(scommon.NewDecimal(100, amt.Precision+2))
}

func decimalAddAllowNil(a, b *scommon.Decimal) *scommon.Decimal {
	if b == nil || b.Sign() == 0 {
		return a
	}
	if a == nil {
		return b.Clone()
	}
	return scommon.DecimalAdd(a, b)
}

func NewDefaultInvokeItemFromRequest(contract Contract, id int64, state TemplateRuntimeState, req ApplyInvokeRequest) (*InvokeItem, error) {
	if c, ok := contract.(*ExchangeContract); ok {
		inputA, inputB, inUtxos, err := exchangeFundingAmounts(c, req.FundingOutputs)
		if err != nil {
			return nil, err
		}
		if inputA.Sign() == 0 && inputB.Sign() == 0 {
			return nil, nil
		}
		item := newExchangeItem(id, contractcommon.ContractInvokeAPIDefault, req, inUtxos, c.AssetBName, inputB, "")
		if inputB.Sign() == 0 {
			item.OrderType = OrderTypeFund
			item.AssetName = c.AssetAName
			item.InAmt = inputA
			item.RemainingAmt = item.InAmt
		} else if inputA.Sign() > 0 {
			item.OutAmt = inputA
		}
		return item, nil
	}
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
		InAmt:          inAmt,
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
	inAmt := parseDecimalOrZero("0")
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
			amt = parseDecimalOrZero(amt.String())
			inAmt = scommon.DecimalAdd(inAmt, amt)
		}
	}
	if inAmt.Sign() == 0 {
		inAmt = nil
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
		if item.RemainingAmt == nil || item.RemainingAmt.Sign() <= 0 {
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
	inAmt := parseDecimalOrZero("0")
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
			amt = parseDecimalOrZero(amt.String())
			inAmt = scommon.DecimalAdd(inAmt, amt)
		}
	}
	if inAmt.Sign() == 0 {
		inAmt = nil
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
		InAmt:      inAmt,
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
		if param.Amt != "" {
			item.ExpectedAmt = parseDecimalOrZero(param.Amt)
		}
		item.RemainingValue = inValue
		if item.OrderType == OrderTypeSell {
			item.RemainingAmt = item.InAmt
			item.RemainingValue = 0
		} else {
			if param.Amt != "" {
				item.RemainingAmt = parseDecimalOrZero(param.Amt)
			}
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
		item.InAmt = nil
		item.RemainingValue = inValue
		item.RefundItemIDs = append([]int64(nil), param.ItemIDs...)
	case InvokeAPIAddLiquidity:
		var param AddLiquidityInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		item.OrderType = param.OrderType
		item.AssetName = firstNonEmpty(param.AssetName, assetName)
		if param.Amt != "" {
			item.ExpectedAmt = parseDecimalOrZero(param.Amt)
			item.RemainingAmt = parseDecimalOrZero(param.Amt)
		}
		item.RemainingValue = param.Value
	case InvokeAPIRemoveLiquidity:
		var param RemoveLiquidityInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		item.OrderType = param.OrderType
		item.AssetName = firstNonEmpty(param.AssetName, assetName)
		if param.LptAmt != "" {
			item.ExpectedAmt = parseDecimalOrZero(param.LptAmt)
			item.RemainingAmt = parseDecimalOrZero(param.LptAmt)
		}
	case InvokeAPIExchange:
		contract, ok := contract.(*ExchangeContract)
		if !ok {
			return nil, fmt.Errorf("exchange action requires exchange contract")
		}
		var param ExchangeInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		inputA, inputB, inUtxos, err := exchangeFundingAmounts(contract, req.FundingOutputs)
		if err != nil {
			return nil, err
		}
		item = newExchangeItem(id, req.Action, req, inUtxos, contract.AssetBName, inputB, param.MinOutA)
		if inputA.Sign() > 0 {
			item.OutAmt = inputA
		}
	case InvokeAPIClose:
		if exchange, ok := contract.(*ExchangeContract); ok {
			inputA, inputB, inUtxos, err := exchangeFundingAmounts(exchange, req.FundingOutputs)
			if err != nil {
				return nil, err
			}
			item = &InvokeItem{
				ID:             id,
				CallID:         req.CallID,
				Action:         req.Action,
				OrderType:      OrderTypeClose,
				Height:         req.Height,
				OrderTime:      req.Timestamp,
				AssetName:      exchange.AssetAName,
				Address:        req.Invoker,
				InUtxos:        inUtxos,
				InAmt:          inputA,
				RemainingAmt:   inputB,
				ServiceFee:     int64(req.ResultGasFee),
				Reason:         InvokeReasonNormal,
				Done:           ItemStatusInit,
				RemainingValue: fundingValue(req.FundingOutputs),
			}
			break
		}
		item = &InvokeItem{
			ID:             id,
			CallID:         req.CallID,
			Action:         req.Action,
			OrderType:      OrderTypeClose,
			Height:         req.Height,
			OrderTime:      req.Timestamp,
			AssetName:      assetName,
			Address:        req.Invoker,
			InUtxos:        inUtxos,
			InAmt:          inAmt,
			ServiceFee:     int64(req.ResultGasFee),
			Reason:         InvokeReasonNormal,
			Done:           ItemStatusInit,
			RemainingValue: fundingValue(req.FundingOutputs),
		}
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
			i.RemainingAmt = nil
			return
		}
		if i.InAmt == nil || i.InAmt.Sign() == 0 {
			i.Reason = InvokeReasonInvalid
			return
		}
		if !isAMM && i.InAmt.Cmp(parseDecimalOrZero(param.Amt)) != 0 {
			i.Reason = InvokeReasonInvalid
			i.RemainingAmt = nil
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
				RequiredAssetA: parseDecimalOrZero(c.AssetAmt),
				RequiredAssetB: scommon.NewDefaultDecimal(c.SatValue),
				K:              parseDecimalOrZero(c.K),
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
	if applier, ok := r.contract.(FundingStateApplier); ok {
		handled, err := applier.ApplyFundingState(&state, outputs, gasAssetName)
		if err != nil || handled {
			if err != nil {
				return err
			}
			return r.saveRuntimeState(state)
		}
	}
	assetName := contractAssetName(r.contract)
	for _, output := range outputs {
		if _, ok := r.contract.(*AMMContract); ok {
			if state.Running.AssetBInPool == nil {
				state.Running.AssetBInPool = parseDecimalOrZero("0")
			}
			state.Running.AssetBInPool = scommon.DecimalAdd(state.Running.AssetBInPool, scommon.NewDefaultDecimal(output.Value))
			if assetName != "" {
				amt, err := output.AssetAmount(assetName)
				if err != nil {
					return err
				}
				amt = parseDecimalOrZero(amt.String())
				if state.Running.AssetAInPool == nil {
					state.Running.AssetAInPool = parseDecimalOrZero("0")
				}
				state.Running.AssetAInPool = scommon.DecimalAdd(state.Running.AssetAInPool, amt)
			}
		}
		if gasAssetName != "" {
			gas, err := output.AssetAmount(gasAssetName)
			if err != nil {
				return err
			}
			state.Running.GasBalance += gas.Int64()
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
	if applier, ok := r.contract.(GasFundingStateApplier); ok {
		handled, err := applier.ApplyGasFundingState(&state, outputs, gasAssetName)
		if err != nil || handled {
			if err != nil {
				return err
			}
			return r.saveRuntimeState(state)
		}
	}
	for _, output := range outputs {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return err
		}
		state.Running.GasBalance += gas.Int64()
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
	case *ExchangeContract:
		return c.AssetAName
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

func parseDecimalOrZero(value string) *scommon.Decimal {
	if value == "" {
		return scommon.NewDecimal(0, MaxPriceDivisibility)
	}
	d, err := scommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return scommon.NewDecimal(0, MaxPriceDivisibility)
	}
	return d
}

func parseStateDecimal(field, value string) (*scommon.Decimal, error) {
	d, err := scommon.NewDecimalFromString(value, MaxPriceDivisibility)
	if err != nil {
		return nil, fmt.Errorf("invalid %s decimal %q: %w", field, value, err)
	}
	return d, nil
}

func decimalInt64(value *scommon.Decimal) int64 {
	if value == nil {
		return 0
	}
	return value.Int64()
}

func (r RunningData) ammTradingReady() bool {
	requiredAssetA := r.RequiredAssetA
	if requiredAssetA == nil {
		requiredAssetA = parseDecimalOrZero("0")
	}
	if requiredAssetA.Sign() == 0 {
		return true
	}
	assetA := r.AssetAInPool
	if assetA == nil {
		assetA = parseDecimalOrZero("0")
	}
	assetB := r.AssetBInPool
	if assetB == nil {
		assetB = parseDecimalOrZero("0")
	}
	requiredAssetB := r.RequiredAssetB
	if requiredAssetB == nil {
		requiredAssetB = parseDecimalOrZero("0")
	}
	if assetA.Cmp(requiredAssetA) < 0 || assetB.Cmp(requiredAssetB) < 0 {
		return false
	}
	k := r.K
	if k == nil {
		k = parseDecimalOrZero("0")
	}
	if k.Sign() == 0 {
		return true
	}
	currentK := scommon.DecimalMul(assetA, assetB)
	return currentK.Cmp(k) >= 0
}
