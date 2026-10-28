package base

import (
	"encoding/hex"
	"fmt"
	"github.com/sat20-labs/satoshinet/wire"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"

	"github.com/sat20-labs/satoshinet/indexer/common"
)

// genAscendFromAnchorPkScript requires b.mutex held by block indexing.
func (b *BaseIndexer) genAscendFromAnchorPkScript(anchorPkScript []byte, outputs []*wire.TxOut, bindOutputs bool) (*common.AscendData, error) {
	ascend, err := anchortx.CheckAnchorPkScriptWithCoreCheck(anchorPkScript, false, b.isAnchorCoreNode, outputs, bindOutputs)
	if err != nil {
		return nil, err
	}
	data := ascendData(ascend)
	data.BTCLucky, err = common.BTCLuckyMetadataFromOutputs(outputs)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// genAscendFromAnchorTx preserves the native witness during indexing and
// replay. Historical RGB validation does not require today's funding UTXO to
// remain unspent. The caller already holds b.mutex.
func (b *BaseIndexer) genAscendFromAnchorTx(tx *wire.MsgTx, height int32) (*common.AscendData, error) {
	_, native, err := common.NativeAssetProof(tx)
	if err != nil {
		return nil, err
	}
	if !native {
		if tx == nil || len(tx.TxIn) != 1 || tx.TxIn[0] == nil {
			return nil, fmt.Errorf("invalid anchor transaction")
		}
		return b.genAscendFromAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, b.chaincfgParam.POSV2Active(height))
	}
	ascend, err := anchortx.CheckAnchorTxValidWithCoreCheck(tx, false, b.chaincfgParam.POSV2Active(height),
		b.chaincfgParam.NativeAssetsActive(height), b.isAnchorCoreNode)
	if err != nil {
		return nil, err
	}
	data := ascendData(ascend)
	data.BTCLucky, err = common.BTCLuckyMetadataFromOutputs(tx.TxOut)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (b *BaseIndexer) isAnchorCoreNode(pub []byte) bool {
	key := hex.EncodeToString(pub)
	if key == indexer.GetBootstrapPubKey() || key == indexer.GetCoreNodePubKey() {
		return true
	}
	_, ok := b.coreNodeMap[key]
	return ok
}

func ascendData(ascend *anchortx.AscendInfo) *common.AscendData {
	return &common.AscendData{
		FundingUtxo: ascend.Utxo,
		Value:       ascend.Value,
		Assets:      ascend.TxAssets,
		Sig:         ascend.Sig,
		Address:     ascend.Address,
		PubA:        ascend.PubKeyA,
		PubB:        ascend.PubKeyB,
	}
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
	if tx == nil || index < 0 || index >= len(tx.Outputs) || len(tx.Inputs) == 0 ||
		tx.Inputs[0] == nil || tx.Inputs[0].Address == nil || len(tx.Inputs[0].Address.Addresses) == 0 {
		return nil, fmt.Errorf("descending requires an ordinary input address")
	}

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
