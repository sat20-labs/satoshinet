package validatechain

import (
	"bytes"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

type EPBlockHeader struct {
	Hash       chainhash.Hash // 块Hash
	CreateTime int64          // 块创建的时间
	Version    uint32         // 块版本
}

// EpochVote Data
type DataEpochVote struct {
	VotorId       string                               // 投票者ID
	EpochIndex    int64                                // 要投票Epoch Index
	CreateTime    int64                                // 要投票的Epoch创建时间
	Reason        uint32                               // 要投票的Epoch发起原因（Epoch创立，Epoch轮换，当前Epoch停摆）
	EpochItemList []epoch.EpochItem                    // 选择的Epoch的ItemList
	Token         string                               // Token
}

type EPBlock struct {
	Header  EPBlockHeader
	Data    *DataEpochVote
	payload []byte
}

func NewEPBlock() *EPBlock {
	epBlock := &EPBlock{
		Header: EPBlockHeader{
			CreateTime: time.Now().Unix(),
			Version:    uint32(Version_ValidateChain),
		}}

	return epBlock
}

func (epb *EPBlock) GetHash() (*chainhash.Hash, error) {
	if epb.Header.Hash.IsEqual(&chainhash.Hash{}) {
		hash, err := epb.CalcBlockHash()
		if err != nil {
			return nil, err
		}
		return hash, nil
	}
	return &epb.Header.Hash, nil
}

func (epb *EPBlock) CalcBlockHash() (*chainhash.Hash, error) {
	if epb.payload == nil {
		payload, err := epb.EncodeData()
		if err != nil {
			return nil, err
		}
		epb.payload = payload
	}

	hash := chainhash.DoubleHashH(epb.payload)
	epb.Header.Hash = hash
	return &hash, nil
}

func (epb *EPBlock) Encode() ([]byte, error) {
	// Encode the VC block payload.
	var bw bytes.Buffer

	// Encode the block header.
	err := epb.Header.Encode(&bw)
	if err != nil {
		return nil, err
	}

	// Encode the block Data.
	if epb.payload == nil {
		payload, err := epb.EncodeData()
		if err != nil {
			return nil, err
		}
		epb.payload = payload
	}

	bw.Write(epb.payload)

	payloadBlock := bw.Bytes()
	return payloadBlock, nil
}

func (epb *EPBlock) EncodeData() ([]byte, error) {
	// Encode the VC block payload.
	var bw bytes.Buffer

	err := epb.Data.Encode(&bw)
	if err != nil {
		return nil, err
	}

	payload := bw.Bytes()
	return payload, nil
}
func (epb *EPBlock) Decode(stateData []byte) error {

	br := bytes.NewReader(stateData)
	err := epb.Header.Decode(br)
	if err != nil {
		return err
	}

	epBlockData := &DataEpochVote{}
	err = epBlockData.Decode(br)
	if err != nil {
		return err
	}
	epb.Data = epBlockData

	return nil
}

/*
Hash       chainhash.Hash // 块Hash
Height     uint32         // 块高度， 高度从0开始
PrevHash   chainhash.Hash // 前序块的Hash
CreateTime int64          // 块创建的时间
Version    uint32         // 块版本
DataType   uint32         // 块数据类型-- Epoch创建， Epoch更新（Epoch转正，成员删除，generator更新）
*/
func (bh *EPBlockHeader) Encode(w io.Writer) error {
	// Encode the VC block header.
	err := utils.WriteElements(w,
		bh.Hash,
		bh.CreateTime,
		bh.Version)
	if err != nil {
		return err
	}
	return nil
}

func (bh *EPBlockHeader) Decode(r io.Reader) error {
	err := utils.ReadElements(r,
		&bh.Hash,
		&bh.CreateTime,
		&bh.Version)

	return err
}

// EpochVote Data
//
//	type DataEpochVote struct {
//		VotorId       uint64                               // 投票者ID
//		PublicKey     [btcec.PubKeyBytesLenCompressed]byte // 投票者公钥
//		EpochIndex    uint32                               // 要投票Epoch Index
//		CreateTime    int64                                // 要投票的Epoch创建时间
//		Reason        uint32                               // 要投票的Epoch发起原因（Epoch创立，Epoch轮换，当前Epoch停摆）
//		EpochItemList []epoch.EpochItem                    // 选择的Epoch的ItemList
//		Token         string                               // Token
//	}
func (ev *DataEpochVote) Encode(w io.Writer) error {
	// Encode New epoch data.
	err := utils.WriteElements(w,
		ev.VotorId,
		ev.EpochIndex,
		ev.CreateTime,
		ev.Reason)
	if err != nil {
		return err
	}

	// Encode epoch item list.
	err = utils.WriteVarInt(w, uint64(len(ev.EpochItemList)))
	if err != nil {
		return err
	}
	for _, item := range ev.EpochItemList {
		err := utils.WriteElements(w,
			item.ValidatorId,
			item.Host)
		if err != nil {
			return err
		}
	}

	// Encode epoch vote token.
	err = utils.WriteElements(w, ev.Token)
	if err != nil {
		return err
	}

	return nil
}

func (ev *DataEpochVote) Decode(r io.Reader) error {
	// Encode New epoch data.
	err := utils.ReadElements(r,
		&ev.VotorId,
		&ev.EpochIndex,
		&ev.CreateTime,
		&ev.Reason)
	if err != nil {
		return err
	}

	// Decode epoch item list.
	count, err := utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	ev.EpochItemList = make([]epoch.EpochItem, 0)
	for i := 0; i < int(count); i++ {
		item := epoch.EpochItem{}
		err := utils.ReadElements(r,
			&item.ValidatorId,
			&item.Host)
		if err != nil {
			return err
		}
		ev.EpochItemList = append(ev.EpochItemList, item)
	}

	// Decode epoch vote token.
	err = utils.ReadElements(r,
		&ev.Token)
	if err != nil {
		return err
	}

	return nil
}
