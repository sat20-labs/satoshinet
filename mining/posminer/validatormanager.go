package posminer

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechaindb"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	
	MinerInterval      int64 = 10  // 出块时间间隔12S，但留2S给出块节点与引导节点同步数据
	PreWarningInterval int64 = 6   // 超时这么长时间还没出块

	CheckingInterval   int64 = 2   // 检查出块顺序

	UnexceptionInterval int64 = 2 * MinerInterval //  超过2个Miner的时间， 就认为出块异常， bootstrap node 会重启Epoch, 目前直接出块以防出块卡死
)

type PosMinerInterface interface {
	// OnTimeGenerateBlock is invoke when time to generate block.
	OnTimeGenerateBlock() (*wire.MsgBlock, error)

	// submit to blockchain
	SubmitNewBlock(*wire.MsgBlock) (*chainhash.Hash, int32, error)

	// GetBlockHeight invoke when get block height from pos miner.
	GetBlockHeight() int32
	GetBlockRecvTime() int64

	// GetMempoolTxSize invoke for get tx count in mempool.
	GetMempoolTxSize() int32

	GetPeerByValidatorId(validatorId string) *peerpkg.Peer
	GetRandomCorePeer() *peerpkg.Peer
}

type ValidatorManagerConfig struct {
	*Config
	PosMiner PosMinerInterface
}

type ValidatorManager struct {

	// ValidatorId uint64
	cfg *ValidatorManagerConfig
	localValidatorId string

	validatorRecordMgr *ValidatorRecordMgr // 所有的已经连接过的验证者列表
	miningSeqMgr *common.MiningSequenceMgr
	
	fatherInfo *common.MiningInfo
	nextInfo *common.MiningInfo // 下一个要出块的节点
	myself *common.MiningInfo
	peerMtx sync.RWMutex

	quit chan struct{}

	generatorTicker *time.Ticker

	// NewVCStore
	vcStore         *validatechaindb.ValidateChainStore // VC Store
	validateChain   *ValidateChain        // VC  --- validate chain
	saveVCBlockMtx sync.RWMutex
}

//var validatorMgr *ValidatorManager

func NewValidatorManager(cfg *ValidatorManagerConfig) *ValidatorManager {
	utils.Log.Tracef("New ValidatorManager")
	validatorMgr := &ValidatorManager{
		cfg: cfg,
		localValidatorId: cfg.MiningPubKey,
		quit: make(chan struct{}),
	}

	vcStore, err := validatechaindb.NewVCStore(cfg.BtcdDir)
	if err != nil {
		utils.Log.Errorf("NewVCStore failed: %v", err)
		return nil
	}
	validatorMgr.vcStore = vcStore
	validatorMgr.validateChain = NewValidateChain(vcStore)

	utils.Log.Tracef("New ValidatorManager succeed")
	return validatorMgr
}

func (vm *ValidatorManager) Start() {
	utils.Log.Tracef("StartValidatorManager")

	
	/* 启动相关定时器
	1. 定时向fatherPeer发送ping消息
	2. 挖矿定时器，每个挖矿周期都检查是不是轮到自己挖矿
	3. 
	*/
	vm.miningSeqMgr = shareindexer.ShareIndexer.GetSeqMgr()
	if vm.miningSeqMgr == nil {
		utils.Log.Panic("miningSeqMgr is nil")
	}
	
	go vm.generatorTimer()
}

func (vm *ValidatorManager) LoadValidatorRecordList() *ValidatorRecordMgr {
	vm.validatorRecordMgr = LoadValidatorRecordList(vm.cfg.BtcdDir)
	
	return vm.validatorRecordMgr
}

func (vm *ValidatorManager) Stop() {
	utils.Log.Tracef("ValidatorManager Stop")

	close(vm.quit)
}

func (vm *ValidatorManager) IsValidGenerator(generator *peerpkg.Peer) bool {
	if generator == nil {
		return false
	}

	validator := generator.ValidatorId()
	if validator == "" {
		return false
	}

	// 使用公钥验证签名
	// valid := generator.VerifyToken(validatorConnected.ValidatorInfo.ValidatorId)
	// if valid {
	// 	utils.Log.Tracef("Signature is valid.")
	// 	generator.Validatorinfo = &validatorConnected.ValidatorInfo
	// 	return true
	// } else {
	// 	utils.Log.Tracef("Signature is invalid.")
	// 	return false
	// }

	return false

}

