package epoch

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
)

const (
	Pos_Epoch_NotStarted = -1
)

type EpochItem struct {
	ValidatorId string // == PublicKey
	Host        string
	Index       uint32 // 第一次生成是确定的，后续不会改变， 当Epoch的Validators有改变（下线）时，保证EpochItem的Index不发生改变
}

type Epoch struct {
	EpochIndex      int64                // Epoch Index, start from 0
	CreateHeight    int32                // 创建Epoch时当前Block的高度
	CreateTime      time.Time            // 当前Epoch的创建时间
	ItemList        []*EpochItem         // 当前Epoch包含的验证者列表，（已排序）， 在一个Epoch结束前不会改变
	Generator       *generator.Generator // 当前Generator
	CurGeneratorPos int32                // 当前Generator在ItemList中的位置
	//	CurrentHeight uint64             // 当前Block chain的高度
	LastChangeTime time.Time       // 最后一次改变的时间, 用于判断是否需要更新， Epoch的改变包括创建， 转正，generator流转，成员删除
	VCBlockHeight  int64           // 记录当前epoch改变的的VCblock高度
	VCBlockHash    *chainhash.Hash // 记录当前epoch改变的的VCblock hash
}

type NewEpochVote struct {
	NewEpoch  *Epoch
	Reason    uint32
	VotorId   string // pubkey
	Token     string
	Hash      *chainhash.Hash // epoch block hash
}

func NewEpoch() *Epoch {
	return &Epoch{
		ItemList:        make([]*EpochItem, 0),
		CreateTime:      time.Now(),
		CreateHeight:    0,
		Generator:       nil,
		CurGeneratorPos: Pos_Epoch_NotStarted, // -1 表示没有还没有Generator
	}
}

func (e *Epoch) AddValidatorToEpoch(validatorInfo *validatorinfo.ValidatorInfo) error {
	// Insert validatorInfo into e.ValidatorList
	if e.IsValidEpochValidator(validatorInfo) == false {
		err := fmt.Errorf("The validator is not valid epoch validator, validator: %v", validatorInfo)
		return err
	}

	for i := 0; i < len(e.ItemList); i++ {
		if e.ItemList[i].ValidatorId == validatorInfo.ValidatorId {
			// The validator is already in e.ValidatorList
			err := fmt.Errorf("The validator (%s:%d) has been existed", validatorInfo.Host, validatorInfo.ValidatorId)
			return err
		}
		if e.ItemList[i].ValidatorId > validatorInfo.ValidatorId {
			// Insert validatorInfo before e.ValidatorList[i]
			e.ItemList = append(e.ItemList, nil)
			copy(e.ItemList[i+1:], e.ItemList[i:])
			e.ItemList[i] = &EpochItem{
				ValidatorId: validatorInfo.ValidatorId,
				Host:        validatorInfo.Host,
			}
			return nil
		}
	}

	// Append to tail
	epochItem := &EpochItem{
		ValidatorId: validatorInfo.ValidatorId,
		Host:        validatorInfo.Host,
	}
	e.ItemList = append(e.ItemList, epochItem)

	utils.Log.Tracef("Add validator (%s:%d) to epoch", validatorInfo.Host, validatorInfo.ValidatorId)
	return nil
}
func (e *Epoch) IsExist(validatorId string) bool {
	for _, validatorInfo := range e.ItemList {
		if validatorInfo.ValidatorId == validatorId {
			// The validator is already in e.ValidatorList
			return true
		}
	}
	return false
}

func (e *Epoch) IsValidEpochValidator(validatorInfo *validatorinfo.ValidatorInfo) bool {
	if validatorInfo.ValidatorId == "" {
		// Should be a valid validator id
		return false
	}

	if validatorInfo.CreateTime.IsZero() {
		// Should be a valid create time
		return false
	}

	if validatorInfo.Host == "" {
		// Should be a valid host
		return false
	}

	return true
}

