package framework

import (
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func ContractTxFromParsed(tx *wire.MsgTx, parsed ParsedTx, contractType byte) contract.Tx {
	out := contract.Tx{
		Kind:         parsed.Type,
		ContractType: contractType,
		Payload:      CloneBytes(parsed.Payload),
		PayloadIndex: parsed.PayloadIndex,
		Funding:      ContractFundingOutputs(parsed.ContractOutputs),
		Inputs:       ContractTxInputs(tx),
	}
	if tx != nil {
		out.TxID = tx.TxID()
	}
	if len(parsed.ContractOutputs) != 0 {
		out.Contract = parsed.ContractOutputs[0].Contract
	}
	if parsed.Deploy != nil {
		out.Subtype = parsed.Deploy.Name
		out.Version = parsed.Deploy.Version
		out.GasLimit = parsed.Deploy.GasLimit
		out.Nonce = parsed.Deploy.Nonce
		out.Payload = CloneBytes(parsed.Deploy.Code)
	}
	if parsed.Invoke != nil {
		out.Action = parsed.Invoke.Action
		out.GasLimit = parsed.Invoke.GasLimit
		out.Nonce = parsed.Invoke.CallNonce
		out.Payload = CloneBytes(parsed.Invoke.Data)
	}
	if parsed.Result != nil {
		cp := *parsed.Result
		out.Result = &cp
	}
	if parsed.StateRoot != nil {
		cp := *parsed.StateRoot
		out.StateRoot = &cp
	}
	return out
}

func ContractFundingOutputs(outputs []ContractOutput) []contract.FundingOutput {
	out := make([]contract.FundingOutput, len(outputs))
	for i := range outputs {
		out[i] = contract.FundingOutput{
			OutPoint: ContractTxOutPoint(outputs[i].OutPoint),
			Vout:     outputs[i].Vout,
			Contract: outputs[i].Contract,
			Value:    outputs[i].Value,
			Assets:   outputs[i].Assets.Clone(),
			PkScript: CloneBytes(outputs[i].PkScript),
		}
	}
	return out
}

func ContractTxInputs(tx *wire.MsgTx) []contract.TxInput {
	if tx == nil {
		return nil
	}
	inputs := make([]contract.TxInput, 0, len(tx.TxIn))
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			continue
		}
		inputs = append(inputs, contract.TxInput{
			PreviousOutPoint: contract.TxOutPoint{
				TxID: txIn.PreviousOutPoint.Hash.String(),
				Vout: txIn.PreviousOutPoint.Index,
			},
		})
	}
	return inputs
}

func ContractTxOutPoint(outpoint OutPoint) contract.TxOutPoint {
	return contract.TxOutPoint{TxID: outpoint.TxID, Vout: outpoint.Vout}
}
