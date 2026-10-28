package common

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

const BTCLuckyVersion = 1

// The source outpoint, value and participants already exist in the signed
// anchor. Only its Bitcoin block identity needs an additional commitment.
type BTCLuckyMetadata struct {
	Version     uint8          `json:"version"`
	BlockHeight uint32         `json:"btcHeight"`
	BlockHash   chainhash.Hash `json:"btcBlockHash"`
}

func (m BTCLuckyMetadata) Script() ([]byte, error) {
	if m.Version != BTCLuckyVersion || m.BlockHeight == 0 || m.BlockHash == (chainhash.Hash{}) {
		return nil, fmt.Errorf("invalid BTC lucky metadata")
	}
	payload := make([]byte, 37)
	payload[0] = m.Version
	binary.LittleEndian.PutUint32(payload[1:5], m.BlockHeight)
	copy(payload[5:], m.BlockHash[:])
	return NullDataScript(CONTENT_TYPE_BTC_LUCKY, payload)
}

// BTCLuckyMetadataFromOutputs recognizes malformed framing as well as valid
// markers. Callers must authenticate the outputs through the anchor invoice.
// Ordinary anchors have no marker; there is no legacy Lucky format.
func BTCLuckyMetadataFromOutputs(outputs []*wire.TxOut) (*BTCLuckyMetadata, error) {
	var result *BTCLuckyMetadata
	for _, out := range outputs {
		if out == nil {
			return nil, fmt.Errorf("nil anchor output")
		}
		tokenizer := txscript.MakeScriptTokenizer(0, out.PkScript)
		if !tokenizer.Next() || tokenizer.Opcode() != txscript.OP_RETURN || !tokenizer.Next() || tokenizer.Opcode() != SAT20_MAGIC_NUMBER ||
			!tokenizer.Next() || len(tokenizer.Data()) > 8 || tokenizer.ExtractInt64() != CONTENT_TYPE_BTC_LUCKY {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("duplicate BTC lucky marker")
		}
		_, payload, err := ReadDataFromNullDataScript(out.PkScript)
		if err != nil || len(payload) != 37 {
			return nil, fmt.Errorf("malformed BTC lucky marker")
		}
		m := &BTCLuckyMetadata{Version: payload[0], BlockHeight: binary.LittleEndian.Uint32(payload[1:5])}
		copy(m.BlockHash[:], payload[5:])
		canonical, err := m.Script()
		if err != nil || out.Value != 0 || len(out.Assets) != 0 || !bytes.Equal(canonical, out.PkScript) {
			return nil, fmt.Errorf("noncanonical BTC lucky marker")
		}
		result = m
	}
	return result, nil
}

type BTCLuckyBlock struct {
	BTCHeight     uint32 `json:"btcHeight"`
	BTCBlockHash  string `json:"btcBlockHash"`
	FundingUtxo   string `json:"fundingUtxo"`
	RewardSats    int64  `json:"rewardSats"`
	MinerPubKey   string `json:"minerPubKey"`
	ServicePubKey string `json:"servicePubKey"`
	RewardAddress string `json:"rewardAddress"`
	L2Height      int    `json:"l2Height"`
	AnchorTxID    string `json:"anchorTxId"`
}

type BTCLuckyBlocks struct {
	IndexedHeight   int             `json:"indexedHeight"`
	Total           int             `json:"total"`
	MinedBlockCount int             `json:"minedBlockCount"`
	TotalRewardSats int64           `json:"totalRewardSats"`
	Start           int             `json:"start"`
	Limit           int             `json:"limit"`
	Items           []BTCLuckyBlock `json:"items"`
}
