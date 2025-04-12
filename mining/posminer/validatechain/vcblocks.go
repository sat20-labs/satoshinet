package validatechain

import (
	"bytes"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	// 块数据类型
	DataType_NewEpoch          = 1
	DataType_UpdateEpoch       = 2
	DataType_GeneratorHandOver = 3
	DataType_MinerNewBlock     = 4
	DataType_DelEpochMember    = 5

	// Epoch 投票原因
	NewEpochReason_EpochCreate   = 1 // 这个用于第一次创建Epoch
	NewEpochReason_EpochHandOver = 2 // 这个用于Epoch轮换， 即上一个Epoch已经结束，投票产生下一个Epoch
	NewEpochReason_EpochStopped  = 3 // 这个用于上一个Epoch长时间没有任何更新， 需要投票产生新的Epoch来重新挖矿
	NewEpochReason_BootStrapNode = 4 // 这个用于有引导节点单独创建Epoch

	// Update Epoch Reason
	UpdateEpochReason_EpochHandOver     = 11 // Epoch 转正
	UpdateEpochReason_GeneratorHandOver = 12 // Generator 轮换
	UpdateEpochReason_MemberRemoved     = 13 // 成员删除
)

type VCBlockHeader struct {
	Hash       chainhash.Hash // 块Hash
	Height     int64          // 块高度， 高度从0开始
	PrevHash   chainhash.Hash // 前序块的Hash
	CreateTime int64          // 块创建的时间
	Version    uint32         // 块版本
	DataType   uint32         // 块数据类型-- Epoch创建， Epoch更新（Epoch转正，成员删除，generator更新）,generator交接，miner出块
}

type EpochVoteItem struct {
	ValidatorId uint64         // 用户ID
	Hash        chainhash.Hash // 投票的块Hash epBlockHash
}

type EpochDelConfirmItem struct {
	ValidatorId uint64                               // 用户ID
	PublicKey   [btcec.PubKeyBytesLenCompressed]byte // 用户公钥
	Result      uint32                               // 用户确认的结果
	Token       string                               // 投票的Token
}

// Epoch创建：
type DataNewEpoch struct {
	CreatorId     uint64                               // 创建者ID
	PublicKey     [btcec.PubKeyBytesLenCompressed]byte // 创建者公钥
	EpochIndex    int64                                // Epoch Index
	CreateTime    int64                                // 创建时间
	Reason        uint32                               // 发起原因（Epoch创立，Epoch轮换，当前Epoch停摆）
	EpochItemList []epoch.EpochItem                    // 该Epoch最终的ItemList
	EpochVoteList []EpochVoteItem                      // 所有用户的投票数据
}

// Epoch删除成员：
type DataEpochDelMember struct {
	RequestId           uint64                               // 请求者ID
	PublicKey           [btcec.PubKeyBytesLenCompressed]byte // 请求者公钥
	EpochIndex          int64                                // Epoch Index
	CreateTime          int64                                // 请求时间
	Reason              uint32                               // Epoch 删除只有一个原因， 成员离线
	EpochItemList       []epoch.EpochItem                    // 该Epoch最终的ItemList
	EpochDelConfirmList []EpochDelConfirmItem                // 所有用户的投票数据
}

// Epoch更新：
type DataUpdateEpoch struct {
	UpdatedId     uint64                               // 更新者ID
	PublicKey     [btcec.PubKeyBytesLenCompressed]byte // 更新者公钥
	EpochIndex    int64                                // Epoch Index
	CreateTime    int64                                // 创建时间
	Reason        uint32                               // 更新原因（Epoch转正，成员删除，generator更新）
	EpochItemList []epoch.EpochItem                    // 该Epoch最终的ItemList
	GeneratorPos  uint32                               // Generator位置
	Generator     *generator.Generator                 // Generator信息
}

// Generator流转
type DataGeneratorHandOver struct {
	ValidatorId     uint64                               // 进行Generator流转的validator Id
	PublicKey       [btcec.PubKeyBytesLenCompressed]byte // generator的公钥
	HandOverType    int32                                // HandOverType: 0: HandOver by current generator with Epoch member Order, 1: Vote by Epoch member
	Timestamp       int64                                //  generator流转的时间
	NextGeneratorId uint64                               // 下一个Generator的ID
	NextHeight      int64                                // 下一个块的高度
	Token           string                               // 流转的Token
}