func (e *Epoch) DelEpochMember(validatorId string) error {
	for i := 0; i < len(e.ItemList); i++ {
		if e.ItemList[i].ValidatorId == validatorId {
			// The validator is found, remove it
			e.ItemList = append(e.ItemList[:i], e.ItemList[i+1:]...)
			if int32(i) == e.CurGeneratorPos {
				// Current generator is removed, reset it
				e.Generator = nil
			} else if int32(i) < e.CurGeneratorPos {
				e.CurGeneratorPos--
			}
			utils.Log.Tracef("Remove validator (%d) from epoch", validatorId)
			return nil
		}
	}
	return fmt.Errorf("The validator (%d) is not in epoch", validatorId)
}

func (e *Epoch) GetValidatorList() []*EpochItem {
	return e.ItemList
}

func (e *Epoch) GetGenerator() *generator.Generator {
	return e.Generator
}

func (e *Epoch) GetCurGeneratorPos() int32 {
	return e.CurGeneratorPos
}

func (e *Epoch) GetMemberCount() int32 {
	return int32(len(e.ItemList))
}

func (e *Epoch) GetMemberValidatorId(pos int32) string {
	if pos < 0 || pos >= int32(len(e.ItemList)) {
		return ""
	}
	return e.ItemList[pos].ValidatorId
}

func (e *Epoch) GetCreateTime() time.Time {
	return e.CreateTime
}

func (e *Epoch) UpdateGenerator(generator *generator.Generator) {
	e.Generator = generator
}
func (e *Epoch) ToNextGenerator(generator *generator.Generator) error {
	var nextPos int32
	if e.CurGeneratorPos == Pos_Epoch_NotStarted {
		nextPos = 0
	} else {
		nextPos = e.CurGeneratorPos + 1
	}
	if nextPos >= int32(len(e.ItemList)) {
		err := fmt.Errorf("Exceed epoch item list length: NextPos= %d, Total = %d ", nextPos, len(e.ItemList))
		return err
	}
	epochItem := e.ItemList[nextPos]
	if epochItem == nil {
		err := fmt.Errorf("Invalid epoch item in NextPos= %d ", e.CurGeneratorPos)
		return err
	}
	if epochItem.ValidatorId != generator.GeneratorId {
		err := fmt.Errorf("The generator is not valid: NextPos EpochItem ID = %d, Generator ID = %d ", epochItem.ValidatorId, generator.GeneratorId)
		return err
	}

	e.CurGeneratorPos = nextPos
	e.Generator = generator

	return nil
}
func (e *Epoch) UpdateCurrentGenerator(generator *generator.Generator) error {
	if e.CurGeneratorPos == Pos_Epoch_NotStarted {
		e.CurGeneratorPos = 0
	}
	posGenerator := e.CurGeneratorPos
	if posGenerator >= int32(len(e.ItemList)) {
		err := fmt.Errorf("Exceed epoch item list length: NextPos= %d, Total = %d ", posGenerator, len(e.ItemList))
		return err
	}
	epochItem := e.ItemList[posGenerator]
	if epochItem == nil {
		err := fmt.Errorf("Invalid epoch item in NextPos= %d ", e.CurGeneratorPos)
		return err
	}
	if epochItem.ValidatorId != generator.GeneratorId {
		err := fmt.Errorf("The generator is not valid: NextPos EpochItem ID = %d, Generator ID = %d ", epochItem.ValidatorId, generator.GeneratorId)
		return err
	}

	e.Generator = generator

	return nil
}

func (e *Epoch) IsLastGenerator() bool {
	if e.CurGeneratorPos == int32(len(e.ItemList)-1) {
		return true
	}
	return false
}

func (e *Epoch) VoteGenerator() *EpochItem {
	if len(e.ItemList) == 0 {
		return nil
	}

	// The Validator list is lined, Vote new generator as first in list
	return e.ItemList[0]
}

