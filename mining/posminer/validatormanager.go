package posminer

import (
	"bytes"
	"fmt"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	
	MinerInterval      int64 = 9  // 出块时间间隔12S，但留3S给出块节点与引导节点同步数据
	PreWarningInterval int64 = 6   // 超时这么长时间还没出块

	CheckingInterval   int64 = 3   // 检查出块顺序

	UnexceptionInterval int64 = 2 * MinerInterval //  超过2个Miner的时间， 就认为出块异常， bootstrap node 会重启Epoch, 目前直接出块以防出块卡死
)

type ValidatorManagerConfig struct {
	*Config
	PosMiner *POSMiner
}

type ValidatorManager struct {
	// ValidatorId uint64
	cfg *ValidatorManagerConfig
	localValidatorId string
	miningSeqMgr *common.MiningSequenceMgr
	myself *common.MiningInfo
	lastBlockTime int64
	quit chan struct{}

	generatorTicker *time.Ticker
	pingTicker *time.Ticker
}

func NewValidatorManager(cfg *ValidatorManagerConfig) *ValidatorManager {
	utils.Log.Tracef("New ValidatorManager")
	validatorMgr := &ValidatorManager{
		cfg: cfg,
		localValidatorId: cfg.MiningPubKey,
		quit: make(chan struct{}),
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

	return validatorMgr
}

func (vm *ValidatorManager) Start() {
	utils.Log.Tracef("StartValidatorManager")
	
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
	vm.generatorTicker.Reset(time.Duration(MinerInterval)*time.Second)
}

// 一些有上下前后关系的miner相互ping，保持连接
func (vm *ValidatorManager) pingTimer() {
	vm.pingTicker = time.NewTicker(time.Duration(MinerInterval * 2) * time.Second)

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
	if vm.lastBlockTime == 0 {
		vm.lastBlockTime = vm.cfg.PosMiner.GetBlockRecvTime()
	}
	if now - vm.lastBlockTime < MinerInterval {
		// The miner time is not past, ignore
		utils.Log.Debugf("[ValidatorManager] not in time")
		return
	}

	if !vm.hasMultiMiner() {
		utils.Log.Infof("need multi miner to generate block")
		return
	}

	txSizeInMempool := vm.cfg.PosMiner.GetMempoolTxSize()
	if txSizeInMempool == 0 {
		utils.Log.Debugf("[ValidatorManager] mempool is empty, current miner %s", vm.miningSeqMgr.GetCurrentMiningAddr())
		// 重置等待时间
		vm.lastBlockTime = now
		return
	}

	var err error
	switch vm.GetNodeType() {
	case indexer.NODE_TYPE_BOOTSTRAP:
		err = vm.generateNewBlock_bootstrap()
	case indexer.NODE_TYPE_CORE:
		err = vm.generateNewBlock_core()
	case indexer.NODE_TYPE_MINER:
		if vm.isMyTurn() {
			err = vm.generateNewBlock_miner(vm.myself, vm.myself.Next)
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

}

func (vm *ValidatorManager) generateNewBlock_bootstrap() error {
	if vm.isMyTurn() {
		return vm.generateNewBlock_miner(vm.myself, vm.myself.Next)
	}

	// 需要监控出块的miner有没有及时出块，如果没有，需要由核心节点代替出块
	// 如果核心节点也不在线，由引导节点代替出块
	now := time.Now().Unix()
	lastBlockTime := vm.cfg.PosMiner.GetBlockRecvTime()
	past := now - lastBlockTime
	if past <= MinerInterval { // miner出块时间
		// 还没到时间
		return nil
	}
	
	miningNode := vm.miningSeqMgr.GetCurrentMiningInfo()
	minerPeer := vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.PubKey)
	var corePeer *peerpkg.Peer
	if miningNode.NodeType == indexer.NODE_TYPE_MINER {
		corePeer = vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.Father.PubKey)
	}
	if (minerPeer == nil || now - minerPeer.LastPingTime().Unix() < 4*int64(peerpkg.MinerPingSeconds)) &&
	(corePeer == nil || now - corePeer.LastPingTime().Unix() < 4*int64(peerpkg.MinerPingSeconds) ) {
		// 直接出块
		return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
	}

	if past < 2*MinerInterval + PreWarningInterval {
		// 已经到了core代替出块的时间
		if corePeer == nil || now - corePeer.LastPingTime().Unix() < 4*int64(peerpkg.MinerPingSeconds) {
			return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
		}
		return nil
	}

	// 已经超时，或者不在线，bootstrap节点代替出块
	return vm.generateNewBlock_miner(vm.myself, miningNode.Next)
}

func (vm *ValidatorManager) generateNewBlock_core() error {
	if vm.isMyTurn() {
		return vm.generateNewBlock_miner(vm.myself, vm.myself.Next)
	}
	if !vm.isMyGroupTurn() {
		return nil
	}

	// 该组成员出块，需要监控出块的miner有没有及时出块，如果没有，需要由核心节点代替出块
	now := time.Now().Unix()
	lastBlockTime := vm.cfg.PosMiner.GetBlockRecvTime()
	past := now - lastBlockTime
	if past <= MinerInterval {
		// 还没到时间
		return nil
	}
	
	miningNode := vm.miningSeqMgr.GetCurrentMiningInfo()
	peer := vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.PubKey)
	if peer != nil && now - peer.LastPingTime().Unix() < 4*int64(peerpkg.MinerPingSeconds) {
		if past > MinerInterval && past < MinerInterval + PreWarningInterval {
			// 已经到了必须出块的时间，继续等
			return nil
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
	// 先ping一下父节点和下一个节点，让对方知道我在线
	var father, next *peerpkg.Peer
	if miningNode.Father != nil {
		father = vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.Father.PubKey)
		if father == nil {
			return fmt.Errorf("can't find father peer %s", miningNode.Father.PubKey)
		}
		father.SendPing("", nil)
	}
	if nextNode != nil {
		next = vm.cfg.PosMiner.GetPeerByValidatorId(nextNode.PubKey)
		if next != nil {
			next.SendPing("", nil)
		}
	}

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

	switch miningNode.NodeType {
	case indexer.NODE_TYPE_BOOTSTRAP:
		// 引导节点出块，只能随机选在线的核心节点，如果没有连接的节点，放弃出块
		core := vm.cfg.PosMiner.GetRandomCorePeer()
		if core == nil || !core.Connected() {
			utils.Log.Errorf("no other core node connected")
			return fmt.Errorf("no other core node connected")
		}
		err = core.SendPingAndWait(2 * time.Second, wire.CmdBlock, buf.Bytes())
		if err != nil {
			utils.Log.Errorf("[ValidatorManager] sendPingAndWait %s failed, %v", core.String(), err) 
			return err
		}

	case indexer.NODE_TYPE_CORE:
		if father != nil && father.Connected() {
			// 向fatherPeer发起ping请求，如果得到响应，就广播出块，否则就继续等
			err = father.SendPingAndWait(2 * time.Second, wire.CmdBlock, buf.Bytes())
			if err != nil {
				utils.Log.Errorf("[ValidatorManager] sendPingAndWait %s failed, %v", father.String(), err) 
				return err
			}
		} else {
			utils.Log.Errorf("not connect to bootstrap node")
			return fmt.Errorf("not connect to bootstrap node")
		}
		if next != nil {
			// 让next早点拿到block数据, 如果是普通miner发起的，在OnPing由core节点做这件事
			next.SendPing(wire.CmdBlock, buf.Bytes())
		}
		
	case indexer.NODE_TYPE_MINER:
		if father != nil && father.Connected() {
			// 向fatherPeer发起ping请求，如果得到响应，就广播出块，否则就继续等
			err = father.SendPingAndWait(2 * time.Second, wire.CmdBlock, buf.Bytes())
			if err != nil {
				utils.Log.Errorf("[ValidatorManager] sendPingAndWait %s failed, %v", father.String(), err) 
				return err
			}
		} else {
			// 尝试连接其他core节点
			core := vm.cfg.PosMiner.GetRandomCorePeer()
			if core == nil || !core.Connected() {
				utils.Log.Errorf("no other core node connected")
				return fmt.Errorf("no other core node connected")
			}
			err = core.SendPingAndWait(2 * time.Second, wire.CmdBlock, buf.Bytes())
			if err != nil {
				utils.Log.Errorf("[ValidatorManager] sendPingAndWait %s failed, %v", core.String(), err) 
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
	for vm.miningSeqMgr.GetCurrentMiningAddr() != currentMiningAddr {
		time.Sleep(100*time.Millisecond)
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
