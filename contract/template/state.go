package template

import (
	"encoding/json"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

const runtimeStateKey = "template-runtime-state"

type ApplyInvokeRequest struct {
	Action                string
	Param                 []byte
	CallID                string
	Invoker               string
	FundingOutput         ContractOutput
	Height                int64
	Timestamp             int64
	ResultGasFee          *scommon.Decimal
	ApplyDefaultRetention bool
}

type TemplateRuntimeState struct {
	NextItemID  int64                  `json:"nextItemId"`
	InvokeCount uint64                 `json:"invokeCount"`
	Items       []InvokeItem           `json:"items,omitempty"`
	LimitOrder  *LimitOrderRunningData `json:"limitOrder,omitempty"`
	AMM         *AMMRunningData        `json:"amm,omitempty"`
	Exchange    *ExchangeRunningData   `json:"exchange,omitempty"`
	Autopay     *AutopayRunningData    `json:"autopay,omitempty"`
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
	GasFee         *scommon.Decimal `json:"gasFee,omitempty"`
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
	StatsApplied   bool             `json:"statsApplied,omitempty"`
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
	GasFee         string  `json:"gasFee,omitempty"`
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
	StatsApplied   bool    `json:"statsApplied,omitempty"`
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
		GasFee:         decimalString(i.GasFee),
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
		StatsApplied:   i.StatsApplied,
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
	i.GasFee = nil
	if item.GasFee != "" {
		var err error
		i.GasFee, err = parseGasStateDecimal("gasFee", item.GasFee)
		if err != nil {
			return err
		}
	}
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
	i.StatsApplied = item.StatsApplied
	return nil
}

type LimitOrderRunningData struct {
	AssetAInPool      *scommon.Decimal `json:"assetAInPool,omitempty"`
	AssetBInPool      *scommon.Decimal `json:"assetBInPool,omitempty"`
	TradingReady      bool             `json:"tradingReady,omitempty"`
	GasBalance        *scommon.Decimal `json:"gasBalance,omitempty"`
	TotalInputAssetA  *scommon.Decimal `json:"totalInputAssetA,omitempty"`
	TotalInputAssetB  *scommon.Decimal `json:"totalInputAssetB,omitempty"`
	TotalDealAssetA   *scommon.Decimal `json:"totalDealAssetA,omitempty"`
	TotalDealAssetB   *scommon.Decimal `json:"totalDealAssetB,omitempty"`
	TotalDealCount    int              `json:"totalDealCount,omitempty"`
	TotalRefundAssetB *scommon.Decimal `json:"totalRefundAssetB,omitempty"`
	Closed            bool             `json:"closed,omitempty"`
}

type AMMRunningData struct {
	AssetAInPool      *scommon.Decimal            `json:"assetAInPool,omitempty"`
	AssetBInPool      *scommon.Decimal            `json:"assetBInPool,omitempty"`
	RequiredAssetA    *scommon.Decimal            `json:"requiredAssetA,omitempty"`
	RequiredAssetB    *scommon.Decimal            `json:"requiredAssetB,omitempty"`
	K                 *scommon.Decimal            `json:"k,omitempty"`
	TradingReady      bool                        `json:"tradingReady,omitempty"`
	GasBalance        *scommon.Decimal            `json:"gasBalance,omitempty"`
	TotalInputAssetA  *scommon.Decimal            `json:"totalInputAssetA,omitempty"`
	TotalInputAssetB  *scommon.Decimal            `json:"totalInputAssetB,omitempty"`
	TotalDealAssetA   *scommon.Decimal            `json:"totalDealAssetA,omitempty"`
	TotalDealAssetB   *scommon.Decimal            `json:"totalDealAssetB,omitempty"`
	TotalDealCount    int                         `json:"totalDealCount,omitempty"`
	TotalRefundAssetB *scommon.Decimal            `json:"totalRefundAssetB,omitempty"`
	TotalLPTAmt       *scommon.Decimal            `json:"totalLptAmt,omitempty"`
	LPBalances        map[string]*scommon.Decimal `json:"lpBalances,omitempty"`
	LPCosts           map[string]int64            `json:"lpCosts,omitempty"`
	Closed            bool                        `json:"closed,omitempty"`
}

type ExchangeRunningData struct {
	AssetAInPool      *scommon.Decimal `json:"assetAInPool,omitempty"`
	AssetBInPool      *scommon.Decimal `json:"assetBInPool,omitempty"`
	GasBalance        *scommon.Decimal `json:"gasBalance,omitempty"`
	TotalInputAssetA  *scommon.Decimal `json:"totalInputAssetA,omitempty"`
	TotalInputAssetB  *scommon.Decimal `json:"totalInputAssetB,omitempty"`
	TotalDealAssetA   *scommon.Decimal `json:"totalDealAssetA,omitempty"`
	TotalDealAssetB   *scommon.Decimal `json:"totalDealAssetB,omitempty"`
	TotalDealCount    int              `json:"totalDealCount,omitempty"`
	TotalRefundAssetB *scommon.Decimal `json:"totalRefundAssetB,omitempty"`
	Closed            bool             `json:"closed,omitempty"`
}

type AutopayRunningData struct {
	GasBalance          *scommon.Decimal           `json:"gasBalance,omitempty"`
	Closed              bool                       `json:"closed,omitempty"`
	FeeBalance          *scommon.Decimal           `json:"feeBalance,omitempty"`
	AutopayStatus       string                     `json:"autopayStatus,omitempty"`
	ActiveHeight        int64                      `json:"activeHeight,omitempty"`
	NextPayHeight       int64                      `json:"nextPayHeight,omitempty"`
	LastPayHeight       int64                      `json:"lastPayHeight,omitempty"`
	PaidBlockCount      int64                      `json:"paidBlockCount,omitempty"`
	AutopayCloseStarted bool                       `json:"autopayCloseStarted,omitempty"`
	AutopayDelegates    map[string]AutopayDelegate `json:"autopayDelegates,omitempty"`
}

type AutopayDelegate struct {
	AmountPerBlock *scommon.Decimal `json:"amountPerBlock,omitempty"`
	Balance        *scommon.Decimal `json:"balance,omitempty"`
	TotalPaid      *scommon.Decimal `json:"totalPaid,omitempty"`
	PaidBlockCount int64            `json:"paidBlockCount,omitempty"`
	LastPayHeight  int64            `json:"lastPayHeight,omitempty"`
	Status         string           `json:"status,omitempty"`
}

type autopayDelegateJSON struct {
	AmountPerBlock string `json:"amountPerBlock,omitempty"`
	Balance        string `json:"balance,omitempty"`
	TotalPaid      string `json:"totalPaid,omitempty"`
	PaidBlockCount int64  `json:"paidBlockCount,omitempty"`
	LastPayHeight  int64  `json:"lastPayHeight,omitempty"`
	Status         string `json:"status,omitempty"`
}

func (s *TemplateRuntimeState) LimitOrderData() *LimitOrderRunningData {
	if s == nil {
		return nil
	}
	if s.LimitOrder == nil {
		s.LimitOrder = &LimitOrderRunningData{}
	}
	return s.LimitOrder
}

func (s *TemplateRuntimeState) AMMData() *AMMRunningData {
	if s == nil {
		return nil
	}
	if s.AMM == nil {
		s.AMM = &AMMRunningData{}
	}
	return s.AMM
}

func (s *TemplateRuntimeState) ExchangeData() *ExchangeRunningData {
	if s == nil {
		return nil
	}
	if s.Exchange == nil {
		s.Exchange = &ExchangeRunningData{}
	}
	return s.Exchange
}

func (s *TemplateRuntimeState) AutopayData() *AutopayRunningData {
	if s == nil {
		return nil
	}
	if s.Autopay == nil {
		s.Autopay = &AutopayRunningData{}
	}
	return s.Autopay
}

func parseDecimalOrNil(value string) *scommon.Decimal {
	if value == "" {
		return nil
	}
	return parseDecimalOrZero(value)
}

func parseGasDecimalOrNil(value string) *scommon.Decimal {
	if value == "" {
		return nil
	}
	d, err := parseGasStateDecimal("gas", value)
	if err != nil {
		return nil
	}
	return d
}

func (s *TemplateRuntimeState) ClosedForContract(contract Contract) bool {
	if s == nil {
		return false
	}
	switch contract.(type) {
	case *LimitOrderContract:
		return s.LimitOrderData().Closed
	case *AMMContract:
		return s.AMMData().Closed
	case *ExchangeContract:
		return s.ExchangeData().Closed
	case *AutopayContract:
		return s.AutopayData().Closed
	default:
		return false
	}
}

func (s *TemplateRuntimeState) GasBalanceForContract(contract Contract) *scommon.Decimal {
	if s == nil {
		return nil
	}
	switch contract.(type) {
	case *LimitOrderContract:
		return s.LimitOrderData().GasBalance
	case *AMMContract:
		return s.AMMData().GasBalance
	case *ExchangeContract:
		return s.ExchangeData().GasBalance
	case *AutopayContract:
		return s.AutopayData().GasBalance
	default:
		return nil
	}
}

func addRunningGasBalance(contract Contract, state *TemplateRuntimeState, gas *scommon.Decimal) {
	if state == nil || gas == nil || gas.Sign() == 0 {
		return
	}
	switch contract.(type) {
	case *LimitOrderContract:
		data := state.LimitOrderData()
		data.GasBalance = decimalAddAllowNil(data.GasBalance, gas)
	case *AMMContract:
		data := state.AMMData()
		data.GasBalance = decimalAddAllowNil(data.GasBalance, gas)
	case *ExchangeContract:
		data := state.ExchangeData()
		data.GasBalance = decimalAddAllowNil(data.GasBalance, gas)
	case *AutopayContract:
		data := state.AutopayData()
		data.GasBalance = decimalAddAllowNil(data.GasBalance, gas)
	}
}

func (s *TemplateRuntimeState) PoolBalancesForContract(contract Contract) (*scommon.Decimal, *scommon.Decimal) {
	if s == nil {
		return nil, nil
	}
	switch contract.(type) {
	case *LimitOrderContract:
		data := s.LimitOrderData()
		return data.AssetAInPool, data.AssetBInPool
	case *AMMContract:
		data := s.AMMData()
		return data.AssetAInPool, data.AssetBInPool
	case *ExchangeContract:
		data := s.ExchangeData()
		return data.AssetAInPool, data.AssetBInPool
	default:
		return nil, nil
	}
}

func applyDefaultInvokeRetentionToLimitOrder(r *LimitOrderRunningData, item *InvokeItem) {
	if r == nil || item == nil {
		return
	}
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

func applyDefaultInvokeRetentionToAMM(r *AMMRunningData, item *InvokeItem) {
	if r == nil || item == nil {
		return
	}
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

func applyDefaultInvokeRetentionToExchange(r *ExchangeRunningData, item *InvokeItem) {
	if r == nil || item == nil {
		return
	}
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

func applyLimitOrderRunningStats(r *LimitOrderRunningData, item *InvokeItem) {
	if item == nil {
		return
	}
	if item.Reason == InvokeReasonInvalid {
		return
	}
	applyDefaultInvokeRetentionToLimitOrder(r, item)
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

func applyAMMRunningStats(r *AMMRunningData, item *InvokeItem) {
	if item == nil {
		return
	}
	if item.Reason == InvokeReasonInvalid {
		return
	}
	applyDefaultInvokeRetentionToAMM(r, item)
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

func (c *LimitOrderContract) ApplyRunningData(state *TemplateRuntimeState, item *InvokeItem) bool {
	if state == nil {
		return true
	}
	running := state.LimitOrderData()
	applyLimitOrderRunningStats(running, item)
	if item == nil || item.Reason == InvokeReasonInvalid {
		return true
	}
	switch item.OrderType {
	case OrderTypeBuy:
		if item.RemainingValue > 0 {
			if running.AssetBInPool == nil {
				running.AssetBInPool = parseDecimalOrZero("0")
			}
			running.AssetBInPool = scommon.DecimalAdd(running.AssetBInPool, scommon.NewDefaultDecimal(item.RemainingValue))
		}
	case OrderTypeSell:
		if item.RemainingAmt != nil && item.RemainingAmt.Sign() > 0 {
			if running.AssetAInPool == nil {
				running.AssetAInPool = parseDecimalOrZero("0")
			}
			running.AssetAInPool = scommon.DecimalAdd(running.AssetAInPool, item.RemainingAmt)
		}
	}
	return true
}

func (s *TemplateRuntimeState) ApplyForContract(contract Contract, item *InvokeItem) {
	if applier, ok := contract.(RuntimeStateApplier); ok && applier.ApplyRunningData(s, item) {
		return
	}
}

func (s *TemplateRuntimeState) recomputeActivePools(contract Contract) {
	if s == nil {
		return
	}
	switch contract.(type) {
	case *LimitOrderContract:
		running := s.LimitOrderData()
		running.AssetAInPool = nil
		running.AssetBInPool = nil
		for i := range s.Items {
			item := &s.Items[i]
			if item.Finished() || item.Reason != InvokeReasonNormal {
				continue
			}
			switch item.OrderType {
			case OrderTypeBuy:
				if item.RemainingValue > 0 {
					running.AssetBInPool = decimalAddAllowNil(running.AssetBInPool, scommon.NewDefaultDecimal(item.RemainingValue))
				}
			case OrderTypeSell:
				if item.RemainingAmt != nil && item.RemainingAmt.Sign() > 0 {
					running.AssetAInPool = decimalAddAllowNil(running.AssetAInPool, item.RemainingAmt)
				}
			}
		}
	}
}

func (s *TemplateRuntimeState) applyFinishedItemStats(contract Contract) {
	if s == nil {
		return
	}
	for i := range s.Items {
		item := &s.Items[i]
		if !item.Finished() || item.StatsApplied {
			continue
		}
		switch contract.(type) {
		case *LimitOrderContract:
			applyLimitOrderFinishedStats(s.LimitOrderData(), item)
		case *AMMContract:
			applyAMMFinishedStats(s.AMMData(), item)
		case *ExchangeContract:
			applyExchangeFinishedStats(s.ExchangeData(), item)
		}
		item.StatsApplied = true
	}
}

func applyLimitOrderFinishedStats(r *LimitOrderRunningData, item *InvokeItem) {
	if r == nil || item == nil || item.Reason == InvokeReasonInvalid {
		return
	}
	switch item.OrderType {
	case OrderTypeBuy, OrderTypeSell:
		switch item.Done {
		case ItemStatusDealt:
			addDealStats(&r.TotalDealAssetA, &r.TotalDealAssetB, &r.TotalDealCount, item)
		case ItemStatusRefunded, ItemStatusCancelled:
			addRefundStats(&r.TotalRefundAssetB, item)
		}
	case OrderTypeRefund:
		addRefundStats(&r.TotalRefundAssetB, item)
	}
}

func applyAMMFinishedStats(r *AMMRunningData, item *InvokeItem) {
	if r == nil || item == nil || item.Reason == InvokeReasonInvalid {
		return
	}
	switch item.OrderType {
	case OrderTypeBuy, OrderTypeSell:
		switch item.Done {
		case ItemStatusDealt:
			addDealStats(&r.TotalDealAssetA, &r.TotalDealAssetB, &r.TotalDealCount, item)
		case ItemStatusRefunded, ItemStatusCancelled:
			addRefundStats(&r.TotalRefundAssetB, item)
		}
	case OrderTypeRefund:
		addRefundStats(&r.TotalRefundAssetB, item)
	}
}

func applyExchangeFinishedStats(r *ExchangeRunningData, item *InvokeItem) {
	if r == nil || item == nil || item.Reason == InvokeReasonInvalid {
		return
	}
	if item.Done == ItemStatusRefunded || item.Done == ItemStatusCancelled || item.OrderType == OrderTypeRefund {
		addRefundStats(&r.TotalRefundAssetB, item)
	}
}

func addDealStats(totalA **scommon.Decimal, totalB **scommon.Decimal, count *int, item *InvokeItem) {
	if item.OutAmt != nil {
		*totalA = decimalAddAllowNil(*totalA, item.OutAmt)
	}
	*totalB = decimalAddAllowNil(*totalB, scommon.NewDefaultDecimal(item.OutValue))
	*count = *count + 1
}

func addRefundStats(total **scommon.Decimal, item *InvokeItem) {
	if item == nil {
		return
	}
	*total = decimalAddAllowNil(*total, scommon.NewDefaultDecimal(item.OutValue+item.RemainingValue))
}

func (r *ContractRuntime) ApplyDefaultInvoke(req ApplyInvokeRequest) (*InvokeItem, error) {
	state, err := r.loadRuntimeState()
	if err != nil {
		return nil, err
	}
	retention := defaultInvokeRetention{}
	if req.ApplyDefaultRetention {
		var fundingOutput ContractOutput
		retention, fundingOutput, err = retainDefaultInvokeFunding(r.contract, req.FundingOutput)
		if err != nil {
			return nil, err
		}
		req.FundingOutput = fundingOutput
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
	state.ApplyForContract(r.contract, item)
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
		r.contract, req.FundingOutput, gasAssetName, req.ResultGasFee)
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
		Reason:         InvokeReasonInvalid,
		Done:           ItemStatusInit,
	}
	if req.ResultGasFee != nil {
		item.GasFee = req.ResultGasFee.Clone()
	}
	state.NextItemID++
	state.InvokeCount++
	state.Items = append(state.Items, *item)
	addRunningGasBalance(r.contract, &state, gasBalance)
	if err := r.saveRuntimeState(state); err != nil {
		return nil, err
	}
	return item, nil
}

type defaultInvokeRetention struct {
	AssetA *scommon.Decimal
	AssetB *scommon.Decimal
}

func retainDefaultInvokeFunding(contract Contract, output ContractOutput) (defaultInvokeRetention, ContractOutput, error) {
	if _, ok := contract.(*AutopayContract); ok {
		return defaultInvokeRetention{}, output, nil
	}
	assetA, assetB := defaultInvokePoolAssets(contract)
	retention := defaultInvokeRetention{}
	next := output
	next.TxOutput = output.IndexerTxOutput()
	if assetA != "" {
		fee, err := retainDefaultInvokeAsset(&next, assetA)
		if err != nil {
			return defaultInvokeRetention{}, ContractOutput{}, err
		}
		retention.AssetA = decimalAddAllowNil(retention.AssetA, fee)
	}
	if assetB == SatoshiAssetName {
		fee := next.PlainValue() / 100
		if fee > 0 {
			if err := next.SubAssetAmount(SatoshiAssetName, scommon.NewDefaultDecimal(fee)); err != nil {
				return defaultInvokeRetention{}, ContractOutput{}, err
			}
			retention.AssetB = decimalAddAllowNil(retention.AssetB, scommon.NewDefaultDecimal(fee))
		}
	} else if assetB != "" && assetB != assetA {
		fee, err := retainDefaultInvokeAsset(&next, assetB)
		if err != nil {
			return defaultInvokeRetention{}, ContractOutput{}, err
		}
		retention.AssetB = decimalAddAllowNil(retention.AssetB, fee)
	}
	return retention, next, nil
}

func invalidInvokeFunding(contract Contract, output ContractOutput, gasAssetName string, resultGasFee *scommon.Decimal) (
	defaultInvokeRetention, *scommon.Decimal, int64, *scommon.Decimal, string, error) {

	assetA, assetB := defaultInvokePoolAssets(contract)
	retention := defaultInvokeRetention{}
	gasBalance := (*scommon.Decimal)(nil)
	inValue := output.PlainValue()
	inAmt := parseDecimalOrZero("0")
	inUtxos := output.OutPoint.String()
	if assetA != "" {
		amt, err := output.AssetAmount(assetA)
		if err != nil {
			return defaultInvokeRetention{}, nil, 0, nil, "", err
		}
		amt = parseDecimalOrZero(amt.String())
		retention.AssetA = decimalAddAllowNil(retention.AssetA, amt)
		inAmt = scommon.DecimalAdd(inAmt, amt)
	}
	if assetB == SatoshiAssetName {
		retention.AssetB = decimalAddAllowNil(retention.AssetB, scommon.NewDefaultDecimal(inValue))
	} else if assetB != "" && assetB != assetA {
		amt, err := output.AssetAmount(assetB)
		if err != nil {
			return defaultInvokeRetention{}, nil, 0, nil, "", err
		}
		amt = parseDecimalOrZero(amt.String())
		retention.AssetB = decimalAddAllowNil(retention.AssetB, amt)
	}
	if resultGasFee != nil && resultGasFee.Sign() > 0 {
		switch gasAssetName {
		case assetA:
			var err error
			retention.AssetA, err = subtractRetainedGasFee("asset A", retention.AssetA, resultGasFee)
			if err != nil {
				return defaultInvokeRetention{}, nil, 0, nil, "", err
			}
		case assetB:
			var err error
			retention.AssetB, err = subtractRetainedGasFee("asset B", retention.AssetB, resultGasFee)
			if err != nil {
				return defaultInvokeRetention{}, nil, 0, nil, "", err
			}
		default:
			gasBalance = parseDecimalOrZero("0")
			gas, err := output.AssetAmount(gasAssetName)
			if err != nil {
				return defaultInvokeRetention{}, nil, 0, nil, "", err
			}
			gasBalance = decimalAddAllowNil(gasBalance, gas)
			if gasBalance == nil || gasBalance.Cmp(resultGasFee) < 0 {
				return defaultInvokeRetention{}, nil, 0, nil, "", fmt.Errorf("insufficient invalid invoke gas balance")
			}
			gasBalance = nil
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
	return amt.SubAlignPrecision(fee), nil
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
	if err := output.SubAssetAmount(assetName, fee); err != nil {
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
	return a.AddAlignPrecision(b)
}

func NewDefaultInvokeItemFromRequest(contract Contract, id int64, state TemplateRuntimeState, req ApplyInvokeRequest) (*InvokeItem, error) {
	if c, ok := contract.(*AutopayContract); ok {
		return NewAutopayDefaultInvokeItem(c, id, req)
	}
	if c, ok := contract.(*ExchangeContract); ok {
		inputA, inputB, inUtxos, err := exchangeFundingAmounts(c, req.FundingOutput)
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
	inValue, inAmt, inUtxos, err := defaultInvokeFunding(assetName, req.FundingOutput)
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

func defaultInvokeFunding(assetName string, output ContractOutput) (int64, *scommon.Decimal, string, error) {
	inValue := output.PlainValue()
	inAmt := parseDecimalOrZero("0")
	inUtxos := output.OutPoint.String()
	if assetName != "" {
		amt, err := output.AssetAmount(assetName)
		if err != nil {
			return 0, nil, "", err
		}
		amt = parseDecimalOrZero(amt.String())
		inAmt = scommon.DecimalAdd(inAmt, amt)
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
	output := req.FundingOutput
	inValue := output.PlainValue()
	inAmt := parseDecimalOrZero("0")
	inUtxos := output.OutPoint.String()
	assetName := contractAssetName(contract)
	if assetName != "" {
		amt, err := output.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		amt = parseDecimalOrZero(amt.String())
		inAmt = scommon.DecimalAdd(inAmt, amt)
	}
	if inAmt.Sign() == 0 {
		inAmt = nil
	}

	item := &InvokeItem{
		ID:        id,
		CallID:    req.CallID,
		Action:    req.Action,
		Height:    req.Height,
		OrderTime: req.Timestamp,
		AssetName: assetName,
		Address:   req.Invoker,
		InUtxos:   inUtxos,
		InValue:   inValue,
		InAmt:     inAmt,
		Reason:    InvokeReasonNormal,
		Done:      ItemStatusInit,
	}

	switch req.Action {
	case InvokeAPISwap:
		var param LimitOrderInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		_, isAMM := contract.(*AMMContract)
		if isAMM {
			item.ServiceFee = SwapInvokeFee
		}
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
				item.ServiceFee = calcSwapServiceFee(calcLimitOrderTradingValue(param.Amt, param.UnitPrice))
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
		item.applyAddLiquidityFundingValidation(param)
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
		inputA, inputB, inUtxos, err := exchangeFundingAmounts(contract, req.FundingOutput)
		if err != nil {
			return nil, err
		}
		item = newExchangeItem(id, req.Action, req, inUtxos, contract.AssetBName, inputB, param.MinOutA)
		if inputA.Sign() > 0 {
			item.OutAmt = inputA
		}
	case InvokeAPIConfig:
		autopay, ok := contract.(*AutopayContract)
		if !ok {
			return nil, fmt.Errorf("config action requires autopay contract")
		}
		var param AutopayConfigInvokeParam
		if err := param.Decode(req.Param); err != nil {
			return nil, err
		}
		amount := parseDecimalOrZero(param.AmountPerBlock)
		if amount.Cmp(autopay.minAmountPerBlock()) < 0 {
			item.Reason = InvokeReasonInvalid
		}
		item.OrderType = OrderTypeValidate
		item.ExpectedAmt = amount
		item.Done = ItemStatusDealt
		item.GasFee = nil
		if req.ResultGasFee != nil {
			item.GasFee = req.ResultGasFee.Clone()
		}
		item.RemainingValue = fundingValue(req.FundingOutput)
	case InvokeAPICancel:
		if _, ok := contract.(*AutopayContract); !ok {
			return nil, fmt.Errorf("cancel action requires autopay contract")
		}
		item.OrderType = OrderTypeCancel
		item.AssetName = assetName
		item.InAmt = nil
		item.GasFee = nil
		if req.ResultGasFee != nil {
			item.GasFee = req.ResultGasFee.Clone()
		}
		item.RemainingValue = fundingValue(req.FundingOutput)
	case InvokeAPIClose:
		if exchange, ok := contract.(*ExchangeContract); ok {
			inputA, inputB, inUtxos, err := exchangeFundingAmounts(exchange, req.FundingOutput)
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
				GasFee:         req.ResultGasFee.Clone(),
				Reason:         InvokeReasonNormal,
				Done:           ItemStatusInit,
				RemainingValue: fundingValue(req.FundingOutput),
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
			GasFee:         req.ResultGasFee.Clone(),
			Reason:         InvokeReasonNormal,
			Done:           ItemStatusInit,
			RemainingValue: fundingValue(req.FundingOutput),
		}
	default:
		return nil, fmt.Errorf("unsupported template action %s", req.Action)
	}
	return item, nil
}

func checkInvokeFunding(contract Contract, action string, param []byte, output ContractOutput) error {
	assetName := contractAssetName(contract)
	switch action {
	case InvokeAPISwap:
		var invokeParam LimitOrderInvokeParam
		if err := invokeParam.Decode(param); err != nil {
			return err
		}
		switch invokeParam.OrderType {
		case OrderTypeBuy:
			requiredValue := calcLimitOrderTradingValue(invokeParam.Amt, invokeParam.UnitPrice)
			if _, isAMM := contract.(*AMMContract); isAMM {
				requiredValue = parseDecimalOrZero(invokeParam.UnitPrice).Int64()
			} else {
				var overflow bool
				requiredValue, overflow = contractframework.AddInt64(requiredValue, calcSwapServiceFee(requiredValue))
				if overflow {
					return fmt.Errorf("limit order required funding overflows int64")
				}
			}
			if requiredValue <= 0 || fundingValue(output) < requiredValue {
				return fmt.Errorf("invoke funding value %d is less than declared value %d", fundingValue(output), requiredValue)
			}
		case OrderTypeSell:
			requiredAsset := firstNonEmpty(invokeParam.AssetName, assetName)
			if err := requireFundingAsset(output, requiredAsset, invokeParam.Amt); err != nil {
				return err
			}
			if got := fundingValue(output); got != SwapInvokeFee {
				return fmt.Errorf("invoke funding value %d does not match sell service fee %d", got, SwapInvokeFee)
			}
		}
	case InvokeAPIAddLiquidity:
		var invokeParam AddLiquidityInvokeParam
		if err := invokeParam.Decode(param); err != nil {
			return err
		}
		requiredAsset := firstNonEmpty(invokeParam.AssetName, assetName)
		if err := requireFundingAsset(output, requiredAsset, invokeParam.Amt); err != nil {
			return err
		}
		if invokeParam.Value <= 0 || fundingValue(output) < invokeParam.Value {
			return fmt.Errorf("invoke funding value %d is less than declared add liquidity value %d", fundingValue(output), invokeParam.Value)
		}
	}
	return nil
}

func requireFundingAsset(output ContractOutput, assetName string, amount string) error {
	if assetName == "" {
		return fmt.Errorf("missing declared funding asset")
	}
	required := parseDecimalOrZero(amount)
	if required.Sign() <= 0 {
		return fmt.Errorf("missing declared funding amount")
	}
	got, err := fundingAssetAmount(output, assetName)
	if err != nil {
		return err
	}
	if got.Cmp(required) < 0 {
		return fmt.Errorf("invoke funding asset %s amount %s is less than declared amount %s", assetName, got.String(), required.String())
	}
	return nil
}

func fundingAssetAmount(output ContractOutput, assetName string) (*scommon.Decimal, error) {
	amount, err := output.AssetAmount(assetName)
	if err != nil {
		return nil, err
	}
	if amount == nil {
		return parseDecimalOrZero("0"), nil
	}
	return parseDecimalOrZero(amount.String()), nil
}

func (i *InvokeItem) applyAddLiquidityFundingValidation(param AddLiquidityInvokeParam) {
	if i.Reason != InvokeReasonNormal {
		return
	}
	if i.InAmt == nil || i.InAmt.Cmp(parseDecimalOrZero(param.Amt)) < 0 {
		i.Reason = InvokeReasonInvalid
		i.RemainingAmt = nil
		i.RemainingValue = 0
		return
	}
	if i.InValue < param.Value {
		i.Reason = InvokeReasonInvalid
		i.RemainingAmt = nil
		i.RemainingValue = 0
	}
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
		expected, overflow := contractframework.AddInt64(requiredValue, i.ServiceFee)
		if overflow {
			i.Reason = InvokeReasonInvalid
			i.RemainingValue = 0
			i.OutValue = 0
			return
		}
		if isAMM {
			if expected <= 0 || !valueWithinTolerance(i.InValue, expected, 5) {
				i.Reason = InvokeReasonInvalid
				i.RemainingValue = 0
			}
			return
		}
		if expected <= 0 || i.InValue < expected {
			i.Reason = InvokeReasonInvalid
			i.RemainingValue = 0
			i.OutValue = 0
			return
		}
		if i.InValue > expected {
			i.OutValue = i.InValue - expected
			i.RemainingValue = requiredValue
		}
	case OrderTypeSell:
		if i.InValue < i.ServiceFee {
			i.Reason = InvokeReasonInvalid
			i.RemainingAmt = nil
			return
		}
		if i.InValue != i.ServiceFee {
			i.Reason = InvokeReasonInvalid
			i.RemainingAmt = nil
			i.RemainingValue = 0
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
		state := TemplateRuntimeState{}
		running := state.AMMData()
		running.RequiredAssetA = parseDecimalOrZero(c.AssetAmt)
		running.RequiredAssetB = scommon.NewDefaultDecimal(c.SatValue)
		running.K = parseDecimalOrZero(c.K)
		return r.saveRuntimeState(state)
	case *AutopayContract:
		state := TemplateRuntimeState{}
		state.AutopayData().AutopayStatus = AutopayStatusFunding
		return r.saveRuntimeState(state)
	default:
		return nil
	}
}

func (r *ContractRuntime) ApplyFunding(output ContractOutput, gasAssetName string) error {
	state, err := r.loadRuntimeState()
	if err != nil {
		return err
	}
	if applier, ok := r.contract.(AddressFundingStateApplier); ok {
		handled, err := applier.ApplyFundingStateForAddress(&state, r.base.Deployer(), output, gasAssetName)
		if err != nil || handled {
			if err != nil {
				return err
			}
			return r.saveRuntimeState(state)
		}
	}
	if applier, ok := r.contract.(FundingStateApplier); ok {
		handled, err := applier.ApplyFundingState(&state, output, gasAssetName)
		if err != nil || handled {
			if err != nil {
				return err
			}
			return r.saveRuntimeState(state)
		}
	}
	assetName := contractAssetName(r.contract)
	if _, ok := r.contract.(*AMMContract); ok {
		running := state.AMMData()
		if running.AssetBInPool == nil {
			running.AssetBInPool = parseDecimalOrZero("0")
		}
		running.AssetBInPool = scommon.DecimalAdd(running.AssetBInPool, scommon.NewDefaultDecimal(output.PlainValue()))
		if assetName != "" {
			amt, err := output.AssetAmount(assetName)
			if err != nil {
				return err
			}
			amt = parseDecimalOrZero(amt.String())
			if running.AssetAInPool == nil {
				running.AssetAInPool = parseDecimalOrZero("0")
			}
			running.AssetAInPool = scommon.DecimalAdd(running.AssetAInPool, amt)
		}
	}
	if gasAssetName != "" {
		gas, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return err
		}
		addRunningGasBalance(r.contract, &state, gas)
	}
	if _, ok := r.contract.(*AMMContract); ok {
		if amm := state.AMMData(); !amm.TradingReady {
			amm.TradingReady = amm.ammTradingReady()
		}
		if err := initializeAMMInitialLP(&state, r.base.Deployer()); err != nil {
			return err
		}
	}
	return r.saveRuntimeState(state)
}

func initializeAMMInitialLP(state *TemplateRuntimeState, deployer string) error {
	if state == nil || deployer == "" {
		return nil
	}
	running := state.AMMData()
	if !running.TradingReady {
		return nil
	}
	if running.TotalLPTAmt != nil && running.TotalLPTAmt.Sign() > 0 {
		return nil
	}
	if len(running.LPBalances) != 0 {
		return nil
	}
	poolAsset := running.AssetAInPool
	poolGas := decimalInt64(running.AssetBInPool)
	if poolAsset == nil || poolAsset.Sign() <= 0 || poolGas <= 0 {
		return nil
	}
	initialLPT := scommon.DecimalMul(poolAsset, scommon.NewDefaultDecimal(poolGas)).Sqrt()
	if initialLPT.Sign() <= 0 {
		return nil
	}
	running.TotalLPTAmt = initialLPT
	running.LPBalances = map[string]*scommon.Decimal{deployer: initialLPT.Clone()}
	cost, err := ammLiquidityCost(poolAsset, poolGas)
	if err != nil {
		return err
	}
	running.LPCosts = map[string]int64{deployer: cost}
	return nil
}

func (r *ContractRuntime) ApplyGasFunding(output ContractOutput, gasAssetName string) error {
	if gasAssetName == "" {
		return nil
	}
	state, err := r.loadRuntimeState()
	if err != nil {
		return err
	}
	if applier, ok := r.contract.(GasFundingStateApplier); ok {
		handled, err := applier.ApplyGasFundingState(&state, output, gasAssetName)
		if err != nil || handled {
			if err != nil {
				return err
			}
			return r.saveRuntimeState(state)
		}
	}
	gas, err := output.AssetAmount(gasAssetName)
	if err != nil {
		return err
	}
	addRunningGasBalance(r.contract, &state, gas)
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
	state.applyFinishedItemStats(r.contract)
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
	case *AutopayContract:
		return c.FeeAssetName
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

func parseOptionalStateDecimal(field, value string) (*scommon.Decimal, error) {
	if value == "" {
		return nil, nil
	}
	return parseStateDecimal(field, value)
}

func parseGasStateDecimal(field, value string) (*scommon.Decimal, error) {
	d, err := scommon.NewDecimalFromString(value, contractcommon.GasFeePrecision)
	if err != nil {
		return nil, fmt.Errorf("invalid %s decimal %q: %w", field, value, err)
	}
	return d, nil
}

func parseOptionalGasStateDecimal(field, value string) (*scommon.Decimal, error) {
	if value == "" {
		return nil, nil
	}
	return parseGasStateDecimal(field, value)
}

func decimalStringMap(in map[string]*scommon.Decimal) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if str := decimalString(value); str != "" {
			out[key] = str
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseStateDecimalMap(field string, in map[string]string) (map[string]*scommon.Decimal, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]*scommon.Decimal, len(in))
	for key, value := range in {
		parsed, err := parseStateDecimal(field+"."+key, value)
		if err != nil {
			return nil, err
		}
		out[key] = parsed
	}
	return out, nil
}

func autopayDelegateJSONMap(in map[string]AutopayDelegate) map[string]autopayDelegateJSON {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]autopayDelegateJSON, len(in))
	for address, delegate := range in {
		out[address] = autopayDelegateJSON{
			AmountPerBlock: decimalString(delegate.AmountPerBlock),
			Balance:        decimalString(delegate.Balance),
			TotalPaid:      decimalString(delegate.TotalPaid),
			PaidBlockCount: delegate.PaidBlockCount,
			LastPayHeight:  delegate.LastPayHeight,
			Status:         delegate.Status,
		}
	}
	return out
}

func parseAutopayDelegateJSONMap(in map[string]autopayDelegateJSON) (map[string]AutopayDelegate, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]AutopayDelegate, len(in))
	for address, item := range in {
		amount, err := parseOptionalStateDecimal("autopayDelegates."+address+".amountPerBlock", item.AmountPerBlock)
		if err != nil {
			return nil, err
		}
		balance, err := parseOptionalStateDecimal("autopayDelegates."+address+".balance", item.Balance)
		if err != nil {
			return nil, err
		}
		totalPaid, err := parseOptionalStateDecimal("autopayDelegates."+address+".totalPaid", item.TotalPaid)
		if err != nil {
			return nil, err
		}
		out[address] = AutopayDelegate{
			AmountPerBlock: amount,
			Balance:        balance,
			TotalPaid:      totalPaid,
			PaidBlockCount: item.PaidBlockCount,
			LastPayHeight:  item.LastPayHeight,
			Status:         item.Status,
		}
	}
	return out, nil
}

func decimalInt64(value *scommon.Decimal) int64 {
	if value == nil {
		return 0
	}
	return value.Int64()
}

func (r AMMRunningData) ammTradingReady() bool {
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
