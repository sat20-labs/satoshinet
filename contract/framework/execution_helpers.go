package framework

import (
	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func ContractOutputFromDefaultInvoke(output contract.DefaultInvokeOutput) ContractOutput {
	return ContractOutput{
		OutPoint: OutPoint{TxID: output.TxID, Vout: output.Vout},
		Vout:     output.Vout,
		Contract: output.Contract,
		Value:    output.Value,
		Assets:   output.Assets.Clone(),
		PkScript: CloneBytes(output.PkScript),
	}
}

func MsgTxInputs(tx *wire.MsgTx) []OutPoint {
	inputs := make([]OutPoint, 0, len(tx.TxIn))
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			continue
		}
		inputs = append(inputs, WireOutPointToFramework(txIn.PreviousOutPoint))
	}
	return inputs
}

func ContractOutputOutPoints(outputs []ContractOutput) []OutPoint {
	out := make([]OutPoint, 0, len(outputs))
	for _, output := range outputs {
		out = append(out, output.OutPoint)
	}
	return out
}

func AddGasFees(a, b *scommon.Decimal) (*scommon.Decimal, error) {
	if a == nil || a.Sign() == 0 {
		if b == nil {
			return nil, nil
		}
		return b.Clone(), nil
	}
	if b == nil || b.Sign() == 0 {
		return a.Clone(), nil
	}
	return a.AddAlignPrecision(b), nil
}

func GasFeeIf(ok bool, fee *scommon.Decimal) *scommon.Decimal {
	if ok {
		return CloneDecimal(fee)
	}
	return nil
}

func OutputHasRequiredGas(output ContractOutput, gasAssetName string, required *scommon.Decimal) (bool, error) {
	if required == nil || required.Sign() == 0 {
		return true, nil
	}
	if gasAssetName == "" {
		return false, nil
	}
	amount, err := output.AssetAmount(gasAssetName)
	if err != nil {
		return false, err
	}
	return amount != nil && amount.Cmp(required) >= 0, nil
}

func OutputsHaveRequiredGas(outputs []ContractOutput, gasAssetName string, required *scommon.Decimal) (bool, error) {
	if required == nil || required.Sign() == 0 {
		return true, nil
	}
	if gasAssetName == "" {
		return false, nil
	}
	total := scommon.NewDecimal(0, required.Precision)
	for _, output := range outputs {
		amount, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return false, err
		}
		if amount != nil {
			total = total.AddAlignPrecision(amount)
		}
	}
	return total.Cmp(required) >= 0, nil
}
