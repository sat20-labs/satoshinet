package framework

import (
	"fmt"
	"math/big"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
)

type SettlementPlan struct {
	Contract string `json:"contract"`
	Height   int64  `json:"height,omitempty"`

	AssetName  string `json:"assetName,omitempty"`
	ResultType string `json:"resultType,omitempty"`
	OutcomeID  string `json:"outcomeID,omitempty"`
	Refund     bool   `json:"refund,omitempty"`

	Deals     []SettlementDeal     `json:"deals,omitempty"`
	Transfers []SettlementTransfer `json:"transfers,omitempty"`
	ItemIDs   []int64              `json:"itemIDs,omitempty"`
	Inputs    []OutPoint           `json:"inputs,omitempty"`

	DeployerFeeBPS   int `json:"deployerFeeBPS,omitempty"`
	AgentFeeBPS      int `json:"agentFeeBPS,omitempty"`
	BootstrapFeeBPS  int `json:"bootstrapFeeBPS,omitempty"`
	WinnerPoolFeeBPS int `json:"winnerPoolFeeBPS,omitempty"`
}

type SettlementDeal struct {
	BuyItemID  int64  `json:"buyItemID,omitempty"`
	SellItemID int64  `json:"sellItemID,omitempty"`
	AssetAmt   string `json:"assetAmt,omitempty"`
	SatValue   int64  `json:"satValue,omitempty"`
	UnitPrice  string `json:"unitPrice,omitempty"`
}

