package generator

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
)

type MinerInterface interface {
	// OnTimeGenerateBlock is invoke when time to generate block.
	OnTimeGenerateBlock() (*chainhash.Hash, int32, error)
}

const (
	// If the generator id is NoGeneratorId, it means the generator is not saved in the peer
	NoGeneratorId = ""
	MinerInterval = 12 * time.Second
)

type Generator struct {
	GeneratorId   string // The validator id of generator
	Height        int32  // The block height for the generator
	Timestamp     int64  // The time of generator created
	Token         string // The token for the generator, it signed by generate
	Validatorinfo *validatorinfo.ValidatorInfo
	MinerTime     time.Time

	LocalMiner MinerInterface
}

const (
	HandOverTypeByEpochOrder = 0
	HandOverTypeByVote       = 1
)

type GeneratorHandOver struct {
	ValidatorId  string // The current validator id (current generator, or voter)
	HandOverType int32  // HandOverType: 0: HandOver by current generator with Epoch member Order, 1: Vote by Epoch member
	Timestamp    int64  // The time of generator hand over
	Token        string // The token for generator handover, it sign by current generator (HandOver), if the type is vote, it is signed by voter
	GeneratorId  string // The next validator id (next generator)
	Height       int32  // The next block height
}

func NewGenerator(validatorInfo *validatorinfo.ValidatorInfo, height int32, timestamp int64, token string) *Generator {
	if timestamp == 0 {
		timestamp = time.Now().Unix()
	}
	generator := &Generator{
		GeneratorId:   validatorInfo.ValidatorId,
		Height:        height,
		Timestamp:     timestamp,
		Token:         token,
		Validatorinfo: validatorInfo,
	}

	return generator
}

func (g *Generator) GetTokenData() []byte {

	// Token Data format: "satsnet:height:validatorid:timestamp"
	tokenData := fmt.Sprintf("satsnet:generate:%d:%d:%d", g.Height, g.GeneratorId, g.Timestamp)
	tokenSource := sha256.Sum256([]byte(tokenData))
	return tokenSource[:]
}

func (g *Generator) SetToken(token string) {
	g.Token = token
}

func (g *Generator) VerifyToken(pubKey string) bool {
	signatureBytes, err := base64.StdEncoding.DecodeString(g.Token)
	if err != nil {
		utils.Log.Tracef("[Generator]VerifyToken: Invalid generator token, ignore it.")
		return false
	}

	tokenData := g.GetTokenData()

	pk, err := hex.DecodeString(pubKey)
	if err != nil {
		return false
	}

	publicKey, err := secp256k1.ParsePubKey(pk)
	if err != nil {
		utils.Log.Tracef("[Generator]VerifyToken: Invalid public key.")
		return false
	}

	// 解析签名
	// signature, err := btcec.ParseDERSignature(signatureBytes)
	signature, err := ecdsa.ParseDERSignature(signatureBytes)
	if err != nil {
		utils.Log.Tracef("[Generator]VerifyToken:Failed to parse signature: %v", err)
		return false
	}

	// 使用公钥验证签名
	valid := signature.Verify(tokenData, publicKey)
	if valid {
		utils.Log.Tracef("[Generator]VerifyToken:Signature is valid.")
		return true
	} else {
		utils.Log.Tracef("[Generator]VerifyToken:Signature is invalid.")
		return false
	}

}

func (g *Generator) SetHandOverTime(handOverTime time.Time) error {
	utils.Log.Debugf("[Generator]SetHandOverTime ...")
	now := time.Now()
	minerTime := handOverTime.Add(MinerInterval)
	if minerTime.After(now) == false {
		//		return fmt.Errorf("miner time is before now")
		minerTime = now.Add(MinerInterval)
	}
	g.MinerTime = minerTime
	go g.minerHandler()
	return nil
}
func (g *Generator) ContinueNextSlot() error {
	utils.Log.Debugf("[Generator]ContinueNextSlot ...")

	now := time.Now()
	newMinerTime := g.MinerTime.Add(MinerInterval)
	if newMinerTime.After(now) == false {
		return fmt.Errorf("new miner time is before now")
	}
	g.MinerTime = newMinerTime
	go g.minerHandler()
	return nil
}

func (g *Generator) SetLocalMiner(localMiner MinerInterface) {
	g.LocalMiner = localMiner
}

func (g *Generator) MinerNewBlock() {
	utils.Log.Tracef("##################################################################")
	utils.Log.Debugf("[Generator]MinerNewBlock...")
	utils.Log.Debugf("[Generator]Miner time: %v", time.Now().Format("2006-01-02 15:04:05"))
	utils.Log.Debugf("[Generator]Miner height: %d", g.Height)
	if g.LocalMiner != nil {
		utils.Log.Debugf("[Generator]Call localMiner to generate a new block...")
		g.LocalMiner.OnTimeGenerateBlock()
	}
	utils.Log.Tracef("##################################################################")
}


