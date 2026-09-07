package common

import (
	"encoding/hex"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
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
	N               int
	Divisibility    int
	MaxSupply       *indexer.Decimal
	TotalAscendAmt  *indexer.Decimal
	TotalDescendAmt *indexer.Decimal
	HolderCount     int
}

// UtxoL1 进入聪网，TxIdL2是进入交易
type AscendData struct {
	Height      int           `json:"height"`      // L2
	FundingUtxo string        `json:"fundingUtxo"` // L1
	AnchorTxId  string        `json:"anchorTxId"`  // L2
	Value       int64         `json:"value"`
	Assets      wire.TxAssets `json:"assets"` // 最多一种资产
	Sig         []byte        `json:"invoiceSig"`

	Address string `json:"address"` // 通道地址
	PubA    []byte `json:"pubKeyA"` // 服务节点
	PubB    []byte `json:"puKeyB"`
}

func (p *AscendData) ToMinerInfo() *MinerInfo {
	var name, amt string
	if len(p.Assets) > 0 {
		name = p.Assets[0].Name.String()
		amt = p.Assets[0].Amount.String()
	} else {
		amt = fmt.Sprintf("%d", p.Value)
	}
	return &MinerInfo{
		AscendHeight: p.Height,
		AscendUtxo:   p.FundingUtxo,
		AnchorTxId:   p.AnchorTxId,
		AssetName:    name,
		AssetAmt:     amt,
		ChannelAddr:  p.Address,
		ServerNode:   hex.EncodeToString(p.PubA),
	}
}

// UtxoL2 离开聪网，TxIdL1是回到主网
type DescendData struct {
	Height                 int           `json:"height"`
	DescendTxId            string        `json:"descendTxId"` // L1
	NullDataUtxo           string        `json:"opReturn"`    // L2的utxo，包含了下降的资产信息
	Value                  int64         `json:"value"`
	Assets                 wire.TxAssets `json:"assets"`  // 支持很多种资产
	Address                string        `json:"address"` // 通道地址
	Version                int           `json:"version"`
	Operation              uint8         `json:"operation"`
	LegacyPayload          string        `json:"legacyPayload,omitempty"`
	ReturnedChannelOutputs []string      `json:"returnedChannelOutputs,omitempty"` // L1 outpoints returned to the same channel address
}

type ChannelLedgerEntry struct {
	ChannelId              string        `json:"channel"`
	Direction              string        `json:"direction"` // ascending or descending
	Operation              string        `json:"operation"`
	L2TxId                 string        `json:"l2TxId"`
	L2Height               int           `json:"l2Height"`
	NullDataUtxo           string        `json:"nullDataUtxo,omitempty"`
	L1TxId                 string        `json:"l1TxId,omitempty"`
	L1Outpoints            []string      `json:"l1Outpoints,omitempty"`
	ReturnedChannelOutputs []string      `json:"returnedChannelOutputs,omitempty"`
	Value                  int64         `json:"value"`
	Assets                 wire.TxAssets `json:"assets"`
	Legacy                 bool          `json:"legacy"`
}

const (
	CHANNEL_EVENT_LATEST_FORCE_CLOSE             = "latest_force_close"
	CHANNEL_EVENT_REVOKED_COMMITMENT_BROADCASTED = "revoked_commitment_broadcasted"
	CHANNEL_EVENT_UNKNOWN_CHANNEL_SPEND          = "unknown_channel_spend"
	CHANNEL_EVENT_L2_DRAINED                     = "l2_drained"

	CHANNEL_EVENT_STATUS_OBSERVED      = "observed"
	CHANNEL_EVENT_STATUS_PUNISHED      = "punished"
	CHANNEL_EVENT_STATUS_DRAINED       = "drained"
	CHANNEL_EVENT_STATUS_EXPIRED       = "expired"
	CHANNEL_EVENT_STATUS_MANUAL_REVIEW = "manual_review"
)

type ChannelStateEvent struct {
	ChannelId            string   `json:"channel"`
	ChannelPoint         string   `json:"channelPoint,omitempty"`
	EventType            string   `json:"eventType"`
	Status               string   `json:"status"`
	ObservedL1TxId       string   `json:"observedL1TxId"`
	ObservedL1Height     int      `json:"observedL1Height,omitempty"`
	ObservedCommitHeight int      `json:"observedCommitHeight,omitempty"`
	CurrentCommitHeight  int      `json:"currentCommitHeight,omitempty"`
	L2Height             int      `json:"l2Height,omitempty"`
	Source               string   `json:"source,omitempty"`
	PunishTxIds          []string `json:"punishTxIds,omitempty"`
	Message              string   `json:"message,omitempty"`
	CreatedAt            int64    `json:"createdAt"`
}

func DescendOperationName(operation uint8) string {
	switch operation {
	case DESCEND_OP_SPLICING_OUT:
		return "splicing_out"
	case DESCEND_OP_CLOSE:
		return "close"
	case DESCEND_OP_FORCE_CLOSE:
		return "force_close"
	default:
		return "unknown"
	}
}

type MinerAscendInfo struct {
	AscendHeight int    // L2
	AscendUtxo   string // L1
}

type CoreNodeInfo struct {
	MinerInfo
	ChildMiners map[string]*MinerAscendInfo // pubkey->ascend
}

type MinerInfo struct {
	AscendHeight int    // L2
	AscendUtxo   string // L1
	AnchorTxId   string // L2
	AssetName    string
	AssetAmt     string
	ServerNode   string // 服务端公钥
	ChannelAddr  string
}

func NewCoreNodeInfo(data *AscendData) *CoreNodeInfo {
	if data == nil {
		return &CoreNodeInfo{
			MinerInfo: MinerInfo{
				AscendHeight: 0,
				AscendUtxo:   "",
				AnchorTxId:   "",
				AssetName:    "",
				AssetAmt:     "0",
				ChannelAddr:  "",
				ServerNode:   "",
			},
			ChildMiners: make(map[string]*MinerAscendInfo),
		}
	}

	return &CoreNodeInfo{
		MinerInfo:   *data.ToMinerInfo(),
		ChildMiners: make(map[string]*MinerAscendInfo),
	}
}

func (p *CoreNodeInfo) Clone() *CoreNodeInfo {
	if p == nil {
		return nil
	}
	n := &CoreNodeInfo{
		MinerInfo:   p.MinerInfo,
		ChildMiners: make(map[string]*MinerAscendInfo),
	}
	for k2, v2 := range p.ChildMiners {
		if v2 == nil {
			n.ChildMiners[k2] = nil
			continue
		}
		cloned := *v2
		n.ChildMiners[k2] = &cloned
	}
	return n
}

type TxdRecord struct {
	TxdDBKey string // 上升或下降在DBKey的前缀中包含
	Height   int
}

type ChannelInfoInDB struct {
	Address string
	PubA    []byte
	PubB    []byte
	//Records     []*TxdRecord
	// more
}

type ChannelInfo struct {
	ChannelInfoInDB
	IsNew bool
}

type MinerNodeInfo struct {
	PubKey      []byte
	Address     string
	ChannelAddr string
	ChildNodes  []*MinerNodeInfo
}

type MinningSequence struct {
	BootstrapNode *MinerNodeInfo
	CodeNodes     []*MinerNodeInfo
}
