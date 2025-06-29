package generator

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec"
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
	NoGeneratorId = uint64(0xffffffffffffffff)
	MinerInterval = 12 * time.Second
)

type Generator struct {
	GeneratorId   uint64 // The validator id of generator
	Height        int32  // The block height for the generator
	Timestamp     int64  // The time of generator created
	Token         string // The token for the generator, it signed by generate
	Validatorinfo *validatorinfo.ValidatorInfo
	MinerTime     time.Time

	minerHandlerOnce sync.Once
    minerHandlerMu   sync.Mutex
    minerHandlerStop chan struct{}
    minerHandlerWake chan struct{}

	LocalMiner MinerInterface
}

const (
	HandOverTypeByEpochOrder = 0
	HandOverTypeByVote       = 1
)

type GeneratorHandOver struct {
	ValidatorId  uint64 // The current validator id (current generator, or voter)
	HandOverType int32  // HandOverType: 0: HandOver by current generator with Epoch member Order, 1: Vote by Epoch member
	Timestamp    int64  // The time of generator hand over
	Token        string // The token for generator handover, it sign by current generator (HandOver), if the type is vote, it is signed by voter
	GeneratorId  uint64 // The next validator id (next generator)
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

func (g *Generator) VerifyToken(pubKey []byte) bool {
	signatureBytes, err := base64.StdEncoding.DecodeString(g.Token)
	if err != nil {
		utils.Log.Tracef("[Generator]VerifyToken: Invalid generator token, ignore it.")
		return false
	}

	tokenData := g.GetTokenData()

	publicKey, err := secp256k1.ParsePubKey(pubKey[:])
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
	utils.Log.Tracef("[Generator]SetHandOverTime ...")
	g.minerHandlerMu.Lock()
    now := time.Now()
    minerTime := handOverTime.Add(MinerInterval)
    if minerTime.Before(now) {
        minerTime = now.Add(MinerInterval)
    }
    g.MinerTime = minerTime
    g.minerHandlerMu.Unlock()
    g.StartMinerHandler()
    g.WakeMinerHandler()
	return nil
}
func (g *Generator) ContinueNextSlot() error {
	utils.Log.Tracef("[Generator]ContinueNextSlot ...")

	g.minerHandlerMu.Lock()
    now := time.Now()
    newMinerTime := g.MinerTime.Add(MinerInterval)
    if newMinerTime.Before(now) {
        g.minerHandlerMu.Unlock()
        return fmt.Errorf("new miner time is before now")
    }
    g.MinerTime = newMinerTime
    g.minerHandlerMu.Unlock()

    g.StartMinerHandler()
    g.WakeMinerHandler()
	return nil
}

func (g *Generator) SetLocalMiner(localMiner MinerInterface) {
	g.LocalMiner = localMiner
}

func (g *Generator) MinerNewBlock() {
	utils.Log.Tracef("##################################################################")
	utils.Log.Tracef("[Generator]MinerNewBlock...")
	utils.Log.Tracef("[Generator]Miner time: %v", time.Now().Format("2006-01-02 15:04:05"))
	utils.Log.Tracef("[Generator]Miner height: %d", g.Height)
	if g.LocalMiner != nil {
		utils.Log.Tracef("[Generator]Call localMiner to generate a new block...")
		g.LocalMiner.OnTimeGenerateBlock()
	}
	utils.Log.Tracef("##################################################################")
}


// 启动唯一的 minerHandler
func (g *Generator) StartMinerHandler() {
    g.minerHandlerOnce.Do(func() {
        g.minerHandlerStop = make(chan struct{})
        g.minerHandlerWake = make(chan struct{}, 1)
        go g.minerHandlerLoop()
    })
}


// 通知 minerHandler 更新时间
func (g *Generator) WakeMinerHandler() {
    select {
    case g.minerHandlerWake <- struct{}{}:
    default:
    }
}

func (g *Generator) minerHandlerLoop() {
    for {
        g.minerHandlerMu.Lock()
        nextTime := g.MinerTime
        g.minerHandlerMu.Unlock()

        wait := time.Until(nextTime)
        if wait < 0 {
            wait = 0
        }
        timer := time.NewTimer(wait)

        select {
        case <-timer.C:
            g.MinerNewBlock()
        case <-g.minerHandlerWake:
            timer.Stop()
            continue // 重新计算时间
        case <-g.minerHandlerStop:
            timer.Stop()
            return
        }
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
	GeneratorId uint64                               // The validator id of generator
	PublicKey   [btcec.PubKeyBytesLenCompressed]byte // The public key of generator
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
