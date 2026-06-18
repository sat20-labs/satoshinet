package framework

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func NewUnsignedTx(inputs []wire.OutPoint) *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	for _, input := range inputs {
		tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	}
	return tx
}

func AddTxOutCopies(tx *wire.MsgTx, outputs []*wire.TxOut) {
	for _, output := range outputs {
		if output == nil {
			continue
		}
		tx.AddTxOut(&wire.TxOut{
			Value:    output.Value,
			PkScript: cloneBytes(output.PkScript),
			Assets:   output.Assets.Clone(),
		})
	}
}

func AddContractPayloadOutput(tx *wire.MsgTx, txType contract.TxType, payload []byte) error {
	scripts, err := contract.NullDataScripts(txType, payload)
	if err != nil {
		return err
	}
	for _, script := range scripts {
		tx.AddTxOut(&wire.TxOut{Value: 0, PkScript: script})
	}
	return nil
}

func NewContractTxOut(value int64, assets wire.TxAssets,
	contractAddr contract.ContractAddress) (*wire.TxOut, error) {

	script, err := contract.ContractPkScript(contractAddr)
	if err != nil {
		return nil, err
	}
	return &wire.TxOut{
		Value:    value,
		Assets:   assets.Clone(),
		PkScript: script,
	}, nil
}

func ContractTxOutFromFunding(funding wire.TxOut,
	contractAddr contract.ContractAddress) (*wire.TxOut, error) {

	return NewContractTxOut(funding.Value, funding.Assets, contractAddr)
}

func MsgTxHex(tx *wire.MsgTx) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("missing transaction")
	}
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf.Bytes()), nil
}

func ParseOutPoint(s string) (wire.OutPoint, error) {
	outpoint, err := wire.NewOutPointFromString(s)
	if err == nil {
		return *outpoint, nil
	}
	hash, hashErr := chainhash.NewHashFromStr(s)
	if hashErr == nil {
		return wire.OutPoint{Hash: *hash, Index: 0}, nil
	}
	return wire.OutPoint{}, err
}
