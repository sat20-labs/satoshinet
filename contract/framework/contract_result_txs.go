package framework

import (
	"fmt"
	"math"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

// Contract Results preserve execution's first-seen contract and output order.
// Each contract spends only its own inputs. A fragment selects the original
// UTXOs needed for its outputs and carries only their valid change. Unspent
// originals remain available to later fragments; the final fragment pays the
// logical settlement fee once. Business execution is never replayed.
type contractResultPart struct {
	tx    *wire.MsgTx
	plan  ResultPlan
	count int
}

func buildContractResultParts(req ResultTxBuildRequest, opts ResultTxBuildOptions) ([]contractResultPart, error) {
	plans := CloneResultPlans(req.Plans)
	// Capture counts before merging, including engine-specific ItemID counts.
	for n := range plans {
		plans[n].ResultCount = resultPlanCount(plans[n], opts.PlanCount)
	}
	plans = MergeResultPlansByContract(plans)
	var parts []contractResultPart
	seen := make(map[OutPoint]bool)
	for _, plan := range plans {
		addr, err := contract.DecodeContractAddress(plan.Contract)
		if err != nil {
			return nil, fmt.Errorf("invalid Result contract: %w", err)
		}
		count := plan.ResultCount
		if count <= 0 || count > math.MaxUint16 {
			return nil, fmt.Errorf("invalid result count %d", count)
		}
		inputs := plan.Inputs
		if opts.UseInputUTXOs {
			inputs = nil
			for _, input := range plan.InputUTXOs {
				inputs = append(inputs, input.OutPoint)
			}
		}
		if len(inputs) == 0 {
			return nil, fmt.Errorf("contract Result spends no input")
		}
		for _, input := range inputs {
			if seen[input] {
				return nil, fmt.Errorf("duplicate contract Result input %s", input)
			}
			seen[input] = true
		}
		for _, input := range plan.InputUTXOs {
			if !input.Contract.Equal(addr) {
				return nil, fmt.Errorf("mixed contract Result funding")
			}
		}
		remaining := plan.Outputs
		available := append([]UTXO(nil), plan.InputUTXOs...)
		if !opts.UseInputUTXOs {
			available = resultPlanInputUTXOs(plan)
		}
		split := false
		for {
			part := CloneResultPlan(plan)
			part.InputUTXOs = available
			// Reserve metadata first. A carry takes a slot only when the
			// selected inputs actually leave change.
			nonFinal := len(remaining) > MaxContractResultOutputs-1
			consumed := len(remaining)
			var carry *ResultOutput
			if nonFinal {
				if len(available) != len(inputs) && !split {
					return nil, fmt.Errorf("fragmented Result requires all physical input UTXOs")
				}
				view := ResultPlanUTXOView{Contract: addr, UTXOs: available}
				var selected ResultPlanUTXOView
				var fundingErr error
				// Prefer the largest legal prefix. Reducing it can avoid a
				// partial-binding boundary in change, or make room for carry.
				for consumed = MaxContractResultOutputs - 1; consumed > 0; consumed-- {
					outputs := remaining[:consumed]
					selected, fundingErr = selectResultFunding(view, outputs, "", nil, 0, nil)
					if fundingErr != nil {
						continue
					}
					if len(selected.UTXOs) == 0 {
						if len(available) == 0 {
							return nil, fmt.Errorf("fragmented Result has no available input")
						}
						selected, fundingErr = selectResultFunding(view, outputs, "", nil, 0, []OutPoint{available[0].OutPoint})
						if fundingErr != nil {
							continue
						}
					}
					value, assets, err := physicalRemainderAfterOutputs(selected, outputs, "", nil)
					if err != nil {
						return nil, err
					}
					change := ResultOutput{To: plan.Contract, Value: value, Assets: assets}
					if !ResultOutputIsZero(change) {
						if consumed+2 > MaxContractResultOutputs {
							continue
						}
						carry = &change
					}
					break
				}
				if consumed == 0 {
					return nil, fmt.Errorf("cannot fund contract Result fragment: %w", fundingErr)
				}
				part.Outputs = CloneResultOutputs(remaining[:consumed])
				if carry != nil {
					part.Outputs = append(part.Outputs, *carry)
				}
				part.Inputs, part.InputUTXOs = selected.Inputs, selected.UTXOs
				// Keep the logical fee for the final fragment; charging it
				// early can force unrelated refund inputs into this carry.
				part.GasFee, part.SatoshiFee, part.FeeOutputs = nil, 0, nil
				spent := make(map[OutPoint]bool, len(selected.Inputs))
				for _, input := range selected.Inputs {
					spent[input] = true
				}
				unspent := make([]UTXO, 0, len(available))
				for _, input := range available {
					if !spent[input.OutPoint] {
						unspent = append(unspent, input)
					}
				}
				available = unspent
			} else {
				part.Outputs = CloneResultOutputs(remaining)
				if split {
					SortUTXOsForCanonicalSelection(available)
					part.InputUTXOs = available
					part.Inputs = nil
					for _, input := range available {
						part.Inputs = append(part.Inputs, input.OutPoint)
					}
				}
			}
			tx, err := BuildResultTx(ResultTxBuildRequest{Status: req.Status, ResultCount: uint16(count),
				Plans: []ResultPlan{part}, ResolveScript: req.ResolveScript}, opts)
			if err != nil {
				return nil, err
			}
			if len(tx.TxIn) == 0 {
				return nil, fmt.Errorf("contract Result fragment spends no input")
			}
			if len(part.InputUTXOs) != 0 {
				if part.SatoshiFee < 0 {
					return nil, fmt.Errorf("negative Result satoshi fee")
				}
				if err := VerifyResultInputCoverage(part.InputUTXOs, part.Outputs,
					scommon.NewDefaultDecimal(part.SatoshiFee), contract.SatoshiAssetName); err != nil {
					return nil, err
				}
			}
			parts = append(parts, contractResultPart{tx: tx, plan: part, count: count})
			if !nonFinal {
				break
			}
			if carry != nil {
				outpoint := OutPoint{TxID: tx.TxHash().String(), Vout: uint32(len(part.Outputs) - 1)}
				available = append(available, UTXOFromTxOutput(outpoint, addr, plan.Height, tx.TxOut[outpoint.Vout]))
			}
			remaining = remaining[consumed:]
			split = true
		}
	}
	return parts, nil
}

func BuildContractResultTxs(req ResultTxBuildRequest, opts ResultTxBuildOptions) ([]*wire.MsgTx, error) {
	parts, err := buildContractResultParts(req, opts)
	if err != nil {
		return nil, err
	}
	txs := make([]*wire.MsgTx, len(parts))
	for n := range parts {
		txs[n] = parts[n].tx
	}
	return txs, nil
}

type ContractResultVerifyRequest struct {
	CanonicalResultVerifyRequest
	ResultTxs []*wire.MsgTx
	// Optional trusted embedding hook; production uses canonical verification.
	Verify func(*wire.MsgTx, []ResultPlan) error
}

func VerifyContractResultTxs(req ContractResultVerifyRequest) error {
	if len(req.Plans) == 0 {
		if len(req.ResultTxs) != 0 {
			return fmt.Errorf("unexpected %s RESULT transactions", req.Label)
		}
		return nil
	}
	if req.ResolveScript == nil {
		return fmt.Errorf("missing %s result output script resolver", req.Label)
	}
	parts, err := buildContractResultParts(ResultTxBuildRequest{Status: req.Status,
		Plans: req.Plans, ResolveScript: req.ResolveScript}, ResultTxBuildOptions{UseInputUTXOs: req.UseInputUTXO, PlanCount: req.PlanCount})
	if err != nil {
		return err
	}
	if len(req.ResultTxs) != len(parts) {
		return fmt.Errorf("%s result transaction count mismatch: got %d want %d", req.Label, len(req.ResultTxs), len(parts))
	}
	for n, part := range parts {
		check := req.CanonicalResultVerifyRequest
		check.ResultTx = req.ResultTxs[n]
		check.Plans = []ResultPlan{part.plan}
		// All fragments describe the same already-executed contract settlement;
		// ResultCount is not an instruction to replay it for each fragment.
		check.ResultCount = part.count
		if err := VerifyCanonicalResultTx(check); err != nil {
			return err
		}
		if req.Verify != nil {
			if err := req.Verify(req.ResultTxs[n], check.Plans); err != nil {
				return err
			}
		}
	}
	return nil
}
