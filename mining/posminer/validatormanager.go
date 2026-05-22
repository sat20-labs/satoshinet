package posminer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/stp"
	"github.com/sat20-labs/satoshinet/wire"
)

var (
	MinerInterval       int64 = envInt64("SATOSHINET_POS_MINER_INTERVAL", 9)      // 出块时间间隔12S，但留3S给出块节点与引导节点同步数据
	PreWarningInterval  int64 = envInt64("SATOSHINET_POS_PREWARNING_INTERVAL", 6) // 超时这么长时间还没出块
	CheckingInterval    int64 = envInt64("SATOSHINET_POS_CHECKING_INTERVAL", 3)   // 检查出块顺序
	UnexceptionInterval       = 2 * MinerInterval                                 // 超过2个Miner的时间， 就认为出块异常， bootstrap node 会重启Epoch, 目前直接出块以防出块卡死
)

func envInt64(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

type ValidatorManagerConfig struct {
	*Config
	PosMiner *POSMiner
}

type ValidatorManager struct {
	// ValidatorId uint64
	cfg               *ValidatorManagerConfig
	localValidatorId  string
	serverValidatorId string
	miningSeqMgr      *common.MiningSequenceMgr
	myself            *common.MiningInfo
	lastBlockTime     int64
	lastBlock         int
	quit              chan struct{}

	generatorTicker *time.Ticker
	pingTicker      *time.Ticker
}

func NewValidatorManager(cfg *ValidatorManagerConfig) *ValidatorManager {
	utils.Log.Tracef("New ValidatorManager")
	validatorMgr := &ValidatorManager{
		cfg:               cfg,
		localValidatorId:  cfg.MiningPubKey,
		serverValidatorId: cfg.ServerPubKey,
		quit:              make(chan struct{}),
	}

	validatorMgr.miningSeqMgr = shareindexer.ShareIndexer.GetSeqMgr()
	if validatorMgr.miningSeqMgr == nil {
		utils.Log.Errorf("miningSeqMgr is nil")
		return nil
	}
	validatorMgr.myself = validatorMgr.miningSeqMgr.GetMiningInfo(validatorMgr.localValidatorId)
	if validatorMgr.myself == nil {
		utils.Log.Errorf("GetMiningInfo %s failed", validatorMgr.localValidatorId)
		return nil
	}

	if validatorMgr.serverValidatorId == "" {
		if validatorMgr.myself.NodeType != indexer.NODE_TYPE_BOOTSTRAP {
			validatorMgr.serverValidatorId = indexer.GetBootstrapPubKey()
		}
	}
	if validatorMgr.myself.NodeType != indexer.NODE_TYPE_BOOTSTRAP {
		// check server node is the same
		if validatorMgr.myself.Father.PubKey != validatorMgr.serverValidatorId {
			utils.Log.Errorf("the server pubkey %s is not the same as %s", validatorMgr.serverValidatorId, validatorMgr.myself.Father.PubKey)
			return nil
		}
	}

	return validatorMgr
}

func (vm *ValidatorManager) Start() {
	utils.Log.Tracef("StartValidatorManager")

	// 测试用，set mining address
	//vm.miningSeqMgr.SetCurrentMiningAddr("tb1qqs42pk590l0qvz7jwa2xfeg0krcxjdg5fax2r0aavzd3u8yhfqksfe8rhm")

	go vm.generatorTimer()
	go vm.pingTimer()
}

func (vm *ValidatorManager) Stop() {
	utils.Log.Tracef("ValidatorManager Stop")

	close(vm.quit)
}

func (vm *ValidatorManager) GetCurrentBlockHeight() int32 {
	return vm.cfg.PosMiner.GetBlockHeight()
}

func (vm *ValidatorManager) ResetGeneratorMoniter() {
	utils.Log.Debugf("resetGeneratorMoniter...")
	if vm.generatorTicker == nil {
		// Not start monitor
		utils.Log.Tracef("GeneratorTicker is not start or stopped.")
		return
	}

	utils.Log.Debugf("local generator: Next check generator after %d seconds.", MinerInterval)
	vm.generatorTicker.Reset(time.Duration(MinerInterval) * time.Second)
}

// 一些有上下前后关系的miner相互ping，保持连接
func (vm *ValidatorManager) pingTimer() {
	vm.pingTicker = time.NewTicker(time.Duration(MinerInterval*2) * time.Second)

exit:
	for {
		select {
		case <-vm.pingTicker.C:
			vm.onPingTimer()
		case <-vm.quit:
			break exit
		}
	}

	vm.pingTicker.Stop()
	vm.pingTicker = nil

	utils.Log.Tracef("[ValidatorManager]pingTimer done.")
}

func (vm *ValidatorManager) onPingTimer() {
	// 向父节点，next节点，发送ping消息
	// 如果是core节点，向随机的其他core节点发送消息

}

// 在轮到自己出块时，reset interval
func (vm *ValidatorManager) generatorTimer() {
	vm.generatorTicker = time.NewTicker(time.Duration(CheckingInterval) * time.Second)
	vm.lastBlockTime = vm.cfg.PosMiner.GetBlockRecvTime()
	vm.lastBlock = int(vm.cfg.PosMiner.GetBlockHeight())

	utils.Log.Debugf("init lastBlockTime to %d", vm.lastBlockTime)

exit:
	for {
		select {
		case <-vm.generatorTicker.C:
			vm.checkAndGenerateNewBlock()
		case <-vm.quit:
			break exit
		}
	}

	vm.generatorTicker.Stop()
	vm.generatorTicker = nil

	utils.Log.Tracef("[ValidatorManager]generatorTimer done.")

}

func (vm *ValidatorManager) checkAndGenerateNewBlock() {
	utils.Log.Debugf("[ValidatorManager]checkAndGenerateNewBlock...")

	if !vm.cfg.IsCurrent() {
		utils.Log.Infof("[ValidatorManager] not reach the tip of block yet")
		return
	}

	now := time.Now().Unix()
	if vm.lastBlock != int(vm.cfg.PosMiner.GetBlockHeight()) {
		vm.lastBlockTime = vm.cfg.PosMiner.GetBlockRecvTime()
		vm.lastBlock = int(vm.cfg.PosMiner.GetBlockHeight())

		utils.Log.Debugf("block updated. height %d, lastBlockTime %d", vm.lastBlock, vm.lastBlockTime)
	}

	txSizeInMempool := vm.cfg.PosMiner.GetMempoolTxSize()
	if txSizeInMempool == 0 {
		utils.Log.Infof("[ValidatorManager] mempool is empty, current miner %s", vm.miningSeqMgr.GetCurrentMiningAddr())
		// 重置等待时间
		vm.lastBlockTime = now
		utils.Log.Debugf("reset lastBlockTime time to %d", vm.lastBlockTime)
		return
	}

	if now-vm.lastBlockTime < MinerInterval {
		// The miner time is not past, ignore
		utils.Log.Debugf("[ValidatorManager] not in time %d", now-vm.lastBlockTime)
		return
	}

	if !vm.hasMultiMiner() {
		utils.Log.Infof("need multi miner to generate block")
		return
	}

	var err error
	switch vm.GetNodeType() {
	case indexer.NODE_TYPE_BOOTSTRAP:
		if vm.isMyTurn() {
			err = vm.generateNewBlock_miner(vm.myself, vm.myself.Next)
		} else {
			err = vm.generateNewBlock_bootstrap()
		}

	case indexer.NODE_TYPE_CORE:
		if vm.isMyTurn() {
			err = vm.generateNewBlock_miner(vm.myself, vm.myself.Next)
		} else if vm.isMyGroupTurn() {
			err = vm.generateNewBlock_core()
		} else {
			utils.Log.Debugf("not my group turn")
		}

	case indexer.NODE_TYPE_MINER:
		if vm.isMyTurn() {
			err = vm.generateNewBlock_miner(vm.myself, vm.myself.Next)
		} else {
			utils.Log.Debugf("not my turn")
		}

	default:
		utils.Log.Infof("[ValidatorManager] %s is not a miner", vm.localValidatorId)
		return
	}
	if err != nil {
		utils.Log.Errorf("[ValidatorManager] generateNewBlock failed, %v", err)
		return
	}

	vm.lastBlockTime = 0
	utils.Log.Debugf("set lastBlockTime time to %d", vm.lastBlockTime)

}

func (vm *ValidatorManager) generateNewBlock_bootstrap() error {
	// 需要监控出块的miner有没有及时出块，如果没有，需要由核心节点代替出块
	// 如果核心节点也不在线，由引导节点代替出块
	now := time.Now().Unix()
	past := now - vm.lastBlockTime

	miningNode := vm.miningSeqMgr.GetCurrentMiningInfo()
	minerPeer := vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.PubKey)
	var fatherPeer *peerpkg.Peer
	if miningNode.NodeType == indexer.NODE_TYPE_MINER {
		fatherPeer = vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.Father.PubKey)
	}

	if miningNode.NodeType == indexer.NODE_TYPE_MINER {
		if minerPeer == nil || now-minerPeer.LastPingTime().Unix() > 4*int64(peerpkg.MinerPingSeconds) {
			// 该节点没连接，要等core node代替出块
			if fatherPeer == nil || now-fatherPeer.LastPingTime().Unix() > 4*int64(peerpkg.MinerPingSeconds) {
				// 该组的corenode不存在，或者已经很久没有连接过来，直接出块
				return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
			}
		}
	} else {
		// core node
		if minerPeer == nil || now-minerPeer.LastPingTime().Unix() > 4*int64(peerpkg.MinerPingSeconds) {
			// corenode不存在，或者已经很久没有连接过来，直接出块
			return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
		}
	}

	// 等待miner或者其father出块
	if past < 2*MinerInterval+PreWarningInterval {
		return fmt.Errorf("wait miner node %s to mine block", miningNode.PubKey)
	}

	// 已经超时，或者不在线，bootstrap节点代替出块
	return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
}

