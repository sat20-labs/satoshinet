package agent

import (
	"fmt"
	"math/big"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

type PredictionSettlementPlan = contractframework.SettlementPlan
type PredictionSettlementOutput = contractframework.SettlementTransfer

type predictionWinner struct {
	Address string
	Amount  *scommon.Decimal
}

func (r *Runtime) buildSettlementPlan(confirm PredictionConfirmParam) (*PredictionSettlementPlan, error) {
	plan := &PredictionSettlementPlan{
		Contract:   r.address.EncodeAddress(),
		AssetName:  r.contract.BetAsset,
		ResultType: confirm.ResultType,
		OutcomeID:  confirm.OutcomeID,
	}
	if confirm.ResultType != ResultTypeOutcome {
		plan.Refund = true
		r.addRefundTransfers(plan)
		return plan, nil
	}

	winners := r.winners(confirm.OutcomeID)
	if len(winners) == 0 {
		plan.Refund = true
		r.addRefundTransfers(plan)
		return plan, nil
	}
	if r.deployer == "" || r.config.AgentAddress == "" || r.config.BootstrapAddress == "" {
		return nil, fmt.Errorf("missing prediction fee recipient")
	}

	precision := r.betAssetPrecision()
	total := r.totalBetAmount().NewPrecision(precision)
	deployerFee := decimalMulBPS(total, PredictionDeployerFeeBPS)
	agentFee := decimalMulBPS(total, PredictionAgentFeeBPS)
	bootstrapFee := decimalMulBPS(total, PredictionBootstrapBPS)
	winnerPool := scommon.DecimalSub(total, deployerFee)
	winnerPool = scommon.DecimalSub(winnerPool, agentFee)
	winnerPool = scommon.DecimalSub(winnerPool, bootstrapFee)

	plan.DeployerFeeBPS = PredictionDeployerFeeBPS
	plan.AgentFeeBPS = PredictionAgentFeeBPS
	plan.BootstrapFeeBPS = PredictionBootstrapBPS
	plan.WinnerPoolFeeBPS = PredictionWinnerPoolBPS
	plan.Transfers = append(plan.Transfers,
		settlementOutput(r.deployer, r.contract.BetAsset, deployerFee, "deployer_fee"),
		settlementOutput(r.config.AgentAddress, r.contract.BetAsset, agentFee, "agent_fee"),
		settlementOutput(r.config.BootstrapAddress, r.contract.BetAsset, bootstrapFee, "bootstrap_fee"),
	)
	plan.Transfers = append(plan.Transfers, distributeWinnerPool(r.contract.BetAsset, winnerPool, winners)...)
	return plan, nil
}

func (r *Runtime) addRefundTransfers(plan *PredictionSettlementPlan) {
	for _, bet := range sortedBetRecords(r.state.Prediction.Bets) {
		plan.Transfers = append(plan.Transfers, PredictionSettlementOutput{
			To:        bet.Address,
			AssetName: r.contract.BetAsset,
			AssetAmt:  bet.Amount,
			Reason:    "refund",
		})
	}
}

func (r *Runtime) winners(outcomeID string) []predictionWinner {
	winners := make([]predictionWinner, 0)
	for _, bet := range sortedBetRecords(r.state.Prediction.Bets) {
		if bet.OutcomeID != outcomeID {
			continue
		}
		winners = append(winners, predictionWinner{
			Address: bet.Address,
			Amount:  parseDecimalOrZero(bet.Amount),
		})
	}
	sort.SliceStable(winners, func(i, j int) bool {
		cmp := winners[i].Amount.Cmp(winners[j].Amount)
		if cmp != 0 {
			return cmp > 0
		}
		return winners[i].Address < winners[j].Address
	})
	return winners
}

func (r *Runtime) totalBetAmount() *scommon.Decimal {
	total := scommon.NewDefaultDecimal(0)
	for _, bet := range r.state.Prediction.Bets {
		total = scommon.DecimalAdd(total, parseDecimalOrZero(bet.Amount))
	}
	return total
}

func (r *Runtime) betAssetPrecision() int {
	if r == nil || r.config.AssetPrecision == nil {
		return MaxPredictionDecimalPrecision
	}
	precision, ok := r.config.AssetPrecision(r.contract.BetAsset)
	if !ok || precision < 0 {
		return MaxPredictionDecimalPrecision
	}
	return precision
}

func distributeWinnerPool(assetName string, winnerPool *scommon.Decimal, winners []predictionWinner) []PredictionSettlementOutput {
	if len(winners) == 0 || winnerPool == nil || winnerPool.Sign() <= 0 {
		return nil
	}
	precision := winnerPool.Precision
	totalWinnerAmount := scommon.NewDefaultDecimal(0)
	for _, winner := range winners {
		if winner.Amount.Precision > precision {
			precision = winner.Amount.Precision
		}
		totalWinnerAmount = scommon.DecimalAdd(totalWinnerAmount, winner.Amount)
	}
	totalWinnerAmount = totalWinnerAmount.NewPrecision(precision)
	pool := winnerPool.NewPrecision(precision)

	rawShares := make([]*big.Int, len(winners))
	sum := big.NewInt(0)
	for i, winner := range winners {
		amount := winner.Amount.NewPrecision(precision)
		raw := new(big.Int).Mul(pool.Value, amount.Value)
		raw.Div(raw, totalWinnerAmount.Value)
		rawShares[i] = raw
		sum.Add(sum, raw)
	}
	remainder := new(big.Int).Sub(pool.Value, sum)
	for i := 0; remainder.Sign() > 0 && i < len(rawShares); i++ {
		rawShares[i].Add(rawShares[i], big.NewInt(1))
		remainder.Sub(remainder, big.NewInt(1))
	}

	outputs := make([]PredictionSettlementOutput, 0, len(winners))
	for i, winner := range winners {
		outputs = append(outputs, PredictionSettlementOutput{
			To:        winner.Address,
			AssetName: assetName,
			AssetAmt:  (&scommon.Decimal{Precision: precision, Value: rawShares[i]}).String(),
			Reason:    "winner_payout",
		})
	}
	return outputs
}

func sortedBetRecords(bets map[string]PredictionBetRecord) []PredictionBetRecord {
	out := make([]PredictionBetRecord, 0, len(bets))
	for _, bet := range bets {
		out = append(out, bet)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Address != out[j].Address {
			return out[i].Address < out[j].Address
		}
		return out[i].OutcomeID < out[j].OutcomeID
	})
	return out
}