// 获得当前准备出块的peer
func (vm *ValidatorManager) GetGenerator() *peerpkg.Peer {
	// TODO 
	// 如果不在线，也要返回一个替补节点
	return nil
}


func (vm *ValidatorManager) GetCurrentBlockHeight() int32 {
	return vm.cfg.PosMiner.GetBlockHeight()
}

func (vm *ValidatorManager) resetGeneratorMoniter() {
	utils.Log.Debugf("resetGeneratorMoniter...")
	if vm.generatorTicker == nil {
		// Not start monitor
		utils.Log.Tracef("GeneratorTicker is not start or stopped.")
		return
	}

	utils.Log.Debugf("local generator: Next check generator after %d seconds.", MinerInterval)
	vm.generatorTicker.Reset(time.Duration(MinerInterval)*time.Second)
}


func (vm *ValidatorManager) GetMyValidatorId() string {
	return vm.cfg.MiningPubKey
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
	defer func() {
		utils.Log.Debugf("[ValidatorManager]checkAndGenerateNewBlock finished.")
	}()
	
	// TODO
	if !vm.cfg.IsCurrent() {
		utils.Log.Infof("[ValidatorManager] not reach the tip of block yet")
		return
	}

	lastBlockTime := vm.cfg.PosMiner.GetBlockRecvTime()
	if time.Now().Unix() - lastBlockTime < MinerInterval {
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
		utils.Log.Infof("[ValidatorManager] mempool is empty")
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
	}

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
	if past <= MinerInterval {
		// 还没到时间
		return nil
	}
	
	miningNode := vm.miningSeqMgr.GetCurrentMiningInfo()
	peer := vm.cfg.PosMiner.GetPeerByValidatorId(miningNode.PubKey)
	if past < 2*MinerInterval + PreWarningInterval {
		// 已经到了miner或者core代替出块的时间，继续等
		return nil
	} else if past < 3*MinerInterval {
		if peer != nil && peer.Connected() {
			err := peer.SendPingAndWait(2 * time.Second, "", nil)
			if err == nil {
				// 在线，等最后的几秒钟
				return err
			}
		}
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
	if past > MinerInterval && past < MinerInterval + PreWarningInterval {
		// 已经到了必须出块的时间，继续等
		return nil
	} else if past > MinerInterval + PreWarningInterval && past <= 2*MinerInterval {
		// 看看该节点是不是不在线，如果不在线，就代替出块
		if peer != nil && peer.Connected() {
			err := peer.SendPingAndWait(2 * time.Second, "", nil)
			if err == nil {
				// 在线，等最后的几秒钟
				return err
			}
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
	if father != nil {
		// 向fatherPeer发起ping请求，如果得到响应，就广播出块，否则就继续等
		err = father.SendPingAndWait(2 * time.Second, wire.CmdBlock, buf.Bytes())
		if err != nil {
			utils.Log.Errorf("[ValidatorManager] sendPingAndWait %s failed, %v", father.String(), err) 
			return err
		}
	} else {
		// 引导节点出块，只能随机选在线的核心节点，如果连接的节点，放弃出块
		core := vm.cfg.PosMiner.GetRandomCorePeer()
		if core != nil {
			utils.Log.Errorf("no one core node connected")
			return fmt.Errorf("no one core node connected")
		}
		err = core.SendPingAndWait(2 * time.Second, wire.CmdBlock, buf.Bytes())
		if err != nil {
			utils.Log.Errorf("[ValidatorManager] sendPingAndWait %s failed, %v", core.String(), err) 
			return err
		}
	}
	if next != nil {
		// 让next早点拿到block数据
		next.SendPing(wire.CmdBlock, buf.Bytes())
	}
	
	// 验证通过，提交block
	hash, height, err := vm.cfg.PosMiner.SubmitNewBlock(block)
	if err != nil {
		// 这里不应该失败！
		utils.Log.Errorf("[ValidatorManager] SubmitNewBlock %s failed, %v", block.BlockHash().String(), err)
		return err
	}
	utils.Log.Infof("[ValidatorManager] SubmitNewBlock %s succeeded, height = %d", hash.String(), height)

	return nil
}

func (vm *ValidatorManager) GetNodeType() int {
	return vm.miningSeqMgr.GetNodeType(vm.localValidatorId)
}

func (vm *ValidatorManager) IsBootstrapNode() bool {
	// 查询索引器当前轮到出块的pubkey
	return false
}


func (vm *ValidatorManager) IsCoreNode() bool {
	// 查询索引器当前轮到出块的pubkey
	return false
}

func (vm *ValidatorManager) isMyTurn() bool {
	// 查询索引器当前轮到出块的pubkey
	return false
}

func (vm *ValidatorManager) isMyGroupTurn() bool {
	// 查询索引器当前轮到出块的pubkey
	return false
}

// syncValidateChain for sync validate chain list from remote peer on a timer
func (vm *ValidatorManager) syncValidateChain() {
	utils.Log.Tracef("[ValidatorManager]syncValidateChain ....")
	// getVCStateCmd := validatorcommand.NewMsgGetVCState(vm.cfg.ValidatorId)

	// // Clear the sync validator and start sync VC State from all connected validators
	// vm.vcSyncValidator = nil

	// for _, validator := range vm.connectedList {
	// 	utils.Log.Tracef("[syncValidateChain]Get VC State form %s...", validator.String())
	// 	validator.SendCommand(getVCStateCmd)
	// }

	utils.Log.Tracef("[ValidatorManager]syncValidateChain done.")
}

func (vm *ValidatorManager) syncVCBlock() {
	utils.Log.Tracef("[ValidatorManager]syncVCBlock ....")
	// if vm.vcSyncValidator == nil {
	// 	return
	// }
	// if vm.vcSyncValidator.IsConnected() == false {
	// 	return
	// }
	// utils.Log.Tracef("[syncVCBlock]Get VC Block form %s...", vm.vcSyncValidator.String())
	// // getVCListCmd := validatorcommand.NewMsgGetVCList()
	// // vm.vcSyncValidator.GetVCBlock()

	utils.Log.Tracef("[ValidatorManager]syncVCBlock Done.")
}

// // Received get vc state command
// func (vm *ValidatorManager) GetVCState(validatorId string) (*wire.MsgVCState, error) {
// 	// if vm.validateChain == nil {
// 	// 	err := errors.New("ValidateChain is invalid")
// 	// 	return nil, err
// 	// }
// 	// localState := vm.validateChain.GetCurrentState()
// 	// if localState == nil {
// 	// 	err := errors.New("ValidateChain state is invalid")
// 	// 	return nil, err
// 	// }
// 	// vcStateCmd := validatorcommand.NewMsgVCState(localState.LatestHeight, localState.LatestHash, localState.LatestEpochIndex)
// 	// return vcStateCmd, nil
// }

// // Received a vc state command
// func (vm *ValidatorManager) OnVCState(vcStateCmd *wire.MsgVCState, validator *peerpkg.Peer) {
// 	if vm.validateChain == nil {
// 		err := errors.New("ValidateChain is invalid")
// 		utils.Log.Error(err)
// 		return
// 	}
// 	localState := vm.validateChain.GetCurrentState()
// 	if localState == nil {
// 		err := errors.New("ValidateChain state is invalid")
// 		utils.Log.Error(err)
// 		return
// 	}

// 	if vcStateCmd.Height > localState.LatestHeight {
// 		if validator != nil && vm.vcSyncValidator == nil {
// 			// Start sync VC block from the validator
// 			vm.vcSyncValidator = validator
// 			getVCListCmd := validatorcommand.NewMsgGetVCList(vm.cfg.ValidatorId, localState.LatestHeight, vcStateCmd.Height)
// 			//getVCListCmd.LogCommandInfo()
// 			vm.vcSyncValidator.SendCommand(getVCListCmd)
// 		}
// 	} else {
// 		// The validator has the latest VC block, check the VC block has blocks need to be sync
// 		missStart := int64(-1)
// 		start := int64(1)
// 		if localState.LatestHeight > 100 {
// 			start = localState.LatestHeight - 100 // Max save 100 blocks in local
// 		}

// 		// the vcblock height start form 1
// 		for i := start; i <= localState.LatestHeight; i++ {
// 			_, err := vm.validateChain.GetVCBlockHash(i)
// 			if err != nil {
// 				missStart = i
// 				break
// 			}
// 		}

// 		if missStart != -1 {
// 			if validator != nil {
// 				// Start sync VC block from the validator
// 				getVCListCmd := validatorcommand.NewMsgGetVCList(vm.cfg.ValidatorId, missStart, localState.LatestHeight)
// 				//getVCListCmd.LogCommandInfo()
// 				validator.SendCommand(getVCListCmd)
// 			}
// 		}
// 	}
// }

// // Received get vc list command
// func (vm *ValidatorManager) GetVCList(validatorId string, start int64, end int64) (*wire.MsgVCList, error) {
// 	utils.Log.Infof("[ValidatorManager]GetVCList: Start [%d] End [%d]", start, end)
// 	VCList := make([]*validatorcommand.VCItem, 0)
// 	count := 0
// 	for i := start; i <= end; i++ {
// 		hash, err := vm.validateChain.GetVCBlockHash(i)
// 		if err != nil {
// 			// generator := vm.myValidator.GetMyGenerator()
// 			// if generator != nil {
// 			// 	generator.ContinueNextSlot()
// 			// }
// 			utils.Log.Errorf("Get VC Block Hash [%d] failed: %v", i, err)
// 			continue
// 		}
// 		//utils.Log.Tracef("Add Height [%d] and VC Block Hash [%s] to VC List", i, hash.String())
// 		VCList = append(VCList, &validatorcommand.VCItem{Height: i, Hash: *hash})
// 		count++
// 		if count >= validatorcommand.MaxVCList {
// 			break
// 		}
// 	}
// 	vclistCmd := validatorcommand.NewMsgVCList(VCList)
// 	return vclistCmd, nil
// }

// // Received a vc list command
// func (vm *ValidatorManager) OnVCList(vclistCmd *validatorcommand.MsgVCList, validator *validator.Validator) {
// 	if vclistCmd == nil || len(vclistCmd.VCList) == 0 {
// 		return
// 	}

// 	// Get all VC Block data from the validator
// 	for _, item := range vclistCmd.VCList {
// 		_, err := vm.validateChain.GetVCBlockHash(item.Height)
// 		if err != nil {
// 			utils.Log.Tracef("Request VC Block [%s] with Height [%d] ", item.Hash.String(), item.Height)
// 			// local missing VC Block, request the VC Block from the validator
// 			getVCBlockCmd := validatorcommand.NewMsgGetVCBlock(vm.cfg.ValidatorId, validatorcommand.BlockType_VCBlock, item.Hash)
// 			validator.SendCommand(getVCBlockCmd)
// 		}
// 	}
// }

// // Received get vc block command
// func (vm *ValidatorManager) GetVCBlock(validatorId string, blockType uint32, hash chainhash.Hash) (*validatorcommand.MsgVCBlock, error) {
// 	utils.Log.Infof("[ValidatorManager]GetVCBlock: Block type [%d] Hash [%s] from %d", blockType, hash.String(), validatorId)
// 	var blockData []byte
// 	if blockType == validatorcommand.BlockType_VCBlock {
// 		// Get VC Block Data
// 		vcBlockData, err := vm.vcStore.GetBlockData(hash[:])
// 		if err != nil {
// 			return nil, err
// 		}
// 		blockData = vcBlockData
// 	} else {
// 		// Get EP Block Data
// 		epBlockData, err := vm.vcStore.GetEPBlockData(hash[:])
// 		if err != nil {
// 			return nil, err
// 		}
// 		blockData = epBlockData
// 	}

// 	getVCBlockCmd := validatorcommand.NewMsgVCBlock(hash, blockType, blockData)
// 	return getVCBlockCmd, nil
// }

// // BroadcastVCBlock broadcasts a VC or EP block to connected peers based on the block type.
// // It retrieves the block data from the VC store using the provided hash and block type.
// // If the block type is a VC block, it fetches VC block data; otherwise, it fetches EP block data.
// // The function constructs a new MsgVCBlock command with the retrieved data and broadcasts it.
// // Returns an error if retrieving the block data fails.
// func (vm *ValidatorManager) BroadcastVCBlock(blockType uint32, hash *chainhash.Hash) error {
// 	var blockData []byte
// 	if blockType == validatorcommand.BlockType_VCBlock {
// 		// Get VC Block Data
// 		vcBlockData, err := vm.vcStore.GetBlockData(hash[:])
// 		if err != nil {
// 			utils.Log.Errorf("Get VC Block [%s] failed: %v", hash.String(), err)
// 			return err
// 		}
// 		blockData = vcBlockData
// 	} else {
// 		// Get EP Block Data
// 		epBlockData, err := vm.vcStore.GetEPBlockData(hash[:])
// 		if err != nil {
// 			utils.Log.Errorf("Get EP Block [%s] failed: %v", hash.String(), err)
// 			return err
// 		}
// 		blockData = epBlockData
// 	}

// 	utils.Log.Tracef("Broadcast Block [%s] to validatechain...", hash.String())
// 	vcBlockCmd := validatorcommand.NewMsgVCBlock(*hash, blockType, blockData)
// 	vm.BroadcastCommand(vcBlockCmd)
// 	return nil
// }

// // Received a vc block command
// func (vm *ValidatorManager) OnVCBlock(vcblockCmd *validatorcommand.MsgVCBlock, validator *validator.Validator) {
// 	utils.Log.Tracef("[ValidatorManager]OnVCBlock...")
// 	if vcblockCmd == nil || vcblockCmd.Payload == nil {
// 		utils.Log.Tracef("[ValidatorManager]Invalid vc block message.")
// 		return
// 	}

// 	if vcblockCmd.BlockType == validatorcommand.BlockType_VCBlock {
// 		utils.Log.Tracef("[ValidatorManager]New vc block [%s].", vcblockCmd.Hash.String())
// 		vcBlock, err := vm.validateChain.GetVCBlock(&vcblockCmd.Hash)
// 		if err == nil {
// 			utils.Log.Tracef("The Block [%s] already exists in local [%d] ", vcBlock.Header.Hash.String(), vcBlock.Header.Height)
// 			return
// 		}

// 		vcBlock = &validatechain.VCBlock{}
// 		err = vcBlock.Decode(vcblockCmd.Payload)
// 		if err != nil {
// 			utils.Log.Error(err)
// 			return
// 		}
// 		vcBlockHash, err := vcBlock.GetHash()
// 		if err != nil {
// 			utils.Log.Error(err)
// 			return
// 		}
// 		if vcBlockHash.IsEqual(&vcblockCmd.Hash) == false {
// 			utils.Log.Error("Block hash is invalid")
// 			return
// 		}
// 		// check the block is valid
// 		err = vm.isReceptVCBlock(vcBlock)
// 		if err != nil {
// 			utils.Log.Errorf("Block isnot recepted:%v", err)
// 			return
// 		}
// 		// Save VC Block data to local
// 		vm.SaveVCBlock(vcBlock)

// 	} else {
// 		// Save EP Block data to local
// 		utils.Log.Tracef("[ValidatorManager]New ep block [%s].", vcblockCmd.Hash.String())
// 		epBlock := &validatechain.EPBlock{}
// 		err := epBlock.Decode(vcblockCmd.Payload)
// 		if err != nil {
// 			utils.Log.Error(err)
// 			return
// 		}
// 		epBlockHash, err := epBlock.GetHash()
// 		if err != nil {
// 			utils.Log.Error(err)
// 			return
// 		}
// 		if epBlockHash.IsEqual(&vcblockCmd.Hash) == false {
// 			utils.Log.Error("Block hash is invalid")
// 			return
// 		}
// 		// Save EP Block data to local
// 		utils.Log.Tracef("[ValidatorManager]SaveEPBlock...")
// 		vm.validateChain.SaveEPBlock(epBlock)
// 	}

// }

// func (vm *ValidatorManager) VCBlock_MinerNewBlock(minerNewBlock *generator.MinerNewBlock) {
// 	// record Miner new block to validatechain
// 	// 1. save vcblock to vc store
// 	// 2. broadcast the vc block to all validators

// 	vcBlock := validatechain.NewVCBlock()
// 	curState := vm.validateChain.GetCurrentState()

// 	vcBlock.Header.Height = curState.LatestHeight + 1 // height + 1
// 	vcBlock.Header.PrevHash = curState.LatestHash     // prev hash is current state hash
// 	vcBlock.Header.DataType = validatechain.DataType_MinerNewBlock

// 	dataBlock := &validatechain.DataMinerNewBlock{
// 		GeneratorId:   minerNewBlock.GeneratorId,
// 		Timestamp:     minerNewBlock.MinerTime,
// 		SatsnetHeight: minerNewBlock.Height,
// 		Hash:          *minerNewBlock.Hash,
// 		Token:         minerNewBlock.Token,
// 	}

// 	vcBlock.Data = dataBlock

// 	err := vm.SaveVCBlock(vcBlock)
// 	if err != nil {
// 		return
// 	}

// 	vm.BroadcastVCBlock(validatorcommand.BlockType_VCBlock, &vcBlock.Header.Hash)
// }

// func (vm *ValidatorManager) GetVCStore() *validatechaindb.ValidateChainStore {
// 	return vm.vcStore
// }

// func (vm *ValidatorManager) SaveVCBlock(vcBlock *validatechain.VCBlock) error {
// 	utils.Log.Tracef("[ValidatorManager]SaveVCBlock...")
// 	vm.saveVCBlockMtx.Lock()
// 	defer vm.saveVCBlockMtx.Unlock()
// 	// Save VC Block data to local
// 	err := vm.validateChain.SaveVCBlock(vcBlock)
// 	if err != nil {
// 		utils.Log.Tracef("[ValidatorManager]SaveVCBlock to DB failed : %v", err)
// 		return err
// 	}

// 	// Save VC Block Hash
// 	err = vm.validateChain.SaveVCBlockHash(int64(vcBlock.Header.Height), &vcBlock.Header.Hash)
// 	if err != nil {
// 		utils.Log.Tracef("[ValidatorManager]SaveVCBlockHash to DB failed : %v", err)
// 		return err
// 	}

// 	utils.Log.Tracef("[ValidatorManager]New block has Saved, Height: %d, Hash: %s.", vcBlock.Header.Height, vcBlock.Header.Hash.String())

// 	localState := vm.validateChain.GetCurrentState()
// 	if localState == nil {
// 		localState = &validatechain.ValidateChainState{}
// 	}

// 	utils.Log.Tracef("[ValidatorManager]Current vc state , LatestHeight: %d, LatestHash: %s, LatestEpochIndex:%d.", localState.LatestHeight, localState.LatestHash, localState.LatestEpochIndex)
// 	// Update Current State
// 	if localState.LatestHeight < int64(vcBlock.Header.Height) {
// 		newEpochIndex := localState.LatestEpochIndex

// 		vcd, ok := vcBlock.Data.(*validatechain.DataUpdateEpoch)
// 		if ok {
// 			// The Block is epoch indx, the epoch index is changed
// 			newEpochIndex = vcd.EpochIndex
// 		}

// 		newVCState := &validatechain.ValidateChainState{
// 			LatestHeight:     vcBlock.Header.Height,
// 			LatestHash:       vcBlock.Header.Hash,
// 			LatestEpochIndex: newEpochIndex,
// 		}
// 		utils.Log.Tracef("[ValidatorManager]Update new vc state , LatestHeight: %d, LatestHash: %s, LatestEpochIndex:%d.", newVCState.LatestHeight, newVCState.LatestHash, newVCState.LatestEpochIndex)
// 		err = vm.validateChain.UpdateCurrentState(newVCState)
// 		if err != nil {
// 			utils.Log.Tracef("[ValidatorManager]UpdateCurrentState to DB failed : %v", err)
// 			return err
// 		}

// 		vcd_newBlock, ok := vcBlock.Data.(*validatechain.DataMinerNewBlock)
// 		if ok {
// 			blockHash_satsnet := vcd_newBlock.Hash
// 			blochHeight_satsnet := vcd_newBlock.SatsnetHeight
// 			// Notify satsnet chain for a new block mined
// 			vm.cfg.PosMiner.OnNewBlockMined(&blockHash_satsnet, blochHeight_satsnet)
// 		}
// 	}

// 	return nil
// }
