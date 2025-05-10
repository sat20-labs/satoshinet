package common

import (
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	DB_KEY_UTXO         = "u-"  // utxo -> UtxoValueInDB
	DB_KEY_ADDRESS      = "a-"  // address -> addressId
	DB_KEY_ADDRESSVALUE = "av-" // addressId-utxoId -> value
	DB_KEY_UTXOID       = "ui-" // utxoId -> utxo
	DB_KEY_ADDRESSID    = "ai-" // addressId -> address
	DB_KEY_BLOCK        = "b-"  // height -> block
)

// Address Type defined in txscript.ScriptClass

type UtxoValueInDB struct {
	UtxoId      uint64
	Value       int64
	AddressType uint16
	ReqSig      uint16
	AddressIds  []uint64
	Assets      wire.TxAssets
}

type BlockValueInDB struct {
	Height     int
	Timestamp  int64
	InputUtxo  int
	OutputUtxo int
	InputSats  int64
	OutputSats int64
	TxAmount   int
}

type BlockInfo struct {
	Height     int   `json:"height"`
	Timestamp  int64 `json:"timestamp"`
	InputUtxo  int   `json:"inpututxos"`
	OutputUtxo int   `json:"outpututxos"`
	InputSats  int64 `json:"inputsats"`
	OutputSats int64 `json:"outputsats"`
	TxAmount   int   `json:"txamount"`
}

type TickerName = wire.AssetName

type UtxoInfo struct {
	UtxoId   uint64
	Value    int64
	PkScript []byte
	Assets   wire.TxAssets
}

type TickerInfo struct {
	wire.AssetName
	MaxSupply string
	Precition int
	N         int
}


// UtxoL1 进入聪网，TxIdL2是进入交易
type AscendData struct {
	Height      int           `json:"height"`
	FundingUtxo string        `json:"fundingUtxo"`
	AnchorTxId  string        `json:"anchorTxId"`
	Value       int64         `json:"value"`
	Assets      wire.TxAssets `json:"assets"`
	Sig         []byte        `json:"invoiceSig"`

	Address string `json:"address"` // 通道地址
	PubA    []byte `json:"pubKeyA"` // 服务节点
	PubB    []byte `json:"puKeyB"`
}

// UtxoL2 离开聪网，TxIdL1是回到主网
type DescendData struct {
	Height       int           `json:"height"`
	DescendTxId  string        `json:"descendTxId"`
	NullDataUtxo string        `json:"opReturn"`
	Value        int64         `json:"value"`
	Assets       wire.TxAssets `json:"assets"`

	Address      string        `json:"address"` // 通道地址
}

type TxdRecord struct {
	TxdDBKey 	string  // 上升或下降在DBKey的前缀中包含
	Height   	int
}

type ChannelInfoInDB struct {
	Address 	string
	PubA		[]byte
	PubB		[]byte
	//Records     []*TxdRecord
	// more 
}

type ChannelInfo struct {
	ChannelInfoInDB
	IsNew    bool
}