func settlementOutput(to, assetName string, amount *scommon.Decimal, reason string) PredictionSettlementOutput {
	return PredictionSettlementOutput{
		To:        to,
		AssetName: assetName,
		AssetAmt:  amount.String(),
		Reason:    reason,
	}
}

func decimalMulBPS(amount *scommon.Decimal, bps int) *scommon.Decimal {
	if amount == nil {
		return scommon.NewDefaultDecimal(0)
	}
	n := amount.MulBigInt(big.NewInt(int64(bps)))
	return n.DivBigInt(big.NewInt(PredictionTotalBPS))
}

func decimalStringAdd(a, b string) string {
	da := parseDecimalOrZero(a)
	db := parseDecimalOrZero(b)
	return scommon.DecimalAdd(da, db).String()
}

func parseDecimalOrZero(value string) *scommon.Decimal {
	if value == "" {
		return scommon.NewDefaultDecimal(0)
	}
	d, err := scommon.NewDecimalFromString(value, MaxPredictionDecimalPrecision)
	if err != nil {
		return scommon.NewDefaultDecimal(0)
	}
	return d
}

func zeroDecimal() *scommon.Decimal {
	return scommon.NewDefaultDecimal(0)
}

func decimalAdd(a, b *scommon.Decimal) *scommon.Decimal {
	return scommon.DecimalAdd(a, b)
}
