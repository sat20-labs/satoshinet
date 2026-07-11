package evm

import (
	"fmt"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultMinimalCommitment struct {
	Status       ResultStatus
	ResultCount  uint16
	ErrorDigest  [32]byte
	HasErrorInfo bool
}

type ResultBinding struct {
	InvokeCalls  []InvokeCallBinding
	DeployCount  int
	TriggerCount int
}

type InvokeCallBinding struct {
	CallID       string
	InvokeTxID   string
	FundingInput OutPoint
	Contract     ContractAddress
}

type ResultVerifyRequest struct {
	ResultTxs      []*wire.MsgTx
	Execution      BlockExecutionResult
	ContractPrefix string
	VerifyResult   ResultVerifier
}

type CanonicalResultVerifier struct {
	GasConfig     GasConfig
	UTXOs         ContractUTXOProvider
	Precision     contractframework.AssetPrecisionPolicy
	ResolveOutput ResultOutputResolver
	ResolveScript ResultRecipientScriptResolver
}

func VerifyResultTxs(req ResultVerifyRequest) error {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	pending := contractframework.CloneExecutionRecords(req.Execution.PendingRecords)
	if len(pending) == 0 {
		if len(req.ResultTxs) != 0 {
			return fmt.Errorf("unexpected EVM RESULT transactions")
		}
		return nil
	}
	if len(req.ResultTxs) != 1 {
		return fmt.Errorf("EVM result transaction count mismatch: got %d want 1", len(req.ResultTxs))
	}
	resultTx := req.ResultTxs[0]
	parsed, err := ParseTx(resultTx, StandardContractScriptResolver(prefix))
	if err != nil {
		return err
	}
	pending, err = verifyResultAgainstPending(resultTx, parsed, pending, req.VerifyResult)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("%d EVM executions remain unsettled", len(pending))
	}
	return nil
}

func (v CanonicalResultVerifier) Verify(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
	plans, err := v.BuildPlans(settled)
	if err != nil {
		return err
	}
	return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label:         "EVM",
		ResultTx:      resultTx,
		Status:        contractframework.AggregateResultStatus(settled),
		Plans:         plans,
		GasAssetName:  v.GasConfig.Normalize().GasAssetName,
		Resolve:       v.ResolveOutput,
		ResolveScript: v.ResolveScript,
		ResultCount:   len(settled),
		UseInputUTXO:  true,
		CheckPayload:  v.ResolveScript != nil,
	})
}

func (v CanonicalResultVerifier) BuildPlans(settled []ExecutionRecord) ([]ResultPlan, error) {
	return (contractframework.CanonicalResultPlanner{
		GasConfig: v.GasConfig,
		UTXOs:     v.UTXOs,
		Precision: v.Precision,
	}).BuildPlans(settled)
}

func DeriveInvokeCallID(invokeTxID string, vout uint32, contract ContractAddress) string {
	return contractframework.DeriveInvokeCallID("invoke", invokeTxID, vout, contract)
}

func DeriveDeployCallID(deployTxID string, contract ContractAddress) string {
	return contractframework.DeriveDeployCallID("deploy", deployTxID, contract)
}

func DeriveTriggerCallID(contract ContractAddress, triggerID string, height int64) string {
	return contractframework.DeriveTriggerCallID("trigger", contract, triggerID, height)
}

func BindResultInvokes(resultInputs []OutPoint, fundingByOutPoint map[OutPoint]InvokeCallBinding) []InvokeCallBinding {
	bindings := make([]InvokeCallBinding, 0)
	for _, in := range resultInputs {
		if binding, ok := fundingByOutPoint[in]; ok {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

func BindResultTxInvokes(resultTx *wire.MsgTx, fundingByOutPoint map[OutPoint]InvokeCallBinding) ([]InvokeCallBinding, error) {
	if resultTx == nil {
		return nil, fmt.Errorf("missing result transaction")
	}
	inputs := make([]OutPoint, 0, len(resultTx.TxIn))
	for i, txIn := range resultTx.TxIn {
		if txIn == nil {
			return nil, fmt.Errorf("nil result input %d", i)
		}
		inputs = append(inputs, WireOutPointToEVM(txIn.PreviousOutPoint))
	}
	return BindResultInvokes(inputs, fundingByOutPoint), nil
}

func RequireResultTxInvokeBindings(resultTx *wire.MsgTx, fundingByOutPoint map[OutPoint]InvokeCallBinding, minBindings int) ([]InvokeCallBinding, error) {
	bindings, err := BindResultTxInvokes(resultTx, fundingByOutPoint)
	if err != nil {
		return nil, err
	}
	if len(bindings) < minBindings {
		return nil, fmt.Errorf("result transaction binds %d invokes, require at least %d", len(bindings), minBindings)
	}
	return bindings, nil
}