func (g *Generator) minerHandler() {
	utils.Log.Debugf("[Generator]minerHandler start...")

	exitMinerHandler := make(chan struct{})
	utils.Log.Tracef("[Generator]Set Miner <height:%d> Timer at %s ", g.Height, g.MinerTime.Format("2006-01-02 15:04:05"))

	minerDuration := time.Until(g.MinerTime)
	time.AfterFunc(minerDuration, func() {
		g.MinerNewBlock()
		exitMinerHandler <- struct{}{}
	})

	// 这里阻塞主 goroutine 等待任务执行（可根据需要改为其他逻辑）
	select {
	case <-exitMinerHandler:
		utils.Log.Tracef("[Generator]minerHandler done .")
		return
	}
}

func (gho *GeneratorHandOver) GetTokenData() []byte {

	// Next Generator Token Data format: "satsnet:handovertype:validatorid:height:generatorid:timestamp"
	tokenData := fmt.Sprintf("satsnet:generatorhandover:%d:%d:%d:%d:%d", gho.HandOverType, gho.ValidatorId, gho.Height, gho.GeneratorId, gho.Timestamp)
	tokenSource := sha256.Sum256([]byte(tokenData))
	return tokenSource[:]
}

func (gho *GeneratorHandOver) VerifyToken(pubKey []byte) bool {
	signatureBytes, err := base64.StdEncoding.DecodeString(gho.Token)
	if err != nil {
		utils.Log.Tracef("[GeneratorHandOver]VerifyToken: Invalid generator token, ignore it.")
		return false
	}

	tokenData := gho.GetTokenData()

	publicKey, err := secp256k1.ParsePubKey(pubKey[:])
	if err != nil {
		utils.Log.Tracef("[GeneratorHandOver]VerifyToken: Invalid public key.")
		return false
	}

	// 解析签名
	// signature, err := btcec.ParseDERSignature(signatureBytes)
	signature, err := ecdsa.ParseDERSignature(signatureBytes)
	if err != nil {
		utils.Log.Tracef("[GeneratorHandOver]VerifyToken:Failed to parse signature: %v", err)
		return false
	}

	// 使用公钥验证签名
	valid := signature.Verify(tokenData, publicKey)
	if valid {
		utils.Log.Tracef("[GeneratorHandOver]VerifyToken:Signature is valid.")
		return true
	} else {
		utils.Log.Tracef("[GeneratorHandOver]VerifyToken:Signature is invalid.")
		return false
	}

}

type MinerNewBlock struct {
	GeneratorId string                               // The validator id of generator
	Height      int32                                // The block height for the generator
	MinerTime   int64                                // The time of generator created
	Hash        *chainhash.Hash                      // Block hash
	Token       string                               // The token for the generator, it signed by generate
}

func (m *MinerNewBlock) GetTokenData() []byte {

	// Token Data format: "satsnet:height:validatorid:timestamp"
	tokenData := fmt.Sprintf("satsnet:miner:%d:%d:%d:%s", m.Height, m.GeneratorId, m.MinerTime, m.Hash)
	tokenSource := sha256.Sum256([]byte(tokenData))
	return tokenSource[:]
}

func (g *MinerNewBlock) VerifyToken(pubKey []byte) bool {
	signatureBytes, err := base64.StdEncoding.DecodeString(g.Token)
	if err != nil {
		utils.Log.Tracef("[MinerNewBlock]VerifyToken: Invalid generator token, ignore it.")
		return false
	}

	tokenData := g.GetTokenData()

	publicKey, err := secp256k1.ParsePubKey(pubKey[:])
	if err != nil {
		utils.Log.Tracef("[MinerNewBlock]VerifyToken: Invalid public key.")
		return false
	}

	// 解析签名
	// signature, err := btcec.ParseDERSignature(signatureBytes)
	signature, err := ecdsa.ParseDERSignature(signatureBytes)
	if err != nil {
		utils.Log.Tracef("[MinerNewBlock]VerifyToken:Failed to parse signature: %v", err)
		return false
	}

	// 使用公钥验证签名
	valid := signature.Verify(tokenData, publicKey)
	if valid {
		utils.Log.Tracef("[MinerNewBlock]VerifyToken:Signature is valid.")
		return true
	} else {
		utils.Log.Tracef("[MinerNewBlock]VerifyToken:Signature is invalid.")
		return false
	}

}