func (vm *ValidatorManager) generateNewBlock_core() error {
	// 监控下面的节点出块

	// 该组成员出块，需要监控出块的miner有没有及时出块，如果没有，需要由核心节点代替出块
	now := time.Now().Unix()
	past := now - vm.lastBlockTime

	miningNode := vm.miningSeqMgr.GetCurrentMiningInfo()
	peer := vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.PubKey)
	if peer != nil && peer.Connected() {
		if past < MinerInterval+PreWarningInterval {
			// 再等等
			return fmt.Errorf("wait miner node %s to mine block", miningNode.PubKey)
		}
	}

	// 已经超时，或者不在线，core节点代替出块
	return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
}

func (vm *ValidatorManager) hasMultiMiner() bool {
	switch vm.myself.NodeType {
	case indexer.NODE_TYPE_BOOTSTRAP:
		return vm.cfg.PosMiner.GetRandomCorePeer() != nil
	case indexer.NODE_TYPE_CORE, indexer.NODE_TYPE_MINER:
		return vm.cfg.PosMiner.GetPeerByValidatorId(vm.myself.Father.PubKey) != nil
	}
	return false
}

func (vm *ValidatorManager) generateNewBlock_miner(miningNode, nextNode *common.MiningInfo) error {
	var father, next *peerpkg.Peer
	if miningNode.Father != nil {
		father = vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.Father.PubKey)
		if father == nil {
			return fmt.Errorf("can't find father peer %s", miningNode.Father.PubKey)
		}
		//father.SendPing("", nil)
	}
	if nextNode != nil {
		next = vm.cfg.PosMiner.GetPeerByValidatorId(nextNode.PubKey)
		//if next != nil {
		//next.SendPing("", nil)
		//}
	}

	// 仅仅是记录排序器的实际挖矿地址，不是输入的miningNode
	currentMiningAddr := vm.miningSeqMgr.GetCurrentMiningAddr()

	// 出块后不要直接上链，而是给father节点去做进一步的审核，杜绝分叉
	block, err := vm.cfg.PosMiner.OnTimeGenerateBlock()
	if err != nil {
		utils.Log.Errorf("[ValidatorManager] OnTimeGenerateBlock failed, %v", err)
		return err
	}

	var buf bytes.Buffer
	if err := block.BtcEncode(&buf, wire.ProtocolVersion, wire.WitnessEncoding); err != nil {
		utils.Log.Errorf("block BtcEncode failed, %v", err)
		return err
	}
	payload := buf.Bytes()
	sig, err := vm.Sign(payload)
	if err != nil {
		utils.Log.Errorf("sign block failed, %v", err)
		return err
	}

	switch miningNode.NodeType {
	case indexer.NODE_TYPE_BOOTSTRAP:
		// 引导节点出块，只能随机选在线的核心节点，如果没有连接的节点，放弃出块
		core := vm.cfg.PosMiner.GetRandomCorePeer()
		if core == nil || !core.Connected() {
			utils.Log.Errorf("no other core node connected")
			return fmt.Errorf("no other core node connected")
		}
		err = core.SendMineBlockAndWait(2*time.Second, wire.CmdBlock, payload, sig)
		if err != nil {
			utils.Log.Errorf("[ValidatorManager] SendMineBlockAndWait %s failed, %v", core.String(), err)
			return err
		}

	case indexer.NODE_TYPE_CORE:
		if father != nil && father.Connected() {
			// 向fatherPeer发起ping请求，如果得到响应，就广播出块，否则就继续等
			err = father.SendMineBlockAndWait(2*time.Second, wire.CmdBlock, payload, sig)
			if err != nil {
				utils.Log.Errorf("[ValidatorManager] SendMineBlockAndWait %s failed, %v", father.String(), err)
				return err
			}
		} else {
			utils.Log.Errorf("not connect to bootstrap node")
			return fmt.Errorf("not connect to bootstrap node")
		}
		if next != nil {
			// 让next早点拿到block数据, 如果是普通miner发起的，在OnPing由core节点做这件事
			next.SendMineBlock(wire.CmdBlock, payload, sig)
		}

	case indexer.NODE_TYPE_MINER:
		if father != nil && father.Connected() {
			// 向fatherPeer发起ping请求，如果得到响应，就广播出块，否则就继续等
			err = father.SendMineBlockAndWait(4*time.Second, wire.CmdBlock, payload, sig)
			if err != nil {
				utils.Log.Errorf("[ValidatorManager] SendMineBlockAndWait %s failed, %v", father.String(), err)
				return err
			}
		} else {
			// 尝试连接其他core节点
			core := vm.cfg.PosMiner.GetRandomCorePeer()
			if core == nil || !core.Connected() {
				utils.Log.Errorf("no other core node connected")
				return fmt.Errorf("no other core node connected")
			}
			err = core.SendMineBlockAndWait(4*time.Second, wire.CmdBlock, payload, sig)
			if err != nil {
				utils.Log.Errorf("[ValidatorManager] SendMineBlockAndWait %s failed, %v", core.String(), err)
				return err
			}
		}
	}

	// 验证通过，提交block
	hash, height, err := vm.cfg.PosMiner.SubmitNewBlock(block)
	if err != nil {
		// 这里不应该失败！
		utils.Log.Errorf("[ValidatorManager] SubmitNewBlock %s failed, %v", block.BlockHash().String(), err)
		return err
	}

	// 等待排序器移动当前挖矿地址，避免下次进来还能继续挖矿
	i := 0
	for vm.miningSeqMgr.GetCurrentMiningAddr() == currentMiningAddr && i < 20 {
		time.Sleep(100 * time.Millisecond)
		i++
	}
	utils.Log.Infof("[ValidatorManager] SubmitNewBlock %s succeeded, height %d, next miner %s",
		hash.String(), height, vm.miningSeqMgr.GetCurrentMiningAddr())

	return nil
}

