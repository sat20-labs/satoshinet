package base

import (
	"strconv"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/chaincfg"

	"github.com/sat20-labs/satoshinet/indexer/common"
)

// anchorPkScript, err := StandardAnchorScript(txid, witnessScript, amount)
func GenAscendFromAnchorPkScript(anchorPkScript []byte, netParams *chaincfg.Params) (*common.AscendData, error) {

	ascend, err := anchortx.CheckAnchorPkScript(anchorPkScript, false)
	if err != nil {
		return nil, err
	}

	return &common.AscendData{
		FundingUtxo: ascend.Utxo,
		Value:       ascend.Value,
		Assets:      ascend.TxAssets,
		Sig:         ascend.Sig,
		Address:     ascend.Address,
		PubA:        ascend.PubKeyA,
		PubB:        ascend.PubKeyB,
	}, nil
}


func GenDescend(tx *common.Transaction, index int, descendTxId string) (*common.DescendData, error) {

	var result common.DescendData
	output := tx.Outputs[index]
	result.NullDataUtxo = tx.Txid + ":" + strconv.Itoa(index)
	result.DescendTxId = descendTxId
	result.Value = output.Value
	result.Assets = output.Assets
	result.Address = tx.Inputs[0].Address.Addresses[0]

	return &result, nil
}

