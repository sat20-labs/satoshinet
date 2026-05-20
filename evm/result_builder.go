package evm

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/evm/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultRecipientScriptResolver func(output ResultOutput) ([]byte, error)

type ResultTxBuildRequest struct {
	Status        ResultStatus
	ResultCount   uint16
	Plans         []ResultPlan
	ResolveScript ResultRecipientScriptResolver
}

type CanonicalResultTxRequest struct {
	Status        ResultStatus
	Records       []ExecutionRecord
	GasConfig     GasConfig
	UTXOs         ContractUTXOProvider
	ResolveScript ResultRecipientScriptResolver
}

func BuildResultTx(req ResultTxBuildRequest) (*wire.MsgTx, error) {
	if req.ResultCount == 0 {
		return nil, fmt.Errorf("result count is zero")
	}
	tx := wire.NewMsgTx(2)
	for _, plan := range req.Plans {
		for _, input := range plan.Inputs {
			outpoint, err := resultBuilderWireOutPoint(input.OutPoint)
			if err != nil {
				return nil, err
			}
			tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
		}
		for _, output := range plan.Outputs {
			if req.ResolveScript == nil {
				return nil, fmt.Errorf("missing result output script resolver")
			}
			txOut, err := resultBuilderTxOut(output, req.ResolveScript)
			if err != nil {
				return nil, err
			}
			tx.AddTxOut(txOut)
		}
	}
	script, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      req.Status,
		ResultCount: req.ResultCount,
	})
	if err != nil {
		return nil, err
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx, nil
}

func BuildCanonicalResultTx(req CanonicalResultTxRequest) (*wire.MsgTx, error) {
	if len(req.Records) == 0 {
		return nil, fmt.Errorf("missing execution records")
	}
	if bits.Len(uint(len(req.Records))) > 16 {
		return nil, fmt.Errorf("too many execution records: %d", len(req.Records))
	}
	verifier := CanonicalResultVerifier{
		GasConfig: req.GasConfig,
		UTXOs:     req.UTXOs,
	}
	plans, err := verifier.BuildPlans(req.Records)
	if err != nil {
		return nil, err
	}
	return BuildResultTx(ResultTxBuildRequest{
		Status:        req.Status,
		ResultCount:   uint16(len(req.Records)),
		Plans:         plans,
		ResolveScript: req.ResolveScript,
	})
}

func resultBuilderWireOutPoint(outpoint OutPoint) (*wire.OutPoint, error) {
	hash, err := chainhash.NewHashFromStr(outpoint.TxID)
	if err != nil {
		return nil, err
	}
	return wire.NewOutPoint(hash, outpoint.Vout), nil
}

func resultBuilderTxOut(output ResultOutput, resolve ResultRecipientScriptResolver) (*wire.TxOut, error) {
	if len(output.ExtraData) != 0 {
		return nil, fmt.Errorf("result output extra data is not supported by tx builder")
	}
	pkScript, err := resolve(output)
	if err != nil {
		return nil, err
	}
	if output.Value > uint64(math.MaxInt64) {
		return nil, fmt.Errorf("satoshi output amount overflows int64")
	}
	assets := output.Assets.Clone()
	for _, asset := range assets {
		if err := validateAssetDecimal(asset.Amount); err != nil {
			return nil, fmt.Errorf("asset %s amount: %w", asset.Name.String(), err)
		}
	}
	return wire.NewTxOut(int64(output.Value), assets, pkScript), nil
}
