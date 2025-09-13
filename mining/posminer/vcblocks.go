package posminer

import (
	"bytes"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
)

const (
	// 块数据类型
	DataType_MinerNewBlock     = 1
)

type VCBlockHeader struct {
	Hash       chainhash.Hash // 块Hash
	Height     int64          // 块高度， 高度从0开始
	PrevHash   chainhash.Hash // 前序块的Hash
	CreateTime int64          // 块创建的时间
	Version    uint32         // 块版本
	DataType   uint32         // 
}

// Generator出块
type DataMinerNewBlock struct {
	GeneratorId   string         // 当前Generator的Id
	Timestamp     int64          // generator出块的时间
	SatsnetHeight int32          // Satsnet出块的高度
	Hash          chainhash.Hash // 出块的Satsnet块Hash
	Token         string         // 出块的Token
}

type VCBlock struct {
	Header VCBlockHeader
	Data   interface{}

	payload []byte
}

func NewVCBlock() *VCBlock {
	vcBlock := &VCBlock{
		Header: VCBlockHeader{
			CreateTime: time.Now().Unix(),
			Version:    uint32(Version_ValidateChain),
		}}

	return vcBlock
}

func (vcb *VCBlock) GetHash() (*chainhash.Hash, error) {
	if isNullHash(vcb.Header.Hash) {
		hash, err := vcb.CalcBlockHash()
		if err != nil {
			return nil, err
		}
		return hash, nil
	}
	return &vcb.Header.Hash, nil
}

func isNullHash(hash chainhash.Hash) bool {
	for _, h := range hash {
		if h != 0 {
			return false
		}
	}
	return true
}

func (vcb *VCBlock) CalcBlockHash() (*chainhash.Hash, error) {
	if vcb.payload == nil {
		payload, err := vcb.EncodeData()
		if err != nil {
			return nil, err
		}
		vcb.payload = payload
	}

	hash := chainhash.DoubleHashH(vcb.payload)
	vcb.Header.Hash = hash
	return &hash, nil
}

func (vcb *VCBlock) Encode() ([]byte, error) {
	// Encode the VC block payload.
	var bw bytes.Buffer

	// Encode the block header.
	err := vcb.Header.Encode(&bw)
	if err != nil {
		return nil, err
	}

	// Encode the block Data.
	if vcb.payload == nil {
		payload, err := vcb.EncodeData()
		if err != nil {
			return nil, err
		}
		vcb.payload = payload
	}

	bw.Write(vcb.payload)

	payloadBlock := bw.Bytes()
	return payloadBlock, nil
}

func (vcb *VCBlock) GetBlockData() ([]byte, error) {
	// Encode the VC block payload.
	var bw bytes.Buffer

	// Encode the block header.
	err := vcb.Header.Encode(&bw)
	if err != nil {
		return nil, err
	}

	// Encode the block Data.
	if vcb.payload == nil {
		payload, err := vcb.EncodeData()
		if err != nil {
			return nil, err
		}
		vcb.payload = payload
	}

	bw.Write(vcb.payload)

	payloadBlock := bw.Bytes()
	return payloadBlock, nil
}

func (vcb *VCBlock) EncodeData() ([]byte, error) {
	// Encode the VC block payload.
	var bw bytes.Buffer

	var err error
	// Encode the block Data.
	switch vcd := vcb.Data.(type) { //nolint:gocritice := vcd.Data.(type)
	case *DataMinerNewBlock:
		err = vcd.Encode(&bw)
	}
	if err != nil {
		return nil, err
	}

	payload := bw.Bytes()
	return payload, nil
}
func (vcb *VCBlock) Decode(stateData []byte) error {

	br := bytes.NewReader(stateData)
	err := vcb.Header.Decode(br)
	if err != nil {
		return err
	}

	switch vcb.Header.DataType {
	

	case DataType_MinerNewBlock:
		newBlock := &DataMinerNewBlock{}
		err = newBlock.Decode(br)
		if err != nil {
			return err
		}
		vcb.Data = newBlock
	}

	return err
}

/*
Hash       chainhash.Hash // 块Hash
Height     uint32         // 块高度， 高度从0开始
PrevHash   chainhash.Hash // 前序块的Hash
CreateTime int64          // 块创建的时间
Version    uint32         // 块版本
DataType   uint32         // 块数据类型-- Epoch创建， Epoch更新（Epoch转正，成员删除，generator更新）
*/
func (bh *VCBlockHeader) Encode(w io.Writer) error {
	// Encode the VC block header.
	err := utils.WriteElements(w,
		bh.Hash,
		bh.Height,
		bh.PrevHash,
		bh.CreateTime,
		bh.Version,
		bh.DataType)
	if err != nil {
		return err
	}
	return nil
}

func (bh *VCBlockHeader) Decode(r io.Reader) error {
	err := utils.ReadElements(r,
		&bh.Hash,
		&bh.Height,
		&bh.PrevHash,
		&bh.CreateTime,
		&bh.Version,
		&bh.DataType)

	return err
}


// Generator出块
// type DataMinerNewBlock struct {
// 	GeneratorId uint64                               // 当前Generator的Id
// 	PublicKey   [btcec.PubKeyBytesLenCompressed]byte // generator的公钥
// 	Timestamp   int64                                // generator出块的时间
// 	Height      int32                                // Satsnet出块的高度
// 	Hash        []byte                               // 出块的Satsnet块Hash
// 	Token       string                               // 出块的Token
// }

func (nb *DataMinerNewBlock) Encode(w io.Writer) error {
	// Encode New epoch data.
	err := utils.WriteElements(w,
		nb.GeneratorId,
		nb.Timestamp,
		nb.SatsnetHeight,
		nb.Hash,
		nb.Token)
	if err != nil {
		return err
	}
	return nil
}

func (nb *DataMinerNewBlock) Decode(r io.Reader) error {
	// Encode New epoch data.
	err := utils.ReadElements(r,
		&nb.GeneratorId,
		&nb.Timestamp,
		&nb.SatsnetHeight,
		&nb.Hash,
		&nb.Token)
	if err != nil {
		return err
	}

	return nil
}