func (vm *ValidatorManager) GetNodeType() int {
	return vm.miningSeqMgr.GetNodeType(vm.localValidatorId)
}

func (vm *ValidatorManager) isMyTurn() bool {
	// 查询索引器当前轮到出块的pubkey
	info := vm.miningSeqMgr.GetCurrentMiningInfo()
	return info.PubKey == vm.localValidatorId
}

func (vm *ValidatorManager) isMyGroupTurn() bool {
	return vm.miningSeqMgr.CheckCurrentMiningPubKey(vm.localValidatorId) == nil
}

func GetMinerToken(height int, validatorId string) []byte {

	// Token Data format: "satsnet:height:validatorid:hash"
	tokenData := fmt.Sprintf("satsnet:miner:%d:%s", height, validatorId)
	tokenSource := sha256.Sum256([]byte(tokenData))
	return tokenSource[:]
}

func (vm *ValidatorManager) Sign(payload []byte) ([]byte, error) {
	sig, err := stp.SignMsg(payload)
	if err != nil {
		utils.Log.Errorf("ValidatorManager Sign failed, %v", err)
		return nil, err
	}
	return sig, nil
}

func (vm *ValidatorManager) VerifyBlockSig(validatorId string, payload, sig []byte) bool {
	pubKey, err := hex.DecodeString(validatorId)
	if err != nil {
		return false
	}
	publicKey, err := secp256k1.ParsePubKey(pubKey[:])
	if err != nil {
		utils.Log.Warnf("ParsePubKey %s failed, %v", validatorId, err)
		return false
	}

	signature, err := ecdsa.ParseDERSignature(sig)
	if err != nil {
		utils.Log.Warnf("ParseDERSignature failed, %v", err)
		return false
	}

	return anchortx.VerifyMessage(publicKey, payload, signature)
}