// Generator出块
type DataMinerNewBlock struct {
	GeneratorId   uint64                               // 当前Generator的Id
	PublicKey     [btcec.PubKeyBytesLenCompressed]byte // generator的公钥
	Timestamp     int64                                // generator出块的时间
	SatsnetHeight int32                                // Satsnet出块的高度
	Hash          chainhash.Hash                       // 出块的Satsnet块Hash
	Token         string                               // 出块的Token
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
	case *DataNewEpoch:
		err = vcd.Encode(&bw)
	case *DataUpdateEpoch:
		err = vcd.Encode(&bw)
	case *DataGeneratorHandOver:
		err = vcd.Encode(&bw)
	case *DataMinerNewBlock:
		err = vcd.Encode(&bw)
	case *DataEpochDelMember:
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
	case DataType_NewEpoch:
		newEpoch := &DataNewEpoch{}
		err = newEpoch.Decode(br)
		if err != nil {
			return err
		}
		vcb.Data = newEpoch

	case DataType_UpdateEpoch:
		updateEpoch := &DataUpdateEpoch{}
		err = updateEpoch.Decode(br)
		if err != nil {
			return err
		}
		vcb.Data = updateEpoch

	case DataType_GeneratorHandOver:
		generatorHandOver := &DataGeneratorHandOver{}
		err = generatorHandOver.Decode(br)
		if err != nil {
			return err
		}
		vcb.Data = generatorHandOver

	case DataType_MinerNewBlock:
		newBlock := &DataMinerNewBlock{}
		err = newBlock.Decode(br)
		if err != nil {
			return err
		}
		vcb.Data = newBlock
	case DataType_DelEpochMember:
		delMember := &DataEpochDelMember{}
		err = delMember.Decode(br)
		if err != nil {
			return err
		}
		vcb.Data = delMember
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

//	 Epoch创建：
//
//		type DataNewEpoch struct {
//			CreatorId     uint64                               // 创建者ID
//			PublicKey     [btcec.PubKeyBytesLenCompressed]byte // 创建者公钥
//			EpochIndex    uint32                               // Epoch Index
//			CreateTime    int64                                // 创建时间
//			Reason        uint32                               // 发起原因（Epoch创立，Epoch轮换，当前Epoch停摆）
//			EpochItemList []epoch.EpochItem                    // 该Epoch最终的ItemList
//			EpochVoteList []EpochVoteItem                      // 所有用户的投票数据
//		}
func (ne *DataNewEpoch) Encode(w io.Writer) error {
	// Encode New epoch data.
	err := utils.WriteElements(w,
		ne.CreatorId,
		ne.PublicKey,
		ne.EpochIndex,
		ne.CreateTime,
		ne.Reason)
	if err != nil {
		return err
	}

	// Encode epoch item list.
	err = utils.WriteVarInt(w, uint64(len(ne.EpochItemList)))
	if err != nil {
		return err
	}
	for _, item := range ne.EpochItemList {
		err := utils.WriteElements(w,
			item.ValidatorId,
			item.PublicKey)
		if err != nil {
			return err
		}
	}

	// Encode epoch vote list.
	err = utils.WriteVarInt(w, uint64(len(ne.EpochVoteList)))
	if err != nil {
		return err
	}
	for _, vote := range ne.EpochVoteList {
		err := utils.WriteElements(w,
			vote.ValidatorId,
			vote.Hash)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ne *DataNewEpoch) Decode(r io.Reader) error {
	// Encode New epoch data.
	err := utils.ReadElements(r,
		&ne.CreatorId,
		&ne.PublicKey,
		&ne.EpochIndex,
		&ne.CreateTime,
		&ne.Reason)
	if err != nil {
		return err
	}

	// Decode epoch item list.
	count, err := utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	ne.EpochItemList = make([]epoch.EpochItem, 0)
	for i := 0; i < int(count); i++ {
		item := epoch.EpochItem{}
		err := utils.ReadElements(r,
			&item.ValidatorId,
			&item.PublicKey)
		if err != nil {
			return err
		}
		ne.EpochItemList = append(ne.EpochItemList, item)
	}

	// Decode epoch vote list.
	count, err = utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	for i := 0; i < int(count); i++ {
		vote := EpochVoteItem{}
		err := utils.ReadElements(r,
			&vote.ValidatorId,
			&vote.Hash)
		if err != nil {
			return err
		}
		ne.EpochVoteList = append(ne.EpochVoteList, vote)
	}

	return nil
}

// Epoch更新：
// type DataUpdateEpoch struct {
// 	UpdatedId     uint64                               // 更新者ID
// 	PublicKey     [btcec.PubKeyBytesLenCompressed]byte // 更新者公钥
// 	EpochIndex    uint32                               // Epoch Index
// 	CreateTime    int64                                // 创建时间
// 	Reason        uint32                               // 更新原因（Epoch转正，成员删除，generator更新）
// 	EpochItemList []epoch.EpochItem                    // 该Epoch最终的ItemList
// 	Generator     *generator.Generator                 // Generator信息
// }

func (ue *DataUpdateEpoch) Encode(w io.Writer) error {
	// Encode update epoch data.
	err := utils.WriteElements(w,
		ue.UpdatedId,
		ue.PublicKey,
		ue.EpochIndex,
		ue.CreateTime,
		ue.Reason)
	if err != nil {
		return err
	}

	// Encode epoch item list.
	err = utils.WriteVarInt(w, uint64(len(ue.EpochItemList)))
	if err != nil {
		return err
	}
	for _, item := range ue.EpochItemList {
		err := utils.WriteElements(w,
			item.ValidatorId,
			item.PublicKey)
		if err != nil {
			return err
		}
	}

	// Encode generator.
	if ue.Generator != nil {
		exist := uint64(1) // Exist generator
		err = utils.WriteVarInt(w, exist)
		if err != nil {
			return err
		}

		// Encode generator data
		// GeneratorId：当前Generator的Id
		// Height：当前要出快的高度
		// Timestamp：  当选generator的时间
		// Token：generator的token
		// GeneratorPos：当前Generator在Epoch中的位置
		err := utils.WriteElements(w,
			ue.Generator.GeneratorId,
			ue.Generator.Height,
			ue.Generator.Timestamp,
			ue.Generator.Token,
			ue.GeneratorPos)
		if err != nil {
			return err
		}
	} else {
		exist := uint64(0) // Not exist generator.
		err = utils.WriteVarInt(w, exist)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ue *DataUpdateEpoch) Decode(r io.Reader) error {
	// Encode New epoch data.
	err := utils.ReadElements(r,
		&ue.UpdatedId,
		&ue.PublicKey,
		&ue.EpochIndex,
		&ue.CreateTime,
		&ue.Reason)
	if err != nil {
		return err
	}

	// Decode epoch item list.
	count, err := utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	ue.EpochItemList = make([]epoch.EpochItem, 0)
	for i := 0; i < int(count); i++ {
		item := epoch.EpochItem{}
		err := utils.ReadElements(r,
			&item.ValidatorId,
			&item.PublicKey)
		if err != nil {
			return err
		}
		ue.EpochItemList = append(ue.EpochItemList, item)
	}

	// Decode generator.
	exist, err := utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	if exist == 0 {
		// No generator.
		return nil
	}

	// Decode generator data
	ue.Generator = &generator.Generator{}
	err = utils.ReadElements(r,
		&ue.Generator.GeneratorId,
		&ue.Generator.Height,
		&ue.Generator.Timestamp,
		&ue.Generator.Token,
		&ue.GeneratorPos)
	if err != nil {
		return err
	}

	return nil
}

// Epoch删除一个成员：
// type DataEpochDelMember struct {
// 	RequestId           uint64                               // 创建者ID
// 	PublicKey           [btcec.PubKeyBytesLenCompressed]byte // 创建者公钥
// 	EpochIndex          int64                                // Epoch Index
// 	CreateTime          int64                                // 创建时间
// 	Reason              uint32                               // Epoch 删除只有一个原因， 成员离线
// 	EpochItemList       []epoch.EpochItem                    // 该Epoch最终的ItemList
// 	EpochDelConfirmList []EpochDelConfirmItem                // 所有用户的投票数据
// }

func (edm *DataEpochDelMember) Encode(w io.Writer) error {
	// Encode update epoch data.
	err := utils.WriteElements(w,
		edm.RequestId,
		edm.PublicKey,
		edm.EpochIndex,
		edm.CreateTime,
		edm.Reason)
	if err != nil {
		return err
	}

	// Encode epoch item list.
	err = utils.WriteVarInt(w, uint64(len(edm.EpochItemList)))
	if err != nil {
		return err
	}
	for _, item := range edm.EpochItemList {
		err := utils.WriteElements(w,
			item.ValidatorId,
			item.PublicKey)
		if err != nil {
			return err
		}
	}

	// Encode Del confirm list.
	err = utils.WriteVarInt(w, uint64(len(edm.EpochDelConfirmList)))
	if err != nil {
		return err
	}
	for _, item := range edm.EpochDelConfirmList {
		err := utils.WriteElements(w,
			item.ValidatorId,
			item.PublicKey,
			item.Result,
			item.Token)
		if err != nil {
			return err
		}
	}
	return nil
}

func (edm *DataEpochDelMember) Decode(r io.Reader) error {
	// Encode New epoch data.
	err := utils.ReadElements(r,
		&edm.RequestId,
		&edm.PublicKey,
		&edm.EpochIndex,
		&edm.CreateTime,
		&edm.Reason)
	if err != nil {
		return err
	}

	// Decode epoch item list.
	count, err := utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	edm.EpochItemList = make([]epoch.EpochItem, 0)
	for i := 0; i < int(count); i++ {
		item := epoch.EpochItem{}
		err := utils.ReadElements(r,
			&item.ValidatorId,
			&item.PublicKey)
		if err != nil {
			return err
		}
		edm.EpochItemList = append(edm.EpochItemList, item)
	}

	// Decode Del confirm list..
	count, err = utils.ReadVarInt(r)
	if err != nil {
		return err
	}
	edm.EpochDelConfirmList = make([]EpochDelConfirmItem, 0)
	for i := 0; i < int(count); i++ {
		item := EpochDelConfirmItem{}
		err := utils.ReadElements(r,
			&item.ValidatorId,
			&item.PublicKey,
			&item.Result,
			&item.Token)
		if err != nil {
			return err
		}
		edm.EpochDelConfirmList = append(edm.EpochDelConfirmList, item)
	}

	return nil
}

// Generator流转
//
//	type DataGeneratorHandOver struct {
//
// ValidatorId     uint64                               // 进行Generator流转的validator Id
// PublicKey       [btcec.PubKeyBytesLenCompressed]byte // generator的公钥
// HandOverType    int32                                // HandOverType: 0: HandOver by current generator with Epoch member Order, 1: Vote by Epoch member
// Timestamp       int64                                // generator流转的时间
// NextGeneratorId uint64                               // 下一个Generator的ID
// NextHeight      int32                                // 下一个块的高度
// Token           string                               // 流转的Token
//
//	}
func (gh *DataGeneratorHandOver) Encode(w io.Writer) error {
	// Encode New epoch data.
	err := utils.WriteElements(w,
		gh.ValidatorId,
		gh.PublicKey,
		gh.HandOverType,
		gh.Timestamp,
		gh.NextGeneratorId,
		gh.NextHeight,
		gh.Token)
	if err != nil {
		return err
	}
	return nil
}

func (gh *DataGeneratorHandOver) Decode(r io.Reader) error {
	// Encode New epoch data.
	err := utils.ReadElements(r,
		&gh.ValidatorId,
		&gh.PublicKey,
		&gh.HandOverType,
		&gh.Timestamp,
		&gh.NextGeneratorId,
		&gh.NextHeight,
		&gh.Token)
	if err != nil {
		return err
	}

	return nil
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
		nb.PublicKey,
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
		&nb.PublicKey,
		&nb.Timestamp,
		&nb.SatsnetHeight,
		&nb.Hash,
		&nb.Token)
	if err != nil {
		return err
	}

	return nil
}
