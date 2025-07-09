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
	Txid    string    `json:"txid"`
	Inputs  []*Input  `json:"inputs"`
	Outputs []*Output `json:"outputs"`
}

type Block struct {
	Timestamp     time.Time      `json:"timestamp"`
	Height        int            `json:"height"`
	Hash          string         `json:"hash"`
	PrevBlockHash string         `json:"prevBlockHash"`
	Transactions  []*Transaction `json:"transactions"`
}

type UTXOIndex struct {
	Index      map[string]*Output
	AscendMap  map[string]*AscendData
	DescendMap map[string]*DescendData
}

func NewUTXOIndex() *UTXOIndex {
	return &UTXOIndex{
		Index:      make(map[string]*Output),
		AscendMap:  make(map[string]*AscendData),
		DescendMap: make(map[string]*DescendData),
	}
}

func GetUtxoId(addrAndId *Output) uint64 {
	return indexer.ToUtxoId(addrAndId.Height, addrAndId.TxId, int(addrAndId.N))
}