// 接收到其他节点发送过来的刚生成的block，需要进行验证
func (vm *ValidatorManager) OnBlockGenerated(peer *peerpkg.Peer, msg *wire.MsgMineBlock) {
	// 特殊的ping消息：
	// 如果是outbound的peer发过来的消息，不需要再往上发送，因为peer就是上级
	// 如果是inbound的peer发过来的消息，需要往上一级发送
	var code wire.RejectCode
	var reason string
	validatorId := peer.ValidatorId()
	var block *btcutil.Block
	for {
		code = wire.RejectInvalid
		if msg.SubCmd != wire.CmdBlock {
			reason = "payload is not block"
			break
		}

		miningSeqMgr := shareindexer.ShareIndexer.GetSeqMgr()
		if miningSeqMgr == nil {
			reason = "miningSeqMgr is nil"
			break
		}

		// 不一定是该validator挖的区块，但必然是矿工才转发，否则拒绝
		if miningSeqMgr.GetNodeType(validatorId) == indexer.NODE_TYPE_NORMAL {
			reason = "not a miner"
			break
		}

		// 检查block，是否可以被接受
		var msgBlock wire.MsgBlock
		rbuf := bytes.NewReader(msg.Payload)
		if err := msgBlock.BtcDecode(rbuf, wire.ProtocolVersion, wire.WitnessEncoding); err != nil {
			reason = "block BtcDecode failed, " + err.Error()
			break
		}
		utils.Log.Infof("OnBlockGenerated receive block %s", msgBlock.BlockHash().String())
		block = btcutil.NewBlock(&msgBlock)

		miningAddr := sindexer.GetMiningAddress(&msgBlock, vm.cfg.ChainParams)
		err := miningSeqMgr.CheckCurrentMiningAddr(miningAddr)
		if err != nil {
			reason = fmt.Sprintf("not its turn to mine a block, %s", miningAddr)
			break
		}
		miningNode := miningSeqMgr.GetMiningInfoWithAddr(miningAddr)
		if miningNode == nil {
			reason = fmt.Sprintf("GetMiningInfoWithAddr %s failed", miningAddr)
			break
		}

		currentMiner := miningSeqMgr.GetCurrentMiningAddr()
		if currentMiner != miningAddr {
			// 非miner本身，需要等过一个时间窗口
			now := time.Now().Unix()
			if now-vm.lastBlockTime < MinerInterval+PreWarningInterval {
				// The miner time is not past, ignore
				utils.Log.Debugf("now %d - lastblockTime %d = %d, < 15s", now, vm.lastBlockTime, now-vm.lastBlockTime)
				reason = "not in time"
				break
			}
		}

		checked := vm.VerifyBlockSig(miningNode.PubKey, msg.Payload, msg.Sig)
		if !checked {
			reason = "invalid signature"
			break
		}

		// Ensure the block is building from the expected previous block.
		expectedPrevHash := vm.cfg.Chain.BestSnapshot().Hash
		prevHash := &block.MsgBlock().Header.PrevBlock
		if !expectedPrevHash.IsEqual(prevHash) {
			reason = fmt.Sprintf("block %s not build from tip", msgBlock.BlockHash().String())
			break
		}
		if err := vm.cfg.Chain.CheckConnectBlockTemplate(block); err != nil {
			if _, ok := err.(blockchain.RuleError); !ok {
				reason = fmt.Sprintf("Failed to process block proposal: %v", err)
			} else {
				reason = fmt.Sprintf("Rejected block proposal: %v", err)
			}
			break
		}

		if peer.Inbound() {
			// 本地检查通过，如果还有上级节点，需要继续发给上级节点，让上级节点进一步检查 （最多2级）
			// 本地大概率就是miningNode.Father，所以这里需要再往上传
			if miningNode.Father != nil && miningNode.Father.Father != nil {
				pubkey := miningNode.Father.Father.PubKey
				// 本地节点如果是bootstrap，就不检查了
				if vm.localValidatorId != pubkey {
					bootstrap := vm.cfg.PosMiner.GetPeerByValidatorId(pubkey)
					if bootstrap == nil {
						reason = fmt.Sprintf("can't find bootstrap peer %s", pubkey)
						break
					}
					err := bootstrap.SendMineBlockAndWait(2*time.Second, wire.CmdBlock, msg.Payload, msg.Sig)
					if err != nil {
						reason = fmt.Sprintf("SendMineBlockAndWait %s failed, %v", bootstrap.String(), err)
						break
					}
				}
			}
		}

		// 让next优先得到该block
		next := vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.Next.PubKey)
		if next != nil && next.Connected() {
			next.SendMineBlock(wire.CmdBlock, msg.Payload, msg.Sig)
		}

		// 检查通过，该block可以被接受，尝试加入区块链
		_, err = vm.cfg.ProcessBlock(block, blockchain.BFFastAdd)
		if err != nil {
			reason = fmt.Sprintf("ProcessBlock failed, %v", err)
			break
		}
		utils.Log.Infof("block %s from %s is accepted", block.Hash().String(), peer.String())

		code = 0
		break
	}

	if code != 0 {
		utils.Log.Errorf("OnBlockGenerated %s failed, reason %s", peer.String(), reason)
	}

	// 响应ping消息
	peer.QueueMessage(wire.NewMsgMineAckWithCode(msg.Nonce, code, reason), nil)
}
