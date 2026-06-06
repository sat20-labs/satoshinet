package base

import (
	"strconv"
	"strings"

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

func NewAscendingLedgerEntry(ascend *common.AscendData) *common.ChannelLedgerEntry {
	if ascend == nil {
		return nil
	}
	return &common.ChannelLedgerEntry{
		ChannelId:   ascend.Address,
		Direction:   "ascending",
		Operation:   "ascending",
		L2TxId:      ascend.AnchorTxId,
		L2Height:    ascend.Height,
		L1TxId:      strings.Split(ascend.FundingUtxo, ":")[0],
		L1Outpoints: []string{ascend.FundingUtxo},
		Value:       ascend.Value,
		Assets:      ascend.Assets,
		Legacy:      false,
	}
}

func NewDescendingLedgerEntry(descend *common.DescendData) *common.ChannelLedgerEntry {
	if descend == nil {
		return nil
	}
	return &common.ChannelLedgerEntry{
		ChannelId:              descend.Address,
		Direction:              "descending",
		Operation:              common.DescendOperationName(descend.Operation),
		L2TxId:                 strings.Split(descend.NullDataUtxo, ":")[0],
		L2Height:               descend.Height,
		NullDataUtxo:           descend.NullDataUtxo,
		L1TxId:                 descend.DescendTxId,
		ReturnedChannelOutputs: descend.ReturnedChannelOutputs,
		Value:                  descend.Value,
		Assets:                 descend.Assets,
		Legacy:                 descend.Version < 2,
	}
}

func GenDescend(tx *common.Transaction, index, height int, descendTxId string) (*common.DescendData, error) {

	var result common.DescendData
	result.Height = height
	output := tx.Outputs[index]
	result.NullDataUtxo = tx.Txid + ":" + strconv.Itoa(index)
	result.Value = output.Value
	result.Assets = output.Assets
	result.Address = tx.Inputs[0].Address.Addresses[0]
	payload, err := common.ParseDescendPayload([]byte(descendTxId))
	if err != nil {
		return nil, err
	}
	result.Version = payload.Version
	result.Operation = payload.Operation
	result.LegacyPayload = payload.LegacyPayload
	result.DescendTxId = payload.L1TxId
	for _, vout := range payload.ReturnedOutputVouts {
		result.ReturnedChannelOutputs = append(result.ReturnedChannelOutputs, payload.L1TxId+":"+strconv.Itoa(int(vout)))
	}

	return &result, nil
}
