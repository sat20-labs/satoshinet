package common

import (
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type Input struct {
	Txid            string `json:"txid"`
	UtxoId          uint64
	Address         *ScriptPubKey  `json:"scriptPubKey"`
	Vout            uint32         `json:"vout"`
	Assets          wire.TxAssets  `json:"assets"`
	Value           int64          `json:"value"`
	Witness         wire.TxWitness `json:"witness"`
	SignatureScript []byte         `json:"sigScript"`
}

type ScriptPubKey struct {
	Addresses []string `json:"addresses"`
	Type      int      `json:"type"`
	ReqSig    int      `json:"reqSig"`
	PkScript  []byte   `json:"pkscript"`
}

type Output struct {
	Height  int           `json:"height"`
	TxId    int           `json:"txid"`
	Value   int64         `json:"value"`
	Address *ScriptPubKey `json:"scriptPubKey"`
	N       uint32        `json:"n"`
	Assets  wire.TxAssets `json:"assets"`
}

type Transaction struct {
	Txid    string      `json:"txid"`
	Inputs  []*Input    `json:"inputs"`
	Outputs []*Output   `json:"outputs"`
	MsgTx   *wire.MsgTx `json:"-"`
}

type Block struct {
	Timestamp     time.Time      `json:"timestamp"`
	Height        int            `json:"height"`
	Hash          string         `json:"hash"`
	PrevBlockHash string         `json:"prevBlockHash"`
	Transactions  []*Transaction `json:"transactions"`
}

type ReferrerInfo struct {
	Name      string
	BindBlock int
}

type UTXOIndex struct {
	Index                map[string]*Output
	AscendMap            map[string]*AscendData
	DescendMap           map[string]*DescendData
	ChannelLedgerMap     map[string]*ChannelLedgerEntry
	ChannelStateEventMap map[string]*ChannelStateEvent
	ReferrerMap          map[string]*ReferrerInfo // 被推荐人地址-》推荐人名字
}

func NewUTXOIndex() *UTXOIndex {
	return &UTXOIndex{
		Index:                make(map[string]*Output),
		AscendMap:            make(map[string]*AscendData),
		DescendMap:           make(map[string]*DescendData),
		ChannelLedgerMap:     make(map[string]*ChannelLedgerEntry),
		ChannelStateEventMap: make(map[string]*ChannelStateEvent),
		ReferrerMap:          make(map[string]*ReferrerInfo),
	}
}

func GetUtxoId(addrAndId *Output) uint64 {
	return indexer.ToUtxoId(addrAndId.Height, addrAndId.TxId, int(addrAndId.N))
}
