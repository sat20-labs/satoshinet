package posminer

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechaindb"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MinerInterval       = 10 * time.Second  // 出块时间间隔12S，但留2S给出块节点与引导节点同步数据

	CheckingInterval       = 2 * time.Second  // 检查出块顺序

	UnexceptionInterval = 2 * MinerInterval //  超过2个Miner的时间， 就认为出块异常， bootstrap node 会重启Epoch, 目前直接出块以防出块卡死
)

type PosMinerInterface interface {
	// OnTimeGenerateBlock is invoke when time to generate block.
	OnTimeGenerateBlock() (*chainhash.Hash, int32, error)

	// GetBlockHeight invoke when get block height from pos miner.
	GetBlockHeight() int32

	// OnNewBlockMined is invoke when new block is mined.
	OnNewBlockMined(hash *chainhash.Hash, height int32)

	// GetMempoolTxSize invoke for get tx count in mempool.
	GetMempoolTxSize() int32
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
	
	fatherPeer *peerpkg.Peer
	bootstrapPeer *peerpkg.Peer
	brotherPeers []*peerpkg.Peer
	childPeers []*peerpkg.Peer
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
	
	go vm.generatorTimer()

	go vm.observeHandler()
}

func (vm *ValidatorManager) LoadValidatorRecordList() *ValidatorRecordMgr {
	vm.validatorRecordMgr = LoadValidatorRecordList(vm.cfg.BtcdDir)
	
	return vm.validatorRecordMgr
}

func (vm *ValidatorManager) Stop() {
	utils.Log.Tracef("ValidatorManager Stop")

	close(vm.quit)
}

