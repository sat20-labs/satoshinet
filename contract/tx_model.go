package contract

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type TxOutPoint struct {
	TxID string `json:"txid"`
	Vout uint32 `json:"vout"`
}

func (o TxOutPoint) String() string {
	return fmt.Sprintf("%s:%d", o.TxID, o.Vout)
}

type TxInput struct {
	PreviousOutPoint TxOutPoint `json:"previousOutPoint"`
	PkScript         []byte     `json:"pkScript,omitempty"`
	Address          string     `json:"address,omitempty"`
}

type FundingOutput struct {
	OutPoint TxOutPoint      `json:"outPoint"`
	Vout     uint32          `json:"vout"`
	Contract ContractAddress `json:"contract"`
	Value    int64           `json:"value"`
	Assets   wire.TxAssets   `json:"assets,omitempty"`
	PkScript []byte          `json:"pkScript,omitempty"`
}

func (o FundingOutput) Clone() FundingOutput {
	return FundingOutput{
		OutPoint: o.OutPoint,
		Vout:     o.Vout,
		Contract: o.Contract,
		Value:    o.Value,
		Assets:   o.Assets.Clone(),
		PkScript: cloneBytes(o.PkScript),
	}
}

type Tx struct {
	TxID               string            `json:"txid"`
	Kind               TxType            `json:"kind"`
	ContractType       byte              `json:"contractType"`
	Contract           ContractAddress   `json:"contract"`
	Subtype            string            `json:"subtype,omitempty"`
	Version            uint32            `json:"version,omitempty"`
	Action             string            `json:"action,omitempty"`
	Actor              string            `json:"actor,omitempty"`
	GasRefundRecipient string            `json:"gasRefundRecipient,omitempty"`
	GasLimit           int64             `json:"gasLimit,omitempty"`
	Nonce              uint64            `json:"nonce,omitempty"`
	Payload            []byte            `json:"payload,omitempty"`
	Funding            []FundingOutput   `json:"funding,omitempty"`
	Inputs             []TxInput         `json:"inputs,omitempty"`
	PayloadIndex       int               `json:"payloadIndex,omitempty"`
	Result             *ResultPayload    `json:"result,omitempty"`
	StateRoot          *StateRootPayload `json:"stateRoot,omitempty"`
}

func (tx Tx) Clone() Tx {
	out := tx
	out.Payload = cloneBytes(tx.Payload)
	out.Funding = make([]FundingOutput, len(tx.Funding))
	for i := range tx.Funding {
		out.Funding[i] = tx.Funding[i].Clone()
	}
	out.Inputs = append([]TxInput(nil), tx.Inputs...)
	if tx.Result != nil {
		cp := *tx.Result
		out.Result = &cp
	}
	if tx.StateRoot != nil {
		cp := *tx.StateRoot
		out.StateRoot = &cp
	}
	return out
}