type SettlementTransfer struct {
	ItemID    int64  `json:"itemID,omitempty"`
	To        string `json:"to,omitempty"`
	AssetName string `json:"assetName,omitempty"`
	AssetAmt  string `json:"assetAmt,omitempty"`
	SatValue  int64  `json:"satValue,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func SettlementPlanHasChanges(plan *SettlementPlan) bool {
	return plan != nil && (len(plan.Deals) != 0 || len(plan.Transfers) != 0 || len(plan.ItemIDs) != 0)
}

func CloneSettlementPlans(plans []*SettlementPlan) []*SettlementPlan {
	out := make([]*SettlementPlan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, CloneSettlementPlan(plan))
	}
	return out
}

func CloneSettlementPlan(plan *SettlementPlan) *SettlementPlan {
	if plan == nil {
		return nil
	}
	out := *plan
	out.Deals = append([]SettlementDeal(nil), plan.Deals...)
	out.Transfers = append([]SettlementTransfer(nil), plan.Transfers...)
	out.ItemIDs = append([]int64(nil), plan.ItemIDs...)
	out.Inputs = append([]OutPoint(nil), plan.Inputs...)
	return &out
}

type SettlementResultOptions struct {
	SatoshiAssetName      string
	MaxPrecision          int
	InvalidAsset          error
	RequireIntegerSatoshi bool
	IntegerSatoshiError   string
	MissingPlanError      string
	InputsByItem          map[int64][]OutPoint
	FeesByItem            map[int64]*scommon.Decimal
}

func BuildSettlementResultPlans(plans []*SettlementPlan, opts SettlementResultOptions) ([]ResultPlan, error) {
	out := make([]ResultPlan, 0, len(plans))
	for _, plan := range plans {
		if !SettlementPlanHasChanges(plan) {
			continue
		}
		result, err := BuildSettlementResultPlan(plan, opts)
		if err != nil {
			return nil, err
		}
		for _, itemID := range result.ItemIDs {
			result.GasFee = DecimalAddAllowNil(result.GasFee, opts.FeesByItem[itemID])
		}
		out = append(out, result)
	}
	return out, nil
}

func BuildSettlementResultPlan(plan *SettlementPlan, opts SettlementResultOptions) (ResultPlan, error) {
	if plan == nil {
		if opts.MissingPlanError != "" {
			return ResultPlan{}, fmt.Errorf("%s", opts.MissingPlanError)
		}
		return ResultPlan{}, fmt.Errorf("missing settlement plan")
	}
	out := ResultPlan{
		Contract: plan.Contract,
		Height:   plan.Height,
		ItemIDs:  append([]int64(nil), plan.ItemIDs...),
		Inputs:   append([]OutPoint(nil), plan.Inputs...),
	}
	for _, itemID := range plan.ItemIDs {
		out.Inputs = append(out.Inputs, opts.InputsByItem[itemID]...)
	}
	out.Inputs = UniqueOutPoints(out.Inputs)
	for _, transfer := range plan.Transfers {
		output, err := ResultOutputFromSettlementTransfer(transfer, opts)
		if err != nil {
			return ResultPlan{}, err
		}
		if !ResultOutputIsZero(output) {
			out.Outputs = append(out.Outputs, output)
		}
	}
	return out, nil
}

func ResultOutputFromSettlementTransfer(transfer SettlementTransfer, opts SettlementResultOptions) (ResultOutput, error) {
	return ResultOutputFromTransferFields(TransferOutputRequest{
		To:                    transfer.To,
		AssetName:             transfer.AssetName,
		AssetAmt:              transfer.AssetAmt,
		SatValue:              transfer.SatValue,
		SatoshiAssetName:      opts.SatoshiAssetName,
		Reason:                transfer.Reason,
		MaxPrecision:          opts.MaxPrecision,
		InvalidAsset:          opts.InvalidAsset,
		RequireIntegerSatoshi: opts.RequireIntegerSatoshi,
		IntegerSatoshiError:   opts.IntegerSatoshiError,
	})
}

func BuildSettlementAssetIntents(plan *SettlementPlan, opts SettlementResultOptions) ([]AssetIntent, error) {
	if plan == nil {
		return nil, nil
	}
	contractAddr, err := contract.DecodeContractAddress(plan.Contract)
	if err != nil {
		return nil, err
	}
	out := make([]AssetIntent, 0, len(plan.Transfers))
	for i, transfer := range plan.Transfers {
		intents, err := assetIntentsFromSettlementTransfer(contractAddr, transfer, uint32(i), opts)
		if err != nil {
			return nil, err
		}
		out = append(out, intents...)
	}
	return out, nil
}

func BuildSettlementAssetIntentsByItem(plan *SettlementPlan,
	opts SettlementResultOptions) (map[int64][]AssetIntent, error) {

	if plan == nil {
		return nil, nil
	}
	contractAddr, err := contract.DecodeContractAddress(plan.Contract)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]AssetIntent)
	for i, transfer := range plan.Transfers {
		intents, err := assetIntentsFromSettlementTransfer(contractAddr, transfer, uint32(i), opts)
		if err != nil {
			return nil, err
		}
		if len(intents) != 0 {
			out[transfer.ItemID] = append(out[transfer.ItemID], intents...)
		}
	}
	return out, nil
}

func AddGasFeesToResultPlans(plans []ResultPlan, records []ExecutionRecord) []ResultPlan {
	out := CloneResultPlans(plans)
	index := make(map[string]int)
	for i, plan := range out {
		index[plan.Contract] = i
	}
	for _, record := range records {
		if !record.RequiresResult || record.GasFee == nil || record.GasFee.Sign() == 0 {
			continue
		}
		contract := record.Contract.MustEncode()
		i, ok := index[contract]
		if !ok {
			i = len(out)
			index[contract] = i
			out = append(out, ResultPlan{Contract: contract})
		}
		out[i].GasFee = DecimalAddAllowNil(out[i].GasFee, record.GasFee)
		out[i].Inputs = append(out[i].Inputs, record.FundingInputs...)
		out[i].Inputs = UniqueOutPoints(out[i].Inputs)
	}
	return out
}

func ResultPlanAssetName(plan ResultPlan) string {
	for _, output := range plan.Outputs {
		if output.AssetName != "" {
			return output.AssetName
		}
	}
	return ""
}

func ScaleResultOutputsToPool(outputs []ResultOutput, assetName string, pool *scommon.Decimal,
	opts SettlementResultOptions) ([]ResultOutput, error) {

	out := CloneResultOutputs(outputs)
	total := ZeroDecimal()
	for _, output := range out {
		if output.AssetName != assetName {
			continue
		}
		amount, err := parseTransferAmount(output.AssetAmt, opts.MaxPrecision,
			opts.RequireIntegerSatoshi && output.AssetName == opts.SatoshiAssetName,
			opts.IntegerSatoshiError)
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(amount)
	}
	if pool == nil || pool.Cmp(total) == 0 {
		return out, nil
	}
	if total.Sign() == 0 {
		if pool.Sign() == 0 {
			return out, nil
		}
		return nil, fmt.Errorf("cannot distribute result pool without result outputs")
	}
	if pool.Cmp(total) < 0 {
		return nil, fmt.Errorf("result outputs spend %s but only %s is available", total.String(), pool.String())
	}

	extra := scommon.DecimalSub(pool, total)
	precision := pool.Precision
	if total.Precision > precision {
		precision = total.Precision
	}
	amounts := make([]*scommon.Decimal, len(out))
	for i, output := range out {
		if output.AssetName != assetName {
			continue
		}
		amount, err := parseTransferAmount(output.AssetAmt, opts.MaxPrecision,
			opts.RequireIntegerSatoshi && output.AssetName == opts.SatoshiAssetName,
			opts.IntegerSatoshiError)
		if err != nil {
			return nil, err
		}
		amounts[i] = amount
		if amount.Precision > precision {
			precision = amount.Precision
		}
	}

	extraValue := extra.NewPrecision(precision).Value
	totalValue := total.NewPrecision(precision).Value
	if extraValue.Sign() == 0 {
		return out, nil
	}
	shares := make([]*big.Int, len(out))
	sum := big.NewInt(0)
	for i, amount := range amounts {
		if amount == nil {
			continue
		}
		amount = amount.NewPrecision(precision)
		share := new(big.Int).Mul(extraValue, amount.Value)
		share.Div(share, totalValue)
		shares[i] = share
		sum.Add(sum, share)
	}
	remainder := new(big.Int).Sub(extraValue, sum)
	for i := range out {
		if remainder.Sign() == 0 {
			break
		}
		if out[i].AssetName != assetName {
			continue
		}
		if shares[i] == nil {
			shares[i] = big.NewInt(0)
		}
		shares[i].Add(shares[i], big.NewInt(1))
		remainder.Sub(remainder, big.NewInt(1))
	}

	for i := range out {
		if out[i].AssetName != assetName || shares[i] == nil || shares[i].Sign() == 0 {
			continue
		}
		amount := amounts[i].NewPrecision(precision)
		amount.Value.Add(amount.Value, shares[i])
		out[i].AssetAmt = amount.String()
		if err := RebuildResultOutputAsset(&out[i], opts); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func RebuildResultOutputAsset(output *ResultOutput, opts SettlementResultOptions) error {
	if output == nil || output.AssetName == "" {
		return nil
	}
	if output.AssetName == opts.SatoshiAssetName {
		amt, err := parseTransferAmount(output.AssetAmt, opts.MaxPrecision,
			opts.RequireIntegerSatoshi, opts.IntegerSatoshiError)
		if err != nil {
			return err
		}
		output.AssetAmt = amt.String()
		output.Value = amt.Int64()
		output.Assets = nil
		return nil
	}
	assets, err := NewAssetSetWithPrecision(output.AssetName, output.AssetAmt,
		opts.MaxPrecision, opts.InvalidAsset)
	if err != nil {
		return err
	}
	output.Value = 0
	output.Assets = assets
	return nil
}

func assetIntentsFromSettlementTransfer(contractAddr contract.ContractAddress,
	transfer SettlementTransfer, index uint32, opts SettlementResultOptions) ([]AssetIntent, error) {

	return AssetIntentsFromTransferFields(TransferIntentRequest{
		From:                  contractAddr,
		To:                    transfer.To,
		AssetName:             transfer.AssetName,
		AssetAmt:              transfer.AssetAmt,
		SatValue:              transfer.SatValue,
		SatoshiAssetName:      opts.SatoshiAssetName,
		MaxPrecision:          opts.MaxPrecision,
		IntentIndex:           index,
		RequireIntegerSatoshi: opts.RequireIntegerSatoshi,
		IntegerSatoshiError:   opts.IntegerSatoshiError,
	})
}