func (e *Epoch) GetNextValidatorByEpochOrder() *EpochItem {
	var nextPos int32
	if e.CurGeneratorPos == Pos_Epoch_NotStarted {
		nextPos = 0
	} else {
		nextPos = e.CurGeneratorPos + 1
	}

	if nextPos < 0 || nextPos >= int32(len(e.ItemList)) {
		return nil
	}

	return e.ItemList[nextPos]
}

func (e *Epoch) GetNextValidator() *EpochItem {

	var nextPos int32
	if e.CurGeneratorPos == Pos_Epoch_NotStarted {
		nextPos = 0
	} else {
		nextPos = e.CurGeneratorPos + 1
	}

	if nextPos < 0 || nextPos >= int32(len(e.ItemList)) {
		return nil
	}
	return e.ItemList[nextPos]
}

func (e *Epoch) GetValidatorPos(validatorId string) int32 {
	for i := 0; i < len(e.ItemList); i++ {
		if e.ItemList[i].ValidatorId == validatorId {
			// The validator is found, remove it
			return int32(i)
		}
	}

	return -1
}
func (e *Epoch) GetLastEpochMemberId() string {
	if len(e.ItemList) == 0 {
		return ""
	}
	return e.ItemList[len(e.ItemList)-1].ValidatorId
}

func (nev *NewEpochVote) GetTokenData() []byte {

	// Next epoch Token Data format: "satsnet:delepochmem:validatorid:DelValidatorId:DelCode:EpochIndex:timestamp"
	//tokenData := fmt.Sprintf("satsnet:newepochvote:%d:%d:%d:%d:%d:%d", he.ValidatorId, he.DelValidatorId, he.DelCode, he.EpochIndex, he.Result, he.Timestamp)
	var bw bytes.Buffer

	err := nev.Encode(&bw)
	if err != nil {
		utils.Log.Errorf("Failed to encode new epoch vote: %v", err)
		return nil
	}

	tokenData := bw.Bytes()

	tokenSource := sha256.Sum256([]byte(tokenData))

	return tokenSource[:]
}
func (nev *NewEpochVote) Encode(w io.Writer) error {
	// new epoch vote Token Data format: "satsnet:newepochvote:"
	// VotorId, Reason
	// EpochIndex
	// CreateTime
	// ItemList

	tokenTitle := "satsnet:newepochvote:"
	err := utils.WriteElements(w,
		tokenTitle,
		nev.VotorId,
		nev.Reason,
		nev.NewEpoch.EpochIndex,
		nev.NewEpoch.CreateTime.Unix())
	if err != nil {
		return err
	}
	for _, item := range nev.NewEpoch.ItemList {
		err := utils.WriteElements(w,
			item.ValidatorId)
		if err != nil {
			return err
		}
	}

	return nil
}

func (nev *NewEpochVote) VerifyToken(pubKey []byte) bool {
	signatureBytes, err := base64.StdEncoding.DecodeString(nev.Token)
	if err != nil {
		utils.Log.Tracef("[NewEpochVote]VerifyToken: Invalid generator token, ignore it.")
		return false
	}

	tokenData := nev.GetTokenData()

	publicKey, err := secp256k1.ParsePubKey(pubKey[:])
	if err != nil {
		utils.Log.Tracef("[NewEpochVote]VerifyToken: Invalid public key.")
		return false
	}
	// 解析签名
	signature, err := ecdsa.ParseDERSignature(signatureBytes)
	if err != nil {
		utils.Log.Tracef("Failed to parse signature: %v", err)
		return false
	}

	// 使用公钥验证签名
	valid := signature.Verify(tokenData, publicKey)
	if valid {
		utils.Log.Tracef("[NewEpochVote]VerifyToken:Signature is valid.")
		return true
	} else {
		utils.Log.Tracef("[NewEpochVote]VerifyToken:Signature is invalid.")
		return false
	}

}
