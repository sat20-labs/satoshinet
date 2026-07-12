package framework

import (
	"fmt"
	"math"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

const MaxContractResultOutputs = 1000

type ResultTxBuildOptions struct {
	UseInputUTXOs bool
	PlanCount     func(ResultPlan) int
}

type CanonicalResultTxRequest struct {
	Status        contract.ResultStatus
	Records       []ExecutionRecord
	GasConfig     GasConfig
	UTXOs         ContractUTXOProvider
	Precision     AssetPrecisionPolicy
	ResolveScript ResultRecipientScriptResolver
}

func BuildCanonicalResultTx(req CanonicalResultTxRequest) (*wire.MsgTx, error) {
	if len(req.Records) == 0 {
		return nil, fmt.Errorf("missing execution records")
	}
	if len(req.Records) > math.MaxUint16 {
		return nil, fmt.Errorf("too many execution records: %d", len(req.Records))
	}
	plans, err := (CanonicalResultPlanner{
		GasConfig: req.GasConfig,
		UTXOs:     req.UTXOs,
		Precision: req.Precision,
	}).BuildPlans(req.Records)
	if err != nil {
		return nil, err
	}
	return BuildResultTx(ResultTxBuildRequest{
		Status:        req.Status,
		ResultCount:   uint16(len(req.Records)),
		Plans:         plans,
		ResolveScript: req.ResolveScript,
	}, ResultTxBuildOptions{
		UseInputUTXOs: true,
	})
}

func BuildResultTx(req ResultTxBuildRequest, opts ResultTxBuildOptions) (*wire.MsgTx, error) {
	if len(req.Plans) == 0 && req.ResultCount == 0 {
		return nil, fmt.Errorf("missing result plans")
	}
	resultCount := 0
	if req.ResultCount == 0 {
		for _, plan := range req.Plans {
			resultCount += resultPlanCount(plan, opts.PlanCount)
		}
	}
	req.Plans = MergeResultPlansByContract(req.Plans)
	tx := wire.NewMsgTx(2)
	outputCount := 0
	for _, plan := range req.Plans {
		if opts.UseInputUTXOs {
			for _, input := range plan.InputUTXOs {
				outpoint, err := ResultWireOutPoint(input.OutPoint)
				if err != nil {
					return nil, err
				}
				tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
			}
		} else {
			for _, input := range plan.Inputs {
				outpoint, err := ResultWireOutPoint(input)
				if err != nil {
					return nil, err
				}
				tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
			}
		}
		for _, output := range plan.Outputs {
			if outputCount >= MaxContractResultOutputs {
				return nil, fmt.Errorf("too many contract result outputs: %d", outputCount+1)
			}
			txOut, err := ResultTxOut(output, req.ResolveScript)
			if err != nil {
				return nil, err
			}
			tx.AddTxOut(txOut)
			outputCount++
		}
	}
	if req.ResultCount != 0 {
		resultCount = int(req.ResultCount)
	}
	if resultCount == 0 || resultCount > math.MaxUint16 {
		return nil, fmt.Errorf("invalid result count %d", resultCount)
	}
	script, err := contract.ResultNullDataScript(contract.ResultPayload{
		Status:      req.Status,
		ResultCount: uint16(resultCount),
	})
	if err != nil {
		return nil, err
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	if tx.SerializeSize() > wire.MaxBlockPayload {
		return nil, fmt.Errorf("contract result transaction is too large: %d", tx.SerializeSize())
	}
	return tx, nil
}

func resultPlanCount(plan ResultPlan, count func(ResultPlan) int) int {
	if count != nil {
		return count(plan)
	}
	if plan.ResultCount > 0 {
		return plan.ResultCount
	}
	return 1
}

func ResultWireOutPoint(outpoint OutPoint) (*wire.OutPoint, error) {
	hash, err := chainhash.NewHashFromStr(outpoint.TxID)
	if err != nil {
		return nil, err
	}
	return wire.NewOutPoint(hash, outpoint.Vout), nil
}

func ResultTxOut(output ResultOutput, resolve ResultRecipientScriptResolver) (*wire.TxOut, error) {
	if len(output.ExtraData) != 0 {
		return nil, fmt.Errorf("result output extra data is not supported by tx builder")
	}
	if resolve == nil {
		return nil, fmt.Errorf("missing result output script resolver")
	}
	pkScript, err := resolve(output)
	if err != nil {
		return nil, err
	}
	if output.Value < 0 {
		return nil, fmt.Errorf("satoshi output amount is negative")
	}
	assets := output.Assets.Clone()
	for _, asset := range assets {
		if err := ValidateAssetDecimal(asset.Amount); err != nil {
			return nil, fmt.Errorf("asset %s amount: %w", asset.Name.String(), err)
		}
	}
	return wire.NewTxOut(output.Value, assets, pkScript), nil
}