func (vm *ValidatorManager) isLocalValidator(pubkey string) bool {
	return vm.localValidatorId == pubkey
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


func (vm *ValidatorManager) SyncValidators() {
	utils.Log.Tracef("[SyncValidators]Will sync validators...")
	// TODO 从引导节点同步其他已经在线的核心节点
}

func (vm *ValidatorManager) OnNewValidatorPeerConnected(validator *peerpkg.Peer) {
	// New validator peer is connected
	utils.Log.Tracef("[ValidatorManager]New validator peer connected: %s", validator.String())

	validatorId := validator.ValidatorId()

	if vm.isLocalValidator(validatorId) {
		utils.Log.Infof("not allow same pubkey %s connected", validatorId)
		return
	}
	if validator.Addr() == "127.0.0.1" {
		utils.Log.Infof("not allow localhost connected")
		return
	}

	validatorPeer := vm.LookupValidator(validatorId)
	if validatorPeer != nil {
		// The validator is already connected, will try to check connection again
		utils.Log.Tracef("[ValidatorManager]New validator has added in connectedlist: %s", validator.Addr())
		return
	}

	//vm.connectedList = append(vm.connectedList, peerValidator)
	vm.AddActivieValidator(validator)

	utils.Log.Tracef("[ValidatorManager]New validator added to connectedlist: %s", validator.Addr())
}

func (vm *ValidatorManager) OnValidatorPeerDisconnected(validator *peerpkg.Peer) {
	// Remote validator peer disconnected, it will be notify by remote validator when it cannot connect or sent any command
	utils.Log.Tracef("[ValidatorManager]validator peer is disconnected: %s", validator.String())
	if validator == nil {
		return
	}

	vm.removeValidator(validator.ValidatorId())
}

func (vm *ValidatorManager) OnValidatorPeerInactive(netAddr net.Addr) {
	// Remote validator peer is inactive, it will be notify by local validator when it is long time to not received any command
	utils.Log.Tracef("[ValidatorManager]validator peer in inactive: %s", netAddr.String())
}

func (vm *ValidatorManager) AddActivieValidator(validator *peerpkg.Peer) error {
	if !validator.Connected() {
		utils.Log.Errorf("validator %s is not connected", validator.ValidatorId())
		return fmt.Errorf("validator is not connected")
	}

	vm.peerMtx.Lock()
	defer vm.peerMtx.Unlock()

	// TODO 判断是哪种validator，然后保存起来
	//vm.connectedList = append(vm.connectedList, validator)

	// 只保存核心节点
	vm.validatorRecordMgr.UpdateValidatorRecord(validator.ValidatorId(), validator.Addr())


	return nil
}


// 列表中有pubkey不同的validator
func (vm *ValidatorManager) HasRemoteValidator() bool {
	vm.peerMtx.Lock()
	defer vm.peerMtx.Unlock()

	return false
}


func (vm *ValidatorManager) GetBootstrapValidator() *peerpkg.Peer {
	return vm.bootstrapPeer
}

func (vm *ValidatorManager) GetDefaultCoreValidator() *peerpkg.Peer {
	return vm.fatherPeer
}

func (vm *ValidatorManager) LookupValidator(pubkey string) *peerpkg.Peer {
	// TODO
	return nil
}

func (vm *ValidatorManager) removeValidator(validatorId string) {
	// TODO 
}

// moniterHandler for show current validator list in local on a timer
func (vm *ValidatorManager) observeHandler() {
	observeInterval := time.Second * 10
	observeTicker := time.NewTicker(observeInterval)
	defer observeTicker.Stop()

exit:
	for {
		select {
		case <-observeTicker.C:
			vm.showCurrentStats()
		case <-vm.quit:
			break exit
		}
	}

	utils.Log.Tracef("[ValidatorManager]observeHandler done.")
}

func (vm *ValidatorManager) showCurrentStats() {
	// validatorList := vm.getValidatorList()

	// for test
	//vm.ReqNewEpoch(vm.cfg.ValidatorId, 10, 1)

	utils.Log.Tracef("********************************* Observe Validators Summary ********************************")
	// showValidatorList(validatorList)
	for _, validator := range vm.childPeers {
		if validator != nil {
			utils.Log.Tracef("%v", validator)
			utils.Log.Tracef("----------------------------------------------------------------")
		}
	}

	utils.Log.Tracef("*********************************        End        ********************************")
}

// syncValidatorsHandler for sync validator list from remote peer on a timer
func (vm *ValidatorManager) syncValidatorsHandler() {

	// First sync validator list , and then sync epoch in 60s
	vm.SyncValidators()

	syncInterval := time.Second * 60
	syncTicker := time.NewTicker(syncInterval)
	defer syncTicker.Stop()

exit:
	for {
		utils.Log.Tracef("[ValidatorManager]Waiting next timer for syncing validator list...")
		select {
		case <-syncTicker.C:
			vm.SyncValidators()
		case <-vm.quit:
			break exit
		}
	}

	utils.Log.Tracef("[ValidatorManager]syncValidatorsHandler done.")
}

func (vm *ValidatorManager) SetLocalAsNextGenerator(height int32, handoverTime time.Time) {
	utils.Log.Tracef("[ValidatorManager]SetLocalAsNextGenerator for mine block height (%d) ...", height)


	vm.resetGeneratorMoniter()
}

func (vm *ValidatorManager) SetLocalAsCurrentGenerator(height int32, handoverTime time.Time) {
	utils.Log.Tracef("[ValidatorManager]SetLocalAsCurrentGenerator for mine block height (%d) ...", height)


	vm.resetGeneratorMoniter()
}

func (vm *ValidatorManager) OnTimeGenerateBlock() (*chainhash.Hash, int32, error) {
	utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock...")

	// 如果连接节点太少，就不要挖矿
	if !vm.HasRemoteValidator() {
		utils.Log.Infof("[ValidatorManager]OnTimeGenerateBlock no validator connected, step to next slot")
		vm.resetGeneratorMoniter()

		return nil, 0, fmt.Errorf("no validator connected, step to next slot")
	}

	// var defaultValidator *peerpkg.Peer
	// if vm.localValidatorId.IsBootStrapNode() {
	// 	defaultValidator = vm.GetDefaultCoreValidator()
	// } else {
	// 	defaultValidator = vm.GetBootstrapValidator()
	// }
	// // 为了杜绝分叉，对于非引导节点，需要在挖矿之前，再ping一下引导节点，确保引导节点知道本节点要开始出块了
	// // 如果是引导节点，那就ping一下内置的核心节点，确保核心节点能连接到
	// if defaultValidator == nil {
	// 	utils.Log.Errorf("[ValidatorManager]OnTimeGenerateBlock not connect to default validator")
	// 	vm.myValidator.ContinueNextSlot()
	// 	vm.resetGeneratorMoniter()
	// 	return nil, 0, fmt.Errorf("not connect to default validator")
	// }

	// if !defaultValidator.IsConnected() {
	// 	err := defaultValidator.Connect()
	// 	if err != nil {
	// 		utils.Log.Errorf("[ValidatorManager]OnTimeGenerateBlock Connect default validator failed: %v", err)
	// 		vm.myValidator.ContinueNextSlot()
	// 		vm.resetGeneratorMoniter()
	// 		return nil, 0, err
	// 	}
	// }

	// nonce, _ := wire.RandomUint64()
	// ping := validatorcommand.NewMsgPing(nonce)
	// err := defaultValidator.SendCommand(ping)
	// if err != nil {
	// 	utils.Log.Errorf("[ValidatorManager]OnTimeGenerateBlock send ping to default validator failed: %v", err)
	// 	vm.myValidator.ContinueNextSlot()
	// 	vm.resetGeneratorMoniter()
	// 	return nil, 0, err
	// }
	// // 发送成功就认为是连接上的，这里最多等2S （ generator.MinerInterval ）
	// if !defaultValidator.WaitCommandSended(ping) {
	// 	utils.Log.Errorf("[ValidatorManager]OnTimeGenerateBlock send ping to default validator failed")
	// 	vm.myValidator.ContinueNextSlot()
	// 	vm.resetGeneratorMoniter()
	// 	return nil, 0, err
	// }
	utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock check completed, start to mine blokc")

	// Notify validator manager to generate new block
	hash, height, err := vm.cfg.PosMiner.OnTimeGenerateBlock()
	if err != nil {
		utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock failed: %v", err)
		// Generate block failed, it should be no tx to be mined, wait for next time
		if err.Error() == "no any new tx in mempool" || err.Error() == "no any new tx need to be mining" {

			vm.resetGeneratorMoniter()
			return nil, 0, err
		}

		// 不应该走到这里

		return nil, 0, err
	}
	utils.Log.Infof("[ValidatorManager]OnTimeGenerateBlock succeed, height %d, Hash: %s", height, hash.String())

	return hash, height, nil
}

func (vm *ValidatorManager) BroadcastCommand(command wire.Message) {
	utils.Log.Tracef("[ValidatorManager]Will broadcast command to all connected validators...")
	// localPubKey := vm.myValidator.GetValidatorId()
	// for _, validator := range vm.connectedList {
	// 	if validator.ValidatorInfo.ValidatorId == localPubKey {
	// 		continue
	// 	}
	// 	utils.Log.Tracef("[ValidatorManager]Send command <%s> to %s...", command.Command(), validator.String())
	// 	validator.SendCommand(command)
	// }
	vm.fatherPeer.QueueMessage(command, nil)
}

func (vm *ValidatorManager) resetGeneratorMoniter() {
	utils.Log.Debugf("resetGeneratorMoniter...")
	if vm.generatorTicker == nil {
		// Not start monitor
		utils.Log.Tracef("GeneratorTicker is not start or stopped.")
		return
	}

	monitorInterval := MinerInterval
	utils.Log.Debugf("local generator: Next check generator after %f seconds.", monitorInterval.Seconds())
	vm.generatorTicker.Reset(monitorInterval)
}


func (vm *ValidatorManager) GetMyValidatorId() string {
	return vm.cfg.MiningPubKey
}

// 在轮到自己出块时，reset interval
func (vm *ValidatorManager) generatorTimer() {
	vm.generatorTicker = time.NewTicker(CheckingInterval)

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
		utils.Log.Infof("not reach the tip of block yet")
		return
	}

	if !vm.isMyTurn() {
		utils.Log.Debugf("not my turn")
		return
	}
	pastMinerDuation := vm.getPastTimeFromLastMiner()
	if pastMinerDuation < MinerInterval {
		// The miner time is not past, ignore
		utils.Log.Infof("not in time")
		return
	}

	txSizeInMempool := vm.cfg.PosMiner.GetMempoolTxSize()
	if txSizeInMempool == 0 {
		utils.Log.Tracef("[ValidatorManager]Current mempool is empty")
		return
	}

	// 向fatherPeer发起ping请求，如果得到响应，就开始出块，否则就继续等
	var peer *peerpkg.Peer
	var wg sync.WaitGroup
	failed := false
	if vm.IsBootstrapNode() {
		wg.Add(1)
		peer = vm.GetDefaultCoreValidator()
	} else if vm.IsCoreNode() {
		wg.Add(1)
		peer = vm.bootstrapPeer
	} else {
		// 需要同时向引导节点和核心节点发送ping
		peer = vm.fatherPeer
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := sendPingAndWait(vm.bootstrapPeer)
			if err != nil {
				failed = true
				utils.Log.Errorf("can't get pong from %s in time, %v", peer.String(), err) 
			}
		}()
	}
	go func() {
		defer wg.Done()
		err := sendPingAndWait(peer)
		if err != nil {
			failed = true
			utils.Log.Errorf("can't get pong from %s in time, %v", peer.String(), err) 
		}
	}()
	wg.Wait()
	if failed {
		utils.Log.Errorf("some error occur when ping peer")
		return
	}
	
	// 出块后不要直接上链，而是给father节点去做进一步的审核，杜绝分叉
	vm.OnTimeGenerateBlock()
}

func sendPingAndWait(peer *peerpkg.Peer) error {
	if peer == nil {
		utils.Log.Errorf("peer is nil")
		return fmt.Errorf("peer is nil")
	}
	if !peer.Connected() {
		utils.Log.Errorf("%s not connetcted", peer.String())
		return fmt.Errorf("%s not connetcted", peer.String())
	}

	duration, err := peer.WaitForPong(2 * time.Second)
	if err != nil {
		utils.Log.Errorf("Peer %v did not respond in time: %v\n", peer.String(), err)
    	return err
	} 
    utils.Log.Debugf("Peer %v responded in %v\n", peer.String(), duration)
	return nil
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

func (vm *ValidatorManager) getLastBlockTime() time.Time {
	// 查询索引器当前轮到出块的pubkey
	return time.Now()
}

func (vm *ValidatorManager) getPastTimeFromLastMiner() time.Duration {
	return time.Since(vm.getLastBlockTime())
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
