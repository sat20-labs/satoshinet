package validatormanager

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/localvalidator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechain"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechaindb"
	"github.com/sat20-labs/satoshinet/mining/posminer/validator"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorcommand"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorrecord"
)

const (
	MAX_CONNECTED        = 8
	BackupGeneratorCount = 2

	MinValidatorsCountEachEpoch = 2  // 每一个Epoch最少的验证者数量， 如果小于该数量， 则不会生成Epoch， 不会进行挖矿
	MaxValidatorsCountEachEpoch = 32 // 每一个Epoch最多有验证者数量， 如果大于该数量， 则会选择指定的数量生成Epoch

	GeneratorMonitorInterval_UonMember   = 2 * generator.MinerInterval // 非epoch成员的Generator的监控间隔
	GeneratorMonitorInterval_EpochMember = 1 * generator.MinerInterval // epoch成员的Generator的监控间隔
	MaxExpiration                        = 3 * time.Second             // 最大的Miner过期时间

	UnexceptionInterval = 2 * generator.MinerInterval //  超过2个Miner的时间， 就认为出块异常， bootstrap node 会重启Epoch, 目前直接出块以防出块卡死
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

type Config struct {
	ChainParams *chaincfg.Params
	Dial        func(net.Addr) (net.Conn, error)
	Lookup      func(string) ([]net.IP, error)
	ValidatorId uint64
	ValidatorPubKey []byte
	BtcdDir     string
	Peers       []string

	PosMiner PosMinerInterface
}

type ValidatorManager struct {

	// ValidatorId uint64
	Cfg *Config

	myValidator *localvalidator.LocalValidator // 当前的验证者（本地验证者）
	//	PreValidatorList []*validator.Validator         // 预备验证者列表， 用于初始化的验证者列表， 主要有本地保存和种子Seed中获取的Validator地址生成
	ValidatorRecordMgr *validatorrecord.ValidatorRecordMgr // 所有的已经连接过的验证者列表
	CurrentEpoch       *epoch.Epoch                        // 当前的Epoch
	NextEpoch          *epoch.Epoch                        // 下一个正在排队的Epoch
	isEpochMember      bool                                // 当前validator是否是Epoch成员

	ConnectedList    []*validator.Validator // 所有已连接的验证者列表
	connectedListMtx sync.RWMutex

	newEpochMgr *NewEpochManager // Handle NewEpoch event

	epochMemberMgr *EpochMemberManager // EpochMember manager

	quit chan struct{}

	needHandOver bool // flag if need to handover

	moniterGeneratorTicker *time.Ticker
	lastHandOverTime       time.Time

	// NewVCStore
	vcStore         *validatechaindb.ValidateChainStore // VC Store
	validateChain   *validatechain.ValidateChain        // VC  --- validate chain
	vcSyncValidator *validator.Validator                // VC Sync Validator

	saveVCBlockMtx sync.RWMutex
}

//var validatorMgr *ValidatorManager

func New(cfg *Config) *ValidatorManager {
	utils.Log.Debugf("New ValidatorManager")
	validatorMgr := &ValidatorManager{
		// ChainParams: cfg.ChainParams,
		// Dial:        cfg.Dial,
		// lookup:      cfg.Lookup,
		// ValidatorId: cfg.ValidatorId,
		Cfg: cfg,

		//ValidatorList: make([]*validator.Validator, 0),
		ConnectedList: make([]*validator.Validator, 0),

		//CurrentEpoch: epoch.NewEpoch(),

		quit: make(chan struct{}),
	}

	vcStore, err := validatechaindb.NewVCStore(cfg.BtcdDir)
	if err != nil {
		utils.Log.Errorf("NewVCStore failed: %v", err)
		return nil
	}
	validatorMgr.vcStore = vcStore

	validatorMgr.validateChain = validatechain.NewValidateChain(vcStore)

	// For testing
	//validatorMgr.Testing()

	validatorMgr.epochMemberMgr = CreateEpochMemberManager(validatorMgr)

	localAddrs, _ := validatorMgr.getLocalAddr()
	//utils.Log.Debugf("Get local address: %s", localAddr.String())
	validatorCfg := validatorMgr.newValidatorConfig(validatorMgr.Cfg.ValidatorId, validatorMgr.Cfg.ValidatorPubKey,  nil) // No remote validator

	myValidator, err := localvalidator.NewValidator(validatorCfg, localAddrs)
	if err != nil {
		utils.Log.Errorf("New LocalValidator failed: %v", err)
		return nil
	}
	validatorMgr.myValidator = myValidator
	validatorMgr.myValidator.Start()

	// err = validatorMgr.CurrentEpoch.AddValidatorToEpoch(&validatorMgr.myValidator.ValidatorInfo)
	// if err != nil {
	// 	utils.Log.Debugf("Add local validator to epoch failed: %v", err)
	// 	return nil
	// }

	utils.Log.Debugf("New ValidatorManager succeed")
	return validatorMgr
}

func (vm *ValidatorManager) newValidatorConfig(localValidatorID uint64, localValidatorPubKey []byte,
	remoteValidatorInfo *validatorinfo.ValidatorInfo) *validator.Config {
	return &validator.Config{
		Listener:    vm,
		ChainParams: vm.Cfg.ChainParams,
		Dial:        vm.Cfg.Dial,
		Lookup:      vm.Cfg.Lookup,
		BtcdDir:     vm.Cfg.BtcdDir,

		LocalValidatorId:    localValidatorID,
		LocalValidatorPubKey: localValidatorPubKey,
		RemoteValidatorInfo: remoteValidatorInfo,
	}
}

// Start starts the validator manager. It loads saved validators peers and if the
// saved validators file not exists, it starts from dns seed. It connects to the
// validators and gets current all validators info.
func (vm *ValidatorManager) Start() {
	utils.Log.Debugf("StartValidatorManager")

	// Load saved validators peers
	vm.LoadValidatorRecordList()

	validatorCfg := vm.newValidatorConfig(vm.Cfg.ValidatorId, vm.Cfg.ValidatorPubKey, nil) // Start , remote validator info is nil (unkown validator)

	PreValidatorList := make([]*validator.Validator, 0)

	for _, record := range vm.ValidatorRecordMgr.ValidatorRecordList {

		if record == nil || record.Host == "" {
			// Invalid record
			utils.Log.Debugf("Invalid record: %v", record)
			continue
		}

		utils.Log.Debugf("Try to connect validator: %s", record.Host)

		// New a validator with addr
		//addrsList := make([]net.Addr, 0, 1)
		//addrsList = append(addrsList, addr)
		isLocalValidator := vm.isLocalValidator(record.Host)
		if isLocalValidator {
			utils.Log.Debugf("Validator is local validator")
			//validator.SetLocalValidator()
			//vm.PreValidatorList = append(vm.PreValidatorList, validator)
		} else {
			utils.Log.Debugf("Validator is remote validator")
			addr, err := vm.getAddr(record.Host)
			if err != nil {
				utils.Log.Errorf("Get addr failed: %v", err)
				continue
			}
			validator, err := validator.NewValidator(validatorCfg, addr)
			if err != nil {
				utils.Log.Errorf("New Validator failed: %v", err)
				continue
			}

			PreValidatorList = append(PreValidatorList, validator)
		}

	}

	// Select Max validator to get current all validators info
	validatorCount := len(PreValidatorList)
	connectCount := validatorCount
	if connectCount > MAX_CONNECTED {
		connectCount = MAX_CONNECTED
	}

	for i := 0; i < connectCount; i++ {
		index := i
		if connectCount < validatorCount {
			index = rand.Intn(validatorCount)
		}
		validator := PreValidatorList[index]

		// Connect to the validator
		err := validator.Connect()
		if err != nil {
			utils.Log.Errorf("Connect validator failed: %v", err)
			continue
		}

		// The validator is connected
		//vm.ConnectedList = append(vm.ConnectedList, validator)
		vm.AddActivieValidator(validator)
		//validator.RequestAllValidatorsInfo()
	}

	vm.validateChain.Start()

	//go vm.syncValidatorsHandler() // sync validators list

	go vm.syncValidateChainHandler() // sync validate chain state

	go vm.syncEpochHandler() // sync current epoch info

	//go vm.getGeneratorHandler()

	//go vm.getCheckEpochHandler()

	go vm.monitorGeneratorHandOverHandler()

	go vm.checkValidatorConnectedHandler()

	go vm.observeHandler()
}

func (vm *ValidatorManager) LoadValidatorRecordList() *validatorrecord.ValidatorRecordMgr {
	vm.ValidatorRecordMgr = validatorrecord.LoadValidatorRecordList(vm.Cfg.BtcdDir)
	if vm.ValidatorRecordMgr.ValidatorRecordList == nil || len(vm.ValidatorRecordMgr.ValidatorRecordList) == 0 {
		// if the saved validators file not exists, start from dns seed
		hostList, _ := vm.getSeedHostList(vm.Cfg.ChainParams)
		hostList = append(hostList, vm.Cfg.Peers...)
		for _, host := range hostList {
			utils.Log.Debugf("Try to connect validator: %s", hostList)
			vm.ValidatorRecordMgr.UpdateValidatorRecord(0, host)
		}
	}

	return vm.ValidatorRecordMgr
}

func (vm *ValidatorManager) Stop() {
	utils.Log.Debugf("ValidatorManager Stop")

	close(vm.quit)
}

func (vm *ValidatorManager) isLocalValidator(host string) bool {
	addrs := vm.myValidator.GetValidatorAddrsList()
	if len(addrs) == 0 {
		return false
	}

	// requestAddr := validator.GetValidatorAddr()
	// if requestAddr == nil {
	// 	return false
	// }

	for _, addr := range addrs {
		hostAddr, _, _ := net.SplitHostPort(addr.String())

		if hostAddr == host {
			return true
		}
	}
	return false
}

func (vm *ValidatorManager) isLocalValidatorById(validatorId uint64) bool {
	if vm.myValidator.ValidatorInfo.ValidatorId == validatorId {
		return true
	}

	return false
}

// Current validator list is updated
func (vm *ValidatorManager) OnValidatorListUpdated(validatorList []validatorinfo.ValidatorInfo, remoteAddr net.Addr) {
	utils.Log.Debugf("ValidatorList Update from [%s]", remoteAddr.String())
	utils.Log.Debugf("********************************* New Validator List ********************************")
	utils.Log.Debugf("Current Validator Count: %d", len(validatorList))
	for _, validatorInfo := range validatorList {
		utils.Log.Debugf("validator ID: %d", validatorInfo.ValidatorId)
		utils.Log.Debugf("validator Public: %x", validatorInfo.PublicKey[:])
		utils.Log.Debugf("validator Host: %s", validatorInfo.Host)
		utils.Log.Debugf("validator CreateTime: %s", validatorInfo.CreateTime.Format("2006-01-02 15:04:05"))
		utils.Log.Debugf("------------------------------------------------")
	}
	utils.Log.Debugf("*********************************        End        ********************************")

	for _, validatorInfo := range validatorList {
		// Find the validator
		validatorRemote := vm.FindRemoteValidator(validatorInfo.ValidatorId)
		if validatorRemote != nil {
			// Found
			// Update the validator
			validatorRemote.SyncVaildatorInfo(&validatorInfo)
		} else {
			// Not found
			// Is local validator
			if vm.isLocalValidatorById(validatorInfo.ValidatorId) {
				// Local validator
				continue
			}

			if validatorInfo.ValidatorId == 0 || validatorInfo.Host == "" {
				// Invalid validator
				continue
			}

			// Try to connect the validator
			// Add the validator
			validatorCfg := vm.newValidatorConfig(vm.Cfg.ValidatorId, vm.Cfg.ValidatorPubKey, &validatorInfo) // vm.ValidatorId is Local validator, validatorId is Remote validator when new validator connected

			addr, err := vm.getAddr(validatorInfo.Host)
			if err != nil {
				continue
			}
			validatorNew, err := validator.NewValidator(validatorCfg, addr)
			if err != nil {
				utils.Log.Errorf("New Validator failed: %v", err)
				continue
			}

			// Connect to the validator
			err = validatorNew.Connect()
			if err != nil {
				utils.Log.Errorf("Connect validator failed: %v", err)
				continue
			}

			// The validator is connected
			//vm.ConnectedList = append(vm.ConnectedList, validator)
			vm.AddActivieValidator(validatorNew)
			//validator.RequestAllValidatorsInfo()

		}
	}
}

func (vm *ValidatorManager) OnValidatorInfoUpdated(validatorInfo *validatorinfo.ValidatorInfo, remoteAddr net.Addr) {
	// if vm.CurrentEpoch.IsExist(validatorInfo.ValidatorId) == false {
	// 	if vm.CurrentEpoch.IsValidEpochValidator(validatorInfo) == true {
	// 		vm.CurrentEpoch.AddValidatorToEpoch(validatorInfo)
	// 	}
	// }
}

// Get current validator list in record this peer
func (vm *ValidatorManager) GetValidatorList(validatorID uint64) []*validatorinfo.ValidatorInfo {

	utils.Log.Debugf("GetValidatorList from validator [%d]", validatorID)

	validatorList := vm.getValidatorList()

	utils.Log.Debugf("********************************* Get Validator Summary From [%d] ********************************", validatorID)
	showValidatorList(validatorList)
	utils.Log.Debugf("*********************************        End        ********************************")

	return validatorList
}

func (vm *ValidatorManager) getValidatorList() []*validatorinfo.ValidatorInfo {
	validatorList := make([]*validatorinfo.ValidatorInfo, 0)

	// Add local validator
	localValidatorItem := validatorinfo.ValidatorInfo{
		ValidatorId:     vm.myValidator.ValidatorInfo.ValidatorId,
		Host:            vm.myValidator.ValidatorInfo.Host,
		PublicKey:       vm.myValidator.ValidatorInfo.PublicKey,
		CreateTime:      vm.myValidator.ValidatorInfo.CreateTime,
		ActivitionCount: vm.myValidator.ValidatorInfo.ActivitionCount,
		GeneratorCount:  vm.myValidator.ValidatorInfo.GeneratorCount,
		DiscountCount:   vm.myValidator.ValidatorInfo.DiscountCount,
		FaultCount:      vm.myValidator.ValidatorInfo.FaultCount,
		ValidatorScore:  vm.myValidator.ValidatorInfo.ValidatorScore,
	}
	validatorList = append(validatorList, &localValidatorItem)

	// Add all remote validators
	for _, validator := range vm.ConnectedList {
		if validator == nil {
			continue
		}
		if validator.IsValidInfo() == false {
			// filter invalid validator
			utils.Log.Errorf("Invalid validator: %s", validator.String())
			continue
		}
		validatorItem := validatorinfo.ValidatorInfo{
			ValidatorId:     validator.ValidatorInfo.ValidatorId,
			Host:            validator.ValidatorInfo.Host,
			PublicKey:       validator.ValidatorInfo.PublicKey,
			CreateTime:      validator.ValidatorInfo.CreateTime,
			ActivitionCount: validator.ValidatorInfo.ActivitionCount,
			GeneratorCount:  validator.ValidatorInfo.GeneratorCount,
			DiscountCount:   validator.ValidatorInfo.DiscountCount,
			FaultCount:      validator.ValidatorInfo.FaultCount,
			ValidatorScore:  validator.ValidatorInfo.ValidatorScore,
		}
		validatorList = append(validatorList, &validatorItem)
	}

	return validatorList
}

func (vm *ValidatorManager) FindRemoteValidator(validatorID uint64) *validator.Validator {
	// Find from all remote validators
	for _, validator := range vm.ConnectedList {
		if validator.ValidatorInfo.ValidatorId == validatorID {
			return validator
		}
	}
	return nil
}
func (vm *ValidatorManager) SyncValidators() {
	utils.Log.Debugf("[SyncValidators]Will sync validators...")
	for _, validator := range vm.ConnectedList {
		utils.Log.Debugf("[SyncValidators]Sync form %s...", validator.String())
		validator.SyncAllValidators()
	}
}

func (vm *ValidatorManager) SyncEpoch() {
	utils.Log.Debugf("[SyncEpoch]Will GetEpoch from connected validators...")
	for _, validator := range vm.ConnectedList {
		utils.Log.Debugf("[SyncEpoch]Get epoch form %s...", validator.String())
		validator.GetEpoch()
	}
}

func (vm *ValidatorManager) SyncGenerator() {
	utils.Log.Debugf("[SyncGenerator]Will GetGenerator from connected validators...")
	for _, validator := range vm.ConnectedList {
		utils.Log.Debugf("[SyncGenerator]GetGenerator form %s...", validator.String())
		validator.GetGenerator()
	}
}

// OnEpochSynced received Epoch message, it's response with reqepoch message
func (vm *ValidatorManager) OnEpochSynced(currentEpoch *epoch.Epoch, nextEpoch *epoch.Epoch, remoteAddr net.Addr) {
	utils.Log.Debugf("OnEpochSynced from validator [%s]", remoteAddr.String())
	// showEpoch("OnEpochSynced:Current Epoch", currentEpoch)
	// showEpoch("OnEpochSynced:Next Epoch", nextEpoch)
	// for _, validatorInfo := range epochList {
	// 	if vm.CurrentEpoch.IsExist(validatorInfo.ValidatorId) == false {
	// 		utils.Log.Debugf("Will add validator [%d] to epoch", validatorInfo.ValidatorId)
	// 		if vm.CurrentEpoch.IsValidEpochValidator(&validatorInfo) == true {
	// 			vm.CurrentEpoch.AddValidatorToEpoch(&validatorInfo)
	// 			utils.Log.Debugf("Validator [%d] has been added to epoch", validatorInfo.ValidatorId)
	// 		}
	// 	}
	// }
	isChanged := false
	if currentEpoch != nil {
		// Recevice valid epoch
		if vm.CurrentEpoch == nil {
			// Local is no valid epoch, update local epoch
			//vm.CurrentEpoch = currentEpoch
			//vm.epochMemberMgr.UpdateCurrentEpoch(vm.CurrentEpoch)
			vm.setCurrentEpoch(currentEpoch)

			vm.NextEpoch = nextEpoch
			isChanged = true
		} else {
			if vm.CurrentEpoch.EpochIndex < currentEpoch.EpochIndex {
				// The renote epoch is updated to newest, update local epoch
				//vm.CurrentEpoch = currentEpoch
				//vm.epochMemberMgr.UpdateCurrentEpoch(vm.CurrentEpoch)
				vm.setCurrentEpoch(currentEpoch)

				vm.NextEpoch = nextEpoch
				isChanged = true
			} else if vm.CurrentEpoch.EpochIndex == currentEpoch.EpochIndex {
				if nextEpoch != nil && vm.NextEpoch == nil {
					// The remote next epoch is updated, update local next epoch
					vm.NextEpoch = nextEpoch
					isChanged = true
				}
			}
		}
	}

	if isChanged {

		// New epoch is set, clear handover flag
		vm.needHandOver = false

		// Local epoch is updated, show new epoch
		utils.Log.Debug("The local epoch is synced")
		showEpoch("New current Epoch", vm.CurrentEpoch)
		showEpoch("New next Epoch", vm.NextEpoch)

		// if vm.CurrentEpoch.IsExist(vm.Cfg.ValidatorId) {
		// 	// Local validator is in current epoch
		// 	if vm.CurrentEpoch.Generator == nil {
		// 		// Current Epoch is Not Start, will Start it
		// 	}
		// 	height := vm.Cfg.PosMiner.GetBlockHeight()
		// 	vm.SetLocalAsNextGenerator(height, time.Now())
		// }
	}

}

// Get current epoch info in record this peer
func (vm *ValidatorManager) GetLocalEpoch(validatorID uint64) (*epoch.Epoch, *epoch.Epoch, error) {
	utils.Log.Debugf("GetLocalEpoch from validator [%d]", validatorID)
	// if vm.CurrentEpoch != nil {
	// 	return vm.CurrentEpoch.GetValidatorList()
	// }
	return vm.CurrentEpoch, vm.NextEpoch, nil
}

// Current generator is updated
func (vm *ValidatorManager) OnGeneratorUpdated(newGenerator *generator.Generator, validatorID uint64) {
	utils.Log.Debugf("OnGeneratorUpdated from validator [%d]", validatorID)

	if newGenerator == nil {
		utils.Log.Debugf("OnGeneratorUpdated: Invalid generator (nil generator)")
		return
	}

	if newGenerator.GeneratorId == generator.NoGeneratorId {
		utils.Log.Debugf("OnGeneratorUpdated: No generator in validator [%d]", validatorID)
		return
	}

	if newGenerator.GeneratorId == vm.Cfg.ValidatorId {
		utils.Log.Debugf("OnGeneratorUpdated: Current generator is local generator, ignore check")
		return
	}

	if vm.IsValidGenerator(newGenerator) == false {
		utils.Log.Debugf("OnGeneratorUpdated: Invalid generator in validator [%d]", validatorID)
		return
	}

	showGeneratorInfo(newGenerator)

	if vm.CurrentEpoch != nil {
		curGenerator := vm.CurrentEpoch.GetGenerator()
		needUpdate := false
		if curGenerator == nil {
			needUpdate = true
		} else if curGenerator.GeneratorId != newGenerator.GeneratorId {
			needUpdate = true
		}
		if needUpdate == true {
			utils.Log.Debugf("OnGeneratorUpdated: Will update current generator with received generator")
			vm.CurrentEpoch.UpdateGenerator(newGenerator)
		}
	}
}

// Current generator is updated
func (vm *ValidatorManager) OnGeneratorHandOver(handOverGenerator *generator.GeneratorHandOver, remoteAddr net.Addr) {
	utils.Log.Debugf("OnGeneratorHandOver from validator [%s]", remoteAddr.String())

	switch handOverGenerator.HandOverType {
	case generator.HandOverTypeByEpochOrder:
		// check the hand over generator is valid. it should be signed by current generator

	case generator.HandOverTypeByVote:
		// It should be check by the result of vote

	}

	// Record the hand over generator info, it should be math with generator notified by new generator
	if vm.isLocalValidatorById(handOverGenerator.GeneratorId) {
		if vm.CurrentEpoch == nil {
			utils.Log.Errorf("OnGeneratorHandOver failed: Current epoch is nil")
			return
		}

		// The next generator is local validator
		vm.SetLocalAsNextGenerator(handOverGenerator.Height, time.Unix(handOverGenerator.Timestamp, 0))

		if vm.CurrentEpoch.IsLastGenerator() {
			// Broadcast  for New Epoch
			// Current epoch is last, request New Epoch
			nextEpochIndex := vm.getCurrentEpochIndex() + 1
			vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochHandOver)

		}
	}
}

func (vm *ValidatorManager) IsValidGenerator(generator *generator.Generator) bool {
	if generator == nil {
		return false
	}

	if generator.GeneratorId == 0 {
		return false
	}

	// Check the block height of current generator is next height
	nextHeight := vm.Cfg.PosMiner.GetBlockHeight() + 1
	if nextHeight != generator.Height {
		utils.Log.Debugf("OnGeneratorUpdated: Invalid generator height [%d], the next generator height is [%d]", generator.Height, nextHeight)
		//return false  // For test, not return false
	}

	validatorConnected := vm.FindRemoteValidator(generator.GeneratorId)
	if validatorConnected == nil {
		utils.Log.Debugf("OnGeneratorUpdated: Cannot find validator [%d] in connected validators, ignore it.", generator.GeneratorId)
		return false
	}

	if generator.Token == "" {
		utils.Log.Debugf("OnGeneratorUpdated: Invalid generator token, ignore it.")
		return false
	}

	// signatureBytes, err := base64.StdEncoding.DecodeString(generator.Token)
	// if err != nil {
	// 	utils.Log.Debugf("OnGeneratorUpdated: Invalid generator token, ignore it.")
	// 	return false
	// }

	// tokenData := generator.GetTokenData()

	// publicKey, err := secp256k1.ParsePubKey(validatorConnected.ValidatorInfo.PublicKey[:])

	// // 解析签名
	// // signature, err := btcec.ParseDERSignature(signatureBytes)
	// signature, err := ecdsa.ParseDERSignature(signatureBytes)
	// if err != nil {
	// 	utils.Log.Debugf("Failed to parse signature: %v", err)
	// 	return false
	// }

	// 使用公钥验证签名
	valid := generator.VerifyToken(validatorConnected.ValidatorInfo.PublicKey[:])
	if valid {
		utils.Log.Debugf("Signature is valid.")
		generator.Validatorinfo = &validatorConnected.ValidatorInfo
		return true
	} else {
		utils.Log.Debugf("Signature is invalid.")
		return false
	}

}

// Get current generator info in record this peer
func (vm *ValidatorManager) GetGenerator() *generator.Generator {
	if vm.CurrentEpoch == nil {
		return nil
	}
	curGenerator := vm.CurrentEpoch.GetGenerator()
	return curGenerator
}

func (vm *ValidatorManager) GetCurrentBlockHeight() int32 {
	return vm.Cfg.PosMiner.GetBlockHeight()
}

func (vm *ValidatorManager) GetCurrentEpoch() *epoch.Epoch {
	utils.Log.Debugf("GetCurrentEpoch:")

	return vm.CurrentEpoch
}

func (vm *ValidatorManager) GetNextEpoch() *epoch.Epoch {
	utils.Log.Debugf("GetCurrentEpoch:")

	return vm.NextEpoch
}

func (vm *ValidatorManager) GetLocalValidatorInfo() *validatorinfo.ValidatorInfo {

	return &vm.myValidator.ValidatorInfo
}

func (vm *ValidatorManager) OnConfirmEpoch(epoch *epoch.Epoch, remoteAddr net.Addr) {
	if remoteAddr != nil {
		utils.Log.Debugf("OnConfirmEpoch from validator [%s]", remoteAddr.String())
	} else {
		utils.Log.Debugf("OnConfirmEpoch from local validator")
	}

	if epoch == nil {
		utils.Log.Debugf("OnConfirmEpoch an empty epoch.")
		return
	}

	// Check the epoch is valid
	if vm.CurrentEpoch != nil {
		if epoch.EpochIndex <= vm.CurrentEpoch.EpochIndex {
			// The epoch is invalid
			utils.Log.Debugf("OnConfirmEpoch: The next epoch is invalid, the epochIndex <%d> should be larger than current epoch <%d>", epoch.EpochIndex, vm.CurrentEpoch.EpochIndex)
			return
		}
	}

	// The epoch will be saved in next epoch, And wait current epoch to completed, and continue miner with next epoch
	vm.NextEpoch = epoch
}

func (vm *ValidatorManager) OnNewValidatorPeerConnected(netAddr net.Addr, validatorInfo *validatorinfo.ValidatorInfo) {
	// New validator peer is connected
	utils.Log.Debugf("[ValidatorManager]New validator peer connected: %s", netAddr.String())

	// get validator in validator list

	// Get host from netAddr
	// addrs, err := net.LookupHost(netAddr.String())
	// if err != nil {
	// 	utils.Log.Errorf("LookupHost failed: %v", err)
	// 	return
	// }
	// if len(addrs) == 0 {
	// 	utils.Log.Errorf("Get host from netAddr failed: %s", netAddr.String())
	// 	return
	// }

	peerHost := validatorinfo.GetAddrHost(netAddr)
	validatorPeer := vm.LookupValidator(peerHost)
	if validatorPeer != nil {
		// The validator is already connected, will try to check connection again
		utils.Log.Debugf("[ValidatorManager]New validator has added in connectedlist: %s", netAddr.String())
		validatorPeer.Connect()
		return
	}

	// Will new an validator
	port := vm.GetValidatorPort()
	addrPeer := &net.TCPAddr{
		IP:   peerHost,
		Port: port,
	}

	validatorCfg := vm.newValidatorConfig(vm.Cfg.ValidatorId, vm.Cfg.ValidatorPubKey, validatorInfo) // vm.ValidatorId is Local validator, validatorId is Remote validator when new validator connected
	peerValidator, err := validator.NewValidator(validatorCfg, addrPeer)
	if err != nil {
		utils.Log.Errorf("New Validator failed: %v", err)
		return
	}
	// Connect to the validator
	err = peerValidator.Connect()
	if err != nil {
		utils.Log.Errorf("Connect validator failed: %v", err)
		return
	}

	//vm.ConnectedList = append(vm.ConnectedList, peerValidator)
	vm.AddActivieValidator(peerValidator)

	utils.Log.Debugf("[ValidatorManager]New validator added to connectedlist: %s", netAddr.String())
}

func (vm *ValidatorManager) getRemoteValidator(remoteAddr net.Addr) *validator.Validator {
	peerHost := validatorinfo.GetAddrHost(remoteAddr)
	validatorPeer := vm.LookupValidator(peerHost)
	return validatorPeer
}

func (vm *ValidatorManager) OnValidatorPeerDisconnected(validator *validator.Validator) {
	// Remote validator peer disconnected, it will be notify by remote validator when it cannot connect or sent any command
	utils.Log.Debugf("[ValidatorManager]validator peer is disconnected: %s", validator.String())
	vm.removeValidator(validator)

	if validator == nil {
		return
	}
	if vm.CurrentEpoch != nil {
		if vm.CurrentEpoch.IsExist(validator.ValidatorInfo.ValidatorId) {
			utils.Log.Debugf("[ValidatorManager]The disconnected validator peer is epoch member: %s", validator.String())
			// Notify EpochMemberManager for a validator disconnect
			vm.epochMemberMgr.OnValidatorDisconnected(validator.ValidatorInfo.ValidatorId)
			return
		}
	}
	utils.Log.Debugf("[ValidatorManager]The disconnected validator peer isnot epoch member, nothing to do: %s", validator.String())
}

func (vm *ValidatorManager) OnValidatorPeerInactive(netAddr net.Addr) {
	// Remote validator peer is inactive, it will be notify by local validator when it is long time to not received any command
	utils.Log.Debugf("[ValidatorManager]validator peer in inactive: %s", netAddr.String())
}

// Get current generator info in record this peer
func (vm *ValidatorManager) GetValidatorPort() int {

	port, err := strconv.Atoi(vm.Cfg.ChainParams.DefaultPort)
	if err != nil {
		utils.Log.Errorf("GetValidatorPort failed: %v", err)
		return 9525
	}
	port -= 1

	return port
}

func (vm *ValidatorManager) AddActivieValidator(validator *validator.Validator) error {
	if validator.IsConnected() == false {
		return fmt.Errorf("validator is not connected")
	}

	vm.connectedListMtx.Lock()
	defer vm.connectedListMtx.Unlock()

	addr := validator.GetValidatorAddr()

	peerHost := validatorinfo.GetAddrHost(addr)
	validatorPeer := vm.LookupValidator(peerHost)
	if validatorPeer != nil {
		// The validator is already in connectedlist
		validator.Stop()
		return fmt.Errorf("validator alreadly in connected list")
	}
	vm.ConnectedList = append(vm.ConnectedList, validator)

	// update validator record
	vm.ValidatorRecordMgr.UpdateValidatorRecord(validator.ValidatorInfo.ValidatorId, peerHost.String())

	//sortsValidatorList(vm.ConnectedList)

	// err := vm.CurrentEpoch.AddValidatorToEpoch(&validator.ValidatorInfo)
	// if err != nil {
	// 	utils.Log.Debugf("Add validator to epoch failed: %v", err)
	// }

	return nil
}

func (vm *ValidatorManager) LookupValidator(host net.IP) *validator.Validator {
	for _, validator := range vm.ConnectedList {
		if validator.IsValidatorAddr(host) {
			return validator
		}
	}
	return nil
}

func (vm *ValidatorManager) removeValidator(validator *validator.Validator) {
	for index, validatorItem := range vm.ConnectedList {
		if validatorItem == validator {

			// if vm.CurrentEpoch.IsExist(validatorItem.ValidatorInfo.ValidatorId) == true {
			// 	// If the validator is in epoch, remove it
			// 	err := vm.CurrentEpoch.RemoveValidatorFromEpoch(validatorItem.ValidatorInfo.ValidatorId)
			// 	if err != nil {
			// 		utils.Log.Debugf("Remove validator from epoch failed: %v", err)
			// 	}
			// }

			if index == len(vm.ConnectedList)-1 {
				vm.ConnectedList = vm.ConnectedList[:index]
				return
			} else if index == 0 {
				vm.ConnectedList = vm.ConnectedList[1:]
				return
			}
			vm.ConnectedList = append(vm.ConnectedList[:index], vm.ConnectedList[index+1:]...)
			return
		}

	}
}

func sortsValidatorList(validatorList []*validatorinfo.ValidatorInfo) {

	slices.SortFunc(validatorList, func(a, b *validatorinfo.ValidatorInfo) int {
		if a.ValidatorId > b.ValidatorId {
			return 1
		} else {
			return -1
		}
	})
}

func getValidatorPos(validators []*validatorinfo.ValidatorInfo, lastEpochMemberId uint64) int {

	if lastEpochMemberId <= 0 {
		return -1
	}

	for index, validator := range validators {
		if validator.ValidatorId == lastEpochMemberId {
			return index
		} else if validator.ValidatorId > lastEpochMemberId {
			// The lastEpochMemberId is not in validators
			return index - 1
		}
	}

	// The lastEpochMemberId is not in validators and the lastEpochMemberId is the last validator
	return -1
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

	utils.Log.Debugf("[ValidatorManager]observeHandler done.")
}

func (vm *ValidatorManager) showCurrentStats() {
	// validatorList := vm.getValidatorList()

	// for test
	//vm.ReqNewEpoch(vm.Cfg.ValidatorId, 10, 1)

	utils.Log.Debugf("********************************* Observe Validators Summary ********************************")
	// showValidatorList(validatorList)
	for _, validator := range vm.ConnectedList {
		if validator != nil {
			validator.LogCurrentStats()
			utils.Log.Debugf("----------------------------------------------------------------")
		}
	}

	utils.Log.Debugf("*********************************        End        ********************************")

	showEpoch("Observe Current Epoch", vm.CurrentEpoch)
	showEpoch("Observe Next Epoch", vm.NextEpoch)
}

func showValidatorList(validatorList []*validatorinfo.ValidatorInfo) {
	utils.Log.Debugf("Current Validator Count: %d", len(validatorList))
	for _, validatorInfo := range validatorList {
		utils.Log.Debugf("validator ID: %d", validatorInfo.ValidatorId)
		utils.Log.Debugf("validator Public: %x", validatorInfo.PublicKey[:])
		utils.Log.Debugf("validator Host: %s", validatorInfo.Host)
		utils.Log.Debugf("validator CreateTime: %s", validatorInfo.CreateTime.Format("2006-01-02 15:04:05"))
		utils.Log.Debugf("------------------------------------------------")
	}
}

func showEpoch(title string, epoch *epoch.Epoch) {
	utils.Log.Debugf("********************************* %s Summary ********************************", title)
	if epoch == nil {
		utils.Log.Debugf("Invalid epoch")
	} else {
		utils.Log.Debugf("EpochIndex: %d", epoch.EpochIndex)
		utils.Log.Debugf("CreateHeight: %d", epoch.CreateHeight)
		utils.Log.Debugf("CreateTime: %s", epoch.CreateTime.Format("2006-01-02 15:04:05"))
		utils.Log.Debugf("EpochIndex: %d", epoch.EpochIndex)
		utils.Log.Debugf("Validator Count in Epoch: %d", len(epoch.ItemList))
		for _, epochItem := range epoch.ItemList {
			utils.Log.Debugf("validator ID: %d", epochItem.ValidatorId)
			utils.Log.Debugf("validator Public: %x", epochItem.PublicKey[:])
			utils.Log.Debugf("validator Host: %s", epochItem.Host)
			utils.Log.Debugf("validator Index: %d", epochItem.Index)
			utils.Log.Debugf("------------------------------------------------")
		}

		utils.Log.Debugf("Epoch generator: ")

		showGeneratorInfo(epoch.GetGenerator())
		//Generator     *generator.Generator // 当前Generator
		utils.Log.Debugf("CurGeneratorPos: %d", epoch.CurGeneratorPos)
		utils.Log.Debugf("LastChangeTime: %s", epoch.LastChangeTime.Format("2006-01-02 15:04:05"))
		utils.Log.Debugf("VCBlockHeight: %d", epoch.VCBlockHeight)
		if epoch.VCBlockHash == nil {
			utils.Log.Debugf("VCBlockHash: nil")
		} else {
			utils.Log.Debugf("VCBlockHash: %s", epoch.VCBlockHash.String())
		}

	}
	utils.Log.Debugf("*********************************        End        ********************************")
}
func showGeneratorInfo(generator *generator.Generator) {
	if generator == nil {
		utils.Log.Debugf("	No generator")
	} else {
		utils.Log.Debugf("	Generator ID: %d", generator.GeneratorId)
		if generator.Validatorinfo != nil {
			utils.Log.Debugf("	Generator Public: %x", generator.Validatorinfo.PublicKey[:])
			utils.Log.Debugf("	Generator Host: %s", generator.Validatorinfo.Host)
			utils.Log.Debugf("	Generator ConnectTime: %s", generator.Validatorinfo.CreateTime.Format("2006-01-02 15:04:05"))
		} else {
			utils.Log.Debugf("	Invalid validator info for Generator.")
		}
		utils.Log.Debugf("	Generator TimeStamp: %s", time.Unix(generator.Timestamp, 0).Format("2006-01-02 15:04:05"))
		utils.Log.Debugf("	Generator Token: %s", generator.Token)
		utils.Log.Debugf("	Generator Block Height: %d", generator.Height)
		utils.Log.Debugf("    Generator Miner time: %s", generator.MinerTime.Format("2006-01-02 15:04:05"))

	}
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
		utils.Log.Debugf("[ValidatorManager]Waiting next timer for syncing validator list...")
		select {
		case <-syncTicker.C:
			vm.SyncValidators()
		case <-vm.quit:
			break exit
		}
	}

	utils.Log.Debugf("[ValidatorManager]syncValidatorsHandler done.")
}

// syncEpochHandler for sync validator list from remote peer on a timer
func (vm *ValidatorManager) syncEpochHandler() {

	// Sync Epoch first
	vm.SyncEpoch()

	syncInterval := time.Second * 30
	syncTicker := time.NewTicker(syncInterval)
	defer syncTicker.Stop()

exit:
	for {
		utils.Log.Debugf("[ValidatorManager]Waiting next timer for syncing epoch list...")
		select {
		case <-syncTicker.C:
			vm.SyncEpoch()
		case <-vm.quit:
			break exit
		}
	}

	utils.Log.Debugf("[ValidatorManager]syncEpochHandler done.")
}

func (vm *ValidatorManager) getGeneratorHandler() {
	utils.Log.Debugf("[ValidatorManager]getGeneratorHandler ...")

	exitGeneraterHandler := make(chan struct{})
	// Sync current generator from all connected validators
	vm.SyncGenerator()
	// 计算从现在到目标时间的间隔
	duration := time.Second * 10
	time.AfterFunc(duration, func() {
		vm.CheckGenerator()
		exitGeneraterHandler <- struct{}{}
	})

	// 这里阻塞主 goroutine 等待任务执行（可根据需要改为其他逻辑）
	select {
	case exitGeneraterHandler <- struct{}{}:
		utils.Log.Debugf("[ValidatorManager]getGeneratorHandler done .")
		return
	case <-vm.quit:
		return
	}
}

func (vm *ValidatorManager) CheckGenerator() {
	utils.Log.Debugf("[ValidatorManager]CheckGenerator...")
	curGenerator := vm.GetGenerator()
	if curGenerator != nil {
		utils.Log.Debugf("[ValidatorManager]Has a generator, Nothing to do...")
		showGeneratorInfo(curGenerator)
		return
	} else {
		utils.Log.Debugf("Generator is nil, Will generate a new generator")
		currentEpoch := vm.GetCurrentEpoch()
		if currentEpoch == nil || len(currentEpoch.ItemList) == 0 {
			// No any validator in epoch, do nothing
			utils.Log.Debugf("[ValidatorManager]No any validator in epoch, Nothing to do...")
			return
		}

		epochList := currentEpoch.ItemList

		testBecomeGenerate := false
		if vm.Cfg.ValidatorId == 10000020 {
			testBecomeGenerate = true
		}

		if testBecomeGenerate == true || (len(epochList) == 1 && epochList[0].ValidatorId == vm.Cfg.ValidatorId) {
			// Only local validator in epoch, become local validator a generator
			utils.Log.Debugf("[ValidatorManager]Only local validator in epoch, become local validator a generator...")
			height := vm.Cfg.PosMiner.GetBlockHeight()
			vm.SetLocalAsNextGenerator(height, time.Now())
			return
		}

		utils.Log.Debugf("[ValidatorManager]Will Vote a new generator...")
		// Vote a new generator
		votedValidator := vm.CurrentEpoch.VoteGenerator()
		if votedValidator == nil {
			// It should be empty in epoch list
			return
		}

		// Broadcast Vote result to epoch list
	}
}

func (vm *ValidatorManager) SetLocalAsNextGenerator(height int32, handoverTime time.Time) {
	utils.Log.Debugf("[ValidatorManager]SetLocalAsNextGenerator for mine block height (%d) ...", height)

	showEpoch("Current Epoch before SetLocalAsNextGenerator", vm.CurrentEpoch)
	curGenerator := vm.CurrentEpoch.GetGenerator()
	if curGenerator != nil && curGenerator.GeneratorId == vm.Cfg.ValidatorId {
		utils.Log.Debugf("Current generator is local generator, check my generator is start...")
		myGenerator := vm.myValidator.GetMyGenerator()
		if myGenerator != nil {
			if myGenerator.Height == curGenerator.Height {
				utils.Log.Debugf("My generator is ready for mine %d, ignore check", curGenerator.Height)
				return
			}

			// My generator is old generator, clear it
			vm.myValidator.ClearMyGenerator()
		}
		utils.Log.Debugf("Current generator is local generator, but my generator is not Start")
		vm.myValidator.BecomeGenerator(curGenerator.Height, time.Unix(curGenerator.Timestamp, 0))
		myGenerator = vm.myValidator.GetMyGenerator()

		vm.resetGeneratorMoniter()

		showGeneratorInfo(myGenerator)
		return
	}

	vm.myValidator.BecomeGenerator(height, handoverTime)
	myGenerator := vm.myValidator.GetMyGenerator()

	showGeneratorInfo(myGenerator)

	err := vm.CurrentEpoch.ToNextGenerator(myGenerator)
	if err != nil {
		utils.Log.Debugf("SetLocalAsNextGenerator: ToNextGenerator failed: %v", err)
		return
	}

	// Save the change to Validatechain, and then broadcast to other validators
	vcBlock := validatechain.NewVCBlock()
	curState := vm.validateChain.GetCurrentState()

	vcBlock.Header.Height = curState.LatestHeight + 1 // height + 1
	vcBlock.Header.PrevHash = curState.LatestHash     // prev hash is current state hash
	vcBlock.Header.DataType = validatechain.DataType_UpdateEpoch

	dataBlock := &validatechain.DataUpdateEpoch{
		UpdatedId:     vm.myValidator.ValidatorInfo.ValidatorId,
		PublicKey:     vm.myValidator.ValidatorInfo.PublicKey,
		EpochIndex:    vm.CurrentEpoch.EpochIndex,
		CreateTime:    vm.CurrentEpoch.CreateTime.Unix(),
		Reason:        validatechain.UpdateEpochReason_GeneratorHandOver,
		EpochItemList: make([]epoch.EpochItem, 0),
		GeneratorPos:  uint32(vm.CurrentEpoch.GetCurGeneratorPos()),
		Generator:     vm.CurrentEpoch.Generator,
	}

	for _, item := range vm.CurrentEpoch.ItemList {
		dataBlock.EpochItemList = append(dataBlock.EpochItemList, *item)
	}

	vcBlock.Data = dataBlock

	err = vm.SaveVCBlock(vcBlock)
	if err != nil {
		return
	}

	vm.BroadcastVCBlock(validatorcommand.BlockType_VCBlock, &vcBlock.Header.Hash)

	// Record the change to current epoch
	vm.CurrentEpoch.LastChangeTime = time.Unix(vcBlock.Header.CreateTime, 0) // vcBlock.Header.CreateTime
	vm.CurrentEpoch.VCBlockHeight = vcBlock.Header.Height
	vm.CurrentEpoch.VCBlockHash = &vcBlock.Header.Hash
	// Broadcast for epoch changed
	updateEpochCmd := validatorcommand.NewMsgUpdateEpoch(vm.CurrentEpoch)
	vm.BroadcastCommand(updateEpochCmd)

	vm.resetGeneratorMoniter()
}

func (vm *ValidatorManager) SetLocalAsCurrentGenerator(height int32, handoverTime time.Time) {
	utils.Log.Debugf("[ValidatorManager]SetLocalAsCurrentGenerator for mine block height (%d) ...", height)

	showEpoch("Current Epoch before SetLocalAsCurrentGenerator", vm.CurrentEpoch)
	vm.myValidator.BecomeGenerator(height, handoverTime)
	myGenerator := vm.myValidator.GetMyGenerator()

	showGeneratorInfo(myGenerator)

	err := vm.CurrentEpoch.UpdateCurrentGenerator(myGenerator)
	if err != nil {
		utils.Log.Debugf("SetLocalAsCurrentGenerator: ToNextGenerator failed: %v", err)
		return
	}

	// Save the change to Validatechain, and then broadcast to other validators
	vcBlock := validatechain.NewVCBlock()
	curState := vm.validateChain.GetCurrentState()

	vcBlock.Header.Height = curState.LatestHeight + 1 // height + 1
	vcBlock.Header.PrevHash = curState.LatestHash     // prev hash is current state hash
	vcBlock.Header.DataType = validatechain.DataType_UpdateEpoch

	dataBlock := &validatechain.DataUpdateEpoch{
		UpdatedId:     vm.myValidator.ValidatorInfo.ValidatorId,
		PublicKey:     vm.myValidator.ValidatorInfo.PublicKey,
		EpochIndex:    vm.CurrentEpoch.EpochIndex,
		CreateTime:    vm.CurrentEpoch.CreateTime.Unix(),
		Reason:        validatechain.UpdateEpochReason_GeneratorHandOver,
		EpochItemList: make([]epoch.EpochItem, 0),
		GeneratorPos:  uint32(vm.CurrentEpoch.GetCurGeneratorPos()),
		Generator:     vm.CurrentEpoch.Generator,
	}

	for _, item := range vm.CurrentEpoch.ItemList {
		dataBlock.EpochItemList = append(dataBlock.EpochItemList, *item)
	}

	vcBlock.Data = dataBlock

	err = vm.SaveVCBlock(vcBlock)
	if err != nil {
		return
	}

	vm.BroadcastVCBlock(validatorcommand.BlockType_VCBlock, &vcBlock.Header.Hash)

	// Record the change to current epoch
	vm.CurrentEpoch.LastChangeTime = time.Unix(vcBlock.Header.CreateTime, 0) // vcBlock.Header.CreateTime
	vm.CurrentEpoch.VCBlockHeight = vcBlock.Header.Height
	vm.CurrentEpoch.VCBlockHash = &vcBlock.Header.Hash
	// Broadcast for epoch changed
	updateEpochCmd := validatorcommand.NewMsgUpdateEpoch(vm.CurrentEpoch)
	vm.BroadcastCommand(updateEpochCmd)

	vm.resetGeneratorMoniter()
}

func (vm *ValidatorManager) OnTimeGenerateBlock() (*chainhash.Hash, int32, error) {
	utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock...")

	// Notify validator manager to generate new block
	hash, height, err := vm.Cfg.PosMiner.OnTimeGenerateBlock()
	if err != nil {
		utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock failed: %v", err)
		// Generate block failed, it should be no tx to be mined, wait for next time
		if err.Error() == "no any new tx in mempool" || err.Error() == "no any new tx need to be mining" {
			if vm.CurrentEpoch.Generator != nil && vm.CurrentEpoch.Generator.GeneratorId == vm.Cfg.ValidatorId {
				// No any tx to be mined, continue next slot
				vm.myValidator.ContinueNextSlot()

				// Update current epoch generator and broadcast
				newGenerator := vm.myValidator.GetMyGenerator()
				vm.CurrentEpoch.Generator = newGenerator

				vm.resetGeneratorMoniter()

				updateEpochCmd := validatorcommand.NewMsgUpdateEpoch(vm.CurrentEpoch)
				vm.BroadcastCommand(updateEpochCmd)
			}
			return nil, 0, err
		}

		// Miner 错误， 直接流转到下一个validator
		utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock failed. The error is: %v", err)

		// Will handover to next validator, clear my generator
		vm.myValidator.ClearMyGenerator()

		// New block generated, should be hand over to next validator with next height
		vm.HandoverToNextGenerator()
		// It should some error in the peer, hand over to next validator to miner, the height is not changed

		return nil, 0, err
	}
	utils.Log.Debugf("[ValidatorManager]OnTimeGenerateBlock succeed, Hash: %s", hash.String())

	// Save vc block first and broadcast the block
	newBlock := &generator.MinerNewBlock{
		GeneratorId: vm.myValidator.ValidatorInfo.ValidatorId,
		PublicKey:   vm.myValidator.ValidatorInfo.PublicKey,
		Height:      height,
		MinerTime:   time.Now().Unix(),
		Hash:        hash,
	}
	tokenData := newBlock.GetTokenData()
	token, err := vm.myValidator.CreateToken(tokenData)
	if err == nil {
		newBlock.Token = token
		vm.VCBlock_MinerNewBlock(newBlock)
	}

	time.Sleep(500 * time.Millisecond)

	// Will handover to next validator, clear my generator
	vm.myValidator.ClearMyGenerator()

	// New block generated, should be hand over to next validator with next height
	vm.HandoverToNextGenerator()

	return hash, height, nil
}

func (vm *ValidatorManager) HandoverToNextGenerator() {
	utils.Log.Infof("[ValidatorManager]HandoverToNextGenerator...")
	if vm.CurrentEpoch == nil {
		err := errors.New("current epoch is nil")
		utils.Log.Errorf("[ValidatorManager]HandoverToNextGenerator failed: %v", err)
		return
	}

	curGenerator := vm.CurrentEpoch.GetGenerator()
	if curGenerator == nil {
		err := errors.New("Invalid local generator")
		utils.Log.Errorf("[ValidatorManager]HandoverToNextGenerator failed: %v", err)
		return
	}

	vm.needHandOver = true

	// New block generated, should be hand over to next validator with next height
	heightGenerator := curGenerator.Height + 1

	//count := 1 + BackupGeneratorCount
	nextGenerator := vm.CurrentEpoch.GetNextValidatorByEpochOrder()
	// if nextGenerators == nil || len(nextGenerators) == 0 || vm.isLocalValidatorById(nextGenerators[0].ValidatorId) {
	// 	utils.Log.Debugf("[ValidatorManager]Next generator is local validator, conitnue miner by local validator")
	// 	// No any generator or next generator is local validator, continue miner by local validator
	// 	vm.myValidator.ContinueNextSlot()
	// 	return hash, nil
	// }
	if nextGenerator == nil {
		utils.Log.Debugf("[ValidatorManager]No any next generator in current epoch, Will hand over to next epoch")
		// No any generator or next generator is local validator, continue miner by local validator
		// Current epoch is not valid, will req new epoch to miner new block
		nextEpoch := vm.GetNextEpoch()
		if nextEpoch != nil {
			utils.Log.Debugf("[ValidatorManager]Has exist next epoch, will hand over to next epoch")
			nextBlockHight := heightGenerator
			timeStamp := time.Now().Unix()
			handoverEpoch := &epoch.HandOverEpoch{
				ValidatorId:    vm.Cfg.ValidatorId,
				Timestamp:      timeStamp,
				NextEpochIndex: nextEpoch.EpochIndex,
				NextHeight:     nextBlockHight,
			}
			tokenData := handoverEpoch.GetNextEpochTokenData()
			token, err := vm.myValidator.CreateToken(tokenData)
			if err != nil {
				utils.Log.Errorf("Create token failed:  %v", err)
				return
			}
			handoverEpoch.Token = token
			nextEpochCmd := validatorcommand.NewMsgNextEpoch(handoverEpoch)
			vm.BroadcastCommand(nextEpochCmd)

			// Notify local peer for changing to next epoch
			vm.OnNextEpoch(handoverEpoch)
			vm.needHandOver = false // HandOver completed.

			utils.Log.Debugf("[ValidatorManager]hand over to next epoch completed")
			// Save handover new epoch into db and broadcast the block to validatechain
			// NNN

		} else {
			// No Next epoch, will req new epoch to miner new block
			utils.Log.Debugf("[ValidatorManager]No next epoch, will Req newepoch for next epoch, and handover to next epoch")
			nextEpochIndex := vm.getCurrentEpochIndex() + 1
			if nextEpochIndex == 1 {
				// Start the first epoch
				vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochCreate)
			} else {
				vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochStopped)
			}

		}

		return
	}

	utils.Log.Debugf("[ValidatorManager] Will hand over to next generator:%d", nextGenerator.ValidatorId)

	nextGeneratorConnected := true // For default, the next generator is connected
	// Check the next generator is connected
	nextValidator := vm.FindRemoteValidator(nextGenerator.ValidatorId)
	if nextValidator == nil {
		nextGeneratorConnected = false
	} else {
		nextGeneratorConnected = nextValidator.IsConnected()
	}

	if nextGeneratorConnected {
		handOver := generator.GeneratorHandOver{
			ValidatorId:  curGenerator.GeneratorId,
			HandOverType: generator.HandOverTypeByEpochOrder,
			Height:       heightGenerator,
			Timestamp:    curGenerator.MinerTime.Unix(), // Last miner time
			GeneratorId:  nextGenerator.ValidatorId,
		}

		tokenData := handOver.GetTokenData()
		token, err := vm.SignToken(tokenData)
		if err != nil {
			utils.Log.Errorf("Sign token failed: %v", err)
			return
		}
		handOver.Token = token

		utils.Log.Debugf("[ValidatorManager]HandOver: %+v", handOver)
		// Will Send HandOver to all Connected Validators
		cmdHandOver := validatorcommand.NewMsgHandOver(&handOver)
		vm.BroadcastCommand(cmdHandOver)
		vm.needHandOver = false // HandOver completed.
	} else {
		utils.Log.Debugf("[ValidatorManager] The next generator is not connected, Will remove the next generator:%d", nextGenerator.ValidatorId)
		// The next generator is disconnected
		// Remove the next generator from epoch, and will req new epoch to miner new block
		// 在多次尝试重连失败后，需要剔除成员
		vm.epochMemberMgr.ReqDelEpochMember(nextGenerator.ValidatorId)
	}
}

func (vm *ValidatorManager) BroadcastCommand(command validatorcommand.Message) {
	utils.Log.Debugf("[ValidatorManager]Will broadcast command to all connected validators...")
	for _, validator := range vm.ConnectedList {
		utils.Log.Debugf("[ValidatorManager]Send command <%s> to %s...", command.Command(), validator.String())
		validator.SendCommand(command)
	}
}

// Req new epoch from remote peer
func (vm *ValidatorManager) ReqNewEpoch(validatorID uint64, epochIndex int64, reason uint32) (*chainhash.Hash, error) {

	currentValidatorCount := len(vm.ConnectedList) + 1
	if currentValidatorCount < MinValidatorsCountEachEpoch {
		return nil, errors.New("no enough validators to new epoch")
	}

	//nextEpochIndex := vm.getCurrentEpochIndex() + 1
	currentBlockHeight := vm.Cfg.PosMiner.GetBlockHeight()
	newEpoch := &epoch.Epoch{
		EpochIndex:      epochIndex,
		CreateHeight:    currentBlockHeight,
		CreateTime:      time.Now(),
		ItemList:        make([]*epoch.EpochItem, 0),
		CurGeneratorPos: epoch.Pos_Epoch_NotStarted,
		Generator:       nil, // will be set by local validator
	}

	lastEpochMemberId := uint64(0)
	if vm.CurrentEpoch != nil {
		lastEpochMemberId = vm.CurrentEpoch.GetLastEpochMemberId()
	}

	validators := vm.getValidatorList()
	sortsValidatorList(validators)

	lastEpochMemberPos := getValidatorPos(validators, lastEpochMemberId)

	start := lastEpochMemberPos + 1
	if start >= len(validators) {
		start = 0
	}

	count := 0

	// Fill item to EpochItem list
	for i := start; i < len(validators); i++ {
		validator := validators[i]

		item := &epoch.EpochItem{
			ValidatorId: validator.ValidatorId,
			Host:        validator.Host,
			PublicKey:   validator.PublicKey,
			Index:       uint32(i)}

		newEpoch.ItemList = append(newEpoch.ItemList, item)
		count++

		if count >= MaxValidatorsCountEachEpoch {
			break
		}
	}

	if start > 0 && count < MaxValidatorsCountEachEpoch {
		// Fill the rest of item to EpochItem list
		for i := 0; i < start; i++ {
			validator := validators[i]

			item := &epoch.EpochItem{
				ValidatorId: validator.ValidatorId,
				Host:        validator.Host,
				PublicKey:   validator.PublicKey,
				Index:       uint32(i)}

			newEpoch.ItemList = append(newEpoch.ItemList, item)
			count++

			if count >= MaxValidatorsCountEachEpoch {
				break
			}
		}
	}

	newEpochVote := epoch.NewEpochVote{
		VotorId:   vm.myValidator.ValidatorInfo.ValidatorId,
		PublicKey: vm.myValidator.ValidatorInfo.PublicKey,
		NewEpoch:  newEpoch,
		Reason:    reason,
	}
	tokenData := newEpochVote.GetTokenData()
	if tokenData == nil {
		return nil, errors.New("token data is nil")
	}
	token, err := vm.SignToken(tokenData)
	if err != nil {
		return nil, err
	}
	newEpochVote.Token = token

	// Save new epoch vote to db and broad cast to all validators
	epBlock := validatechain.NewEPBlock()
	epBlock.Data = &validatechain.DataEpochVote{
		VotorId:       newEpochVote.VotorId,
		PublicKey:     newEpochVote.PublicKey,
		EpochIndex:    newEpochVote.NewEpoch.EpochIndex,
		CreateTime:    newEpochVote.NewEpoch.CreateTime.Unix(),
		Reason:        newEpochVote.Reason,
		EpochItemList: make([]epoch.EpochItem, 0),
		Token:         newEpochVote.Token,
	}
	for _, item := range newEpoch.ItemList {
		epBlock.Data.EpochItemList = append(epBlock.Data.EpochItemList, *item)
	}

	showVoteData("Save Epoch vote before saved", epBlock.Data)

	err = vm.validateChain.SaveEPBlock(epBlock)
	if err != nil {
		return nil, err
	}

	blockHash, err := epBlock.GetHash()
	if err != nil {
		return nil, err
	}
	showEpoch("New Epoch", newEpoch)
	utils.Log.Debugf("New Epoch Block hash: %s", blockHash.String())
	// Broadcast new epoch to all validators
	//newEpochVoteMsg := validatorcommand.NewMsgNewEpoch(&newEpochVote)
	vm.BroadcastVCBlock(validatorcommand.BlockType_EPBlock, blockHash)

	// Test
	voteItemData, err := vm.validateChain.GetEPBlock(blockHash)
	if err != nil {
		utils.Log.Debugf("Cannot get epblock by hash [%s] ", blockHash.String())
		return nil, err
	}

	showVoteData("Read Epoch vote after saved", voteItemData.Data)

	// response the new epoch to remote peer
	return blockHash, nil
}

// OnNextEpoch from remote peer
func (vm *ValidatorManager) OnNextEpoch(handoverEpoch *epoch.HandOverEpoch) {
	utils.Log.Debugf("[ValidatorManager]OnNextEpoch ...")

	if handoverEpoch == nil {
		utils.Log.Debugf("OnNextEpoch: handoverEpoch is nil, return")
		return
	}
	if handoverEpoch.NextEpochIndex <= vm.getCurrentEpochIndex() {
		utils.Log.Debugf("OnNextEpoch: handoverEpoch has started.")
		return
	}
	if vm.NextEpoch == nil {
		utils.Log.Debugf("OnNextEpoch: vm.NextEpoch is nil, next epoch is not ready.")
		return
	}
	// Change the next epoch to current epoch, and clear next epoch
	//vm.CurrentEpoch = vm.NextEpoch
	//vm.epochMemberMgr.UpdateCurrentEpoch(vm.CurrentEpoch)
	newCurrentEpoch := vm.NextEpoch
	vm.NextEpoch = nil
	vm.setCurrentEpoch(newCurrentEpoch)

	// New epoch is set clear handover flag
	vm.needHandOver = false

	showEpoch("New current epoch", vm.CurrentEpoch)

	if vm.CurrentEpoch != nil {
		nextEpochValidator := vm.CurrentEpoch.GetNextValidator()
		if nextEpochValidator == nil {
			utils.Log.Errorf("The new epoch has no next validator")
			return
		}
		utils.Log.Debugf("Start new epoch with validator : %d", nextEpochValidator.ValidatorId)
		if nextEpochValidator.ValidatorId == vm.GetMyValidatorId() {
			// The first validator of new current epoch is local validator
			vm.SetLocalAsNextGenerator(handoverEpoch.NextHeight, time.Unix(handoverEpoch.Timestamp, 0))
		}
	}
}
func (vm *ValidatorManager) OnUpdateEpoch(currentEpoch *epoch.Epoch) {
	utils.Log.Debugf("[ValidatorManager]OnUpdateEpoch ...")
	// Check the current epoch is valid or not
	if vm.CurrentEpoch == nil {
		//vm.CurrentEpoch = currentEpoch
		//vm.epochMemberMgr.UpdateCurrentEpoch(vm.CurrentEpoch)
		vm.setCurrentEpoch(currentEpoch)

		if vm.NextEpoch != nil {
			utils.Log.Debugf("[ValidatorManager]Check the next epoch is same as current epoch.")
			if vm.NextEpoch.EpochIndex == currentEpoch.EpochIndex {
				utils.Log.Debugf("[ValidatorManager]The next epoch is handover current epoch.")
				vm.NextEpoch = nil
			}
		}

		utils.Log.Debugf("[ValidatorManager]New current epoch Updated.")
		showEpoch("New current epoch", vm.CurrentEpoch)
		return
	}

	if vm.CurrentEpoch.EpochIndex != currentEpoch.EpochIndex {
		utils.Log.Debugf("[ValidatorManager]Invalid current epoch.")
		return
	}

	// Check the current epoch is latest or not
	if vm.CurrentEpoch.VCBlockHeight > currentEpoch.VCBlockHeight {
		utils.Log.Debugf("[ValidatorManager]The update epoch isnot latest.")
		return
	} else if vm.CurrentEpoch.VCBlockHeight == currentEpoch.VCBlockHeight {
		utils.Log.Debugf("[ValidatorManager]The VC block height (%d) is the same.", currentEpoch.VCBlockHeight)
		// No record change for the current epoch, it just the generator miner time change if no any tx to be mind
		// Check the current epoch member isnot changed
		oldEpochMemberList := vm.CurrentEpoch.GetValidatorList()
		newEpochMemberList := currentEpoch.GetValidatorList()
		if len(oldEpochMemberList) != len(newEpochMemberList) {
			utils.Log.Debugf("[ValidatorManager]The epoch change isnot record, ignored.")
			return
		}
		count := len(oldEpochMemberList)
		for i := 0; i < count; i++ {
			if oldEpochMemberList[i].ValidatorId != newEpochMemberList[i].ValidatorId {
				utils.Log.Debugf("[ValidatorManager]The epoch change <member change> isnot record, ignored.")
				return
			}
		}

		// Check generator is not changed
		if vm.CurrentEpoch.GetGenerator() == nil || currentEpoch.GetGenerator() == nil {
			utils.Log.Debugf("[ValidatorManager]The epoch change <generator change> isnot record, ignored.")
			return
		}

		if vm.CurrentEpoch.CurGeneratorPos != currentEpoch.CurGeneratorPos || vm.CurrentEpoch.GetGenerator().GeneratorId != currentEpoch.GetGenerator().GeneratorId {
			utils.Log.Debugf("[ValidatorManager]The epoch change <generator pos or generator id change>isnot record, ignored.")
			return
		}

		if vm.CurrentEpoch.GetGenerator().MinerTime == currentEpoch.GetGenerator().MinerTime {
			utils.Log.Debugf("[ValidatorManager]The epoch miner time isnot change, ignored.")
			return
		}

		// Check current mempool is empty or not
		txSizeInMempool := vm.Cfg.PosMiner.GetMempoolTxSize()
		if txSizeInMempool > 0 {
			utils.Log.Debugf("[ValidatorManager]Current mempool is not empty, cannot update miner time directly.")
			return
		}

		// Update new miner timer
		vm.CurrentEpoch.GetGenerator().MinerTime = currentEpoch.GetGenerator().MinerTime
		vm.resetGeneratorMoniter()

		showEpoch("Updated current epoch", vm.CurrentEpoch)
		return
	}

	utils.Log.Debugf("[ValidatorManager]The update epoch is latest, will update to local.")

	// vm.CurrentEpoch.Generator = currentEpoch.Generator
	// vm.CurrentEpoch.CurGeneratorPos = currentEpoch.CurGeneratorPos
	vm.setCurrentEpoch(currentEpoch)

	if vm.CurrentEpoch.Generator == nil {
		utils.Log.Debugf("[ValidatorManager]No generator in current epoch now, will generate a new generator ...")
		posGenerator := vm.CurrentEpoch.GetCurGeneratorPos()

		if posGenerator < 0 {
			posGenerator = 0
		}
		generatorId := vm.CurrentEpoch.GetMemberValidatorId(posGenerator)
		if generatorId == vm.Cfg.ValidatorId {
			utils.Log.Debugf("[ValidatorManager]local validator is the generator in current epoch now, will become local validator as new generator ...")
			// if current generator is nil, it should be the generator is disconnect, and to be remove, if the curGeenerator is the local generator, it should became to generator
			nextHeight := vm.Cfg.PosMiner.GetBlockHeight() + 1
			handoverTime := time.Now()

			vm.SetLocalAsCurrentGenerator(nextHeight, handoverTime)
		}
	}
	// 如果epoch更新后，当前epoch的generator是本地validator，且是最后一个generator，就请求下一轮的epoch
	if vm.CurrentEpoch.Generator != nil {
		GeneratorId := vm.CurrentEpoch.Generator.GeneratorId
		if vm.NextEpoch == nil && vm.isLocalValidatorById(GeneratorId) {
			if vm.CurrentEpoch.IsLastGenerator() {
				// Broadcast  for New Epoch
				// Current epoch is last, request New Epoch
				nextEpochIndex := vm.getCurrentEpochIndex() + 1
				vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochHandOver)

			}
		}
	}

	utils.Log.Debugf("[ValidatorManager]Current epoch Updated.")
	showEpoch("Updated current epoch", vm.CurrentEpoch)
}

func (vm *ValidatorManager) CheckContinueHandOver() {
	if vm.needHandOver {
		utils.Log.Debugf("[ValidatorManager]Handover to next generator after OnUpdateEpoch (next epoch member should be disconnect and removed) ...")
		vm.HandoverToNextGenerator()
	} else {
		utils.Log.Debugf("[ValidatorManager]No continue action .")
	}
}

func (vm *ValidatorManager) setCurrentEpoch(currentEpoch *epoch.Epoch) {

	utils.Log.Debugf("setCurrentEpoch...")

	vm.CurrentEpoch = currentEpoch
	vm.epochMemberMgr.UpdateCurrentEpoch(vm.CurrentEpoch)
	if vm.CurrentEpoch == nil {
		utils.Log.Debugf("setCurrentEpoch to nil.")
		return
	}

	vm.resetGeneratorMoniter()
}

func (vm *ValidatorManager) resetGeneratorMoniter() {
	utils.Log.Debugf("resetGeneratorMoniter...")
	if vm.moniterGeneratorTicker == nil {
		// Not start monitor
		utils.Log.Debugf("GeneratorTicker is not start or stopped.")
		return
	}

	pos := vm.CurrentEpoch.GetValidatorPos(vm.Cfg.ValidatorId)
	if pos == -1 {
		vm.isEpochMember = false
	} else {
		vm.isEpochMember = true
	}

	if vm.isEpochMember {
		posGenerator := vm.CurrentEpoch.GetCurGeneratorPos()
		memCount := vm.CurrentEpoch.GetMemberCount()
		if posGenerator == pos {
			monitorInterval := GeneratorMonitorInterval_EpochMember + MaxExpiration
			utils.Log.Debugf("local generator: Next check generator after %f seconds.", monitorInterval.Seconds())
			vm.moniterGeneratorTicker.Reset(monitorInterval)
		} else if pos < posGenerator {
			// 已经完成了出块，只需要监控后面member出块的情况
			monitorInterval := GeneratorMonitorInterval_EpochMember + MaxExpiration + time.Duration(memCount-posGenerator)*time.Second
			utils.Log.Debugf("mined epoch member: Next check generator after %f seconds.", monitorInterval.Seconds())
			vm.moniterGeneratorTicker.Reset(monitorInterval)
		} else { // pos > posGenerator
			// 还没有完成了出块，需要监控前面member出块的情况
			monitorInterval := GeneratorMonitorInterval_EpochMember + MaxExpiration + time.Duration(pos-posGenerator)*time.Second
			utils.Log.Debugf("waiting epoch member: Next check generator after %f seconds.", monitorInterval.Seconds())
			vm.moniterGeneratorTicker.Reset(monitorInterval)
		}
	} else {
		monitorInterval := GeneratorMonitorInterval_UonMember
		utils.Log.Debugf("Not epoch member :Next check generator after %f seconds.", monitorInterval.Seconds())
		vm.moniterGeneratorTicker.Reset(monitorInterval)
	}
}

func (vm *ValidatorManager) getCurrentEpochIndex() int64 {
	utils.Log.Debugf("[ValidatorManager]getCurrentEpochIndex...")
	if vm.CurrentEpoch == nil {
		// Get from db
		if vm.validateChain == nil {
			utils.Log.Debugf("[ValidatorManager]No validateChain, start from [0].")
			return 0
		}
		currentState := vm.validateChain.GetCurrentState()
		if currentState == nil {
			utils.Log.Debugf("[ValidatorManager]Cannot get current state from validateChain, start from [0].")
			return 0
		}
		utils.Log.Debugf("[ValidatorManager]LatestEpochIndex [%d] in validateChain.", currentState.LatestEpochIndex)
		return currentState.LatestEpochIndex
	}
	utils.Log.Debugf("[ValidatorManager]current epoch index is [%d].", vm.CurrentEpoch.EpochIndex)
	return vm.CurrentEpoch.EpochIndex
}

func (vm *ValidatorManager) getCheckEpochHandler() {
	utils.Log.Debugf("[ValidatorManager]getCheckEpochHandler ...")

	exitGeneraterHandler := make(chan struct{})
	// Sync current epoch from all connected validators
	// 计算从现在到目标时间的间隔, 启动15秒后检查一次
	duration := time.Second * 15
	time.AfterFunc(duration, func() {
		vm.CheckEpoch()
		exitGeneraterHandler <- struct{}{}
	})

	// 这里阻塞主 goroutine 等待任务执行（可根据需要改为其他逻辑）
	select {
	case exitGeneraterHandler <- struct{}{}:
		utils.Log.Debugf("[ValidatorManager]getCheckEpochHandler done .")
		return
	case <-vm.quit:
		return
	}

}

// 启动15秒后检查一次， 如果当前没有有效的epoch， 并且所有连接的validator超过最小validator数量， 就申请生成新的epoch
func (vm *ValidatorManager) CheckEpoch() {
	utils.Log.Debugf("[ValidatorManager]CheckEpoch ...")
	if vm.CurrentEpoch != nil {
		utils.Log.Debugf("[ValidatorManager]Has a epoch, Nothing to do...")
		showEpoch("Current Epoch", vm.CurrentEpoch)
		return
	}

	utils.Log.Debugf("[ValidatorManager]No Epoch now ...")
	// 没有有效的Epoch， 就申请生成新的epoch
	AcitvityValidatorCount := len(vm.ConnectedList) + 1
	if AcitvityValidatorCount >= MinValidatorsCountEachEpoch {
		utils.Log.Debugf("Not valid epoch to miner, will req new epoch to miner new block")
		nextEpochIndex := vm.getCurrentEpochIndex() + 1
		// No epoch exist, request New Epoch
		if nextEpochIndex == 1 {
			// Start the first epoch
			vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochCreate)
		} else {
			vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochStopped)
		}
	} else {
		utils.Log.Debugf("Not enough validators, cannot new epoch to miner new block")
	}
}

func (vm *ValidatorManager) RequestNewEpoch(nextEpochIndex int64, reason uint32) {

	utils.Log.Debugf("[ValidatorManager]Will Req newepoch for next epoch [%d] with reason %d", nextEpochIndex, reason)

	if vm.ConnectedList == nil || len(vm.ConnectedList) == 0 {
		utils.Log.Debugf("No any validator connected")
		if vm.myValidator.IsBootStrapNode() {
			utils.Log.Debugf("Current validator is bootstap node, will new epoch only bootstap node")
			currentBlockHeight := vm.Cfg.PosMiner.GetBlockHeight()
			newEpoch := &epoch.Epoch{
				EpochIndex:      nextEpochIndex,
				CreateHeight:    currentBlockHeight,
				CreateTime:      time.Now(),
				ItemList:        make([]*epoch.EpochItem, 0),
				CurGeneratorPos: epoch.Pos_Epoch_NotStarted,
				Generator:       nil, // will be set by local validator
			}

			newEpoch.AddValidatorToEpoch(&vm.myValidator.ValidatorInfo)
			vm.ConfirmNewEpoch(newEpoch, validatechain.NewEpochReason_BootStrapNode, nil)

			// Notify local peer for the confirmed epoch
			vm.OnConfirmEpoch(newEpoch, nil)
		}
		return
	}

	// Will Send CmdReqEpoch to all Connected Validators
	CmdReqEpoch := validatorcommand.NewMsgReqEpoch(vm.Cfg.ValidatorId, nextEpochIndex, reason)
	vm.newEpochMgr = CreateNewEpochManager(vm, reason)
	//vm.BroadcastCommand(CmdReqEpoch)
	utils.Log.Debugf("Will broadcast ReqEpoch command from all connected validators...")
	for _, validator := range vm.ConnectedList {
		validator.SendCommand(CmdReqEpoch)
		vm.newEpochMgr.NewReqEpoch(validator.ValidatorInfo.ValidatorId)
	}

	// Add local validator to invited list
	vm.newEpochMgr.NewReqEpoch(vm.Cfg.ValidatorId)
	vm.newEpochMgr.Start()

	// Add local new epoch result to newepochmanager
	hash, err := vm.ReqNewEpoch(vm.Cfg.ValidatorId, nextEpochIndex, reason)
	if err != nil {
		utils.Log.Debugf("ReqNewEpoch failed: %v", err)
		return
	}
	vm.newEpochMgr.AddReceivedEpoch(vm.Cfg.ValidatorId, hash)
}

func (vm *ValidatorManager) OnNewEpoch(validatorId uint64, hash *chainhash.Hash) {
	utils.Log.Debugf("[ValidatorManager]OnNewEpoch received from validator [%d]...", validatorId)
	if hash == nil {
		utils.Log.Debugf("[ValidatorManager]OnNewEpoch Hash is nil, nothing to do...")
		return
	}

	utils.Log.Debugf("[ValidatorManager]OnNewEpoch Hash: [%s]", hash.String())
	// title := fmt.Sprintf("Received [%d] New Epoch", validatorId)
	// showEpoch(title, epoch)

	// Todo: due to received epoch, for new next epoch
	if vm.newEpochMgr != nil {
		vm.newEpochMgr.AddReceivedEpoch(validatorId, hash)
	}
}

func (vm *ValidatorManager) GetMyValidatorId() uint64 {
	return vm.Cfg.ValidatorId
}

// Received a Del epoch member command
func (vm *ValidatorManager) ConfirmDelEpochMember(reqDelEpochMember *validatorcommand.MsgReqDelEpochMember, remoteAddr net.Addr) *epoch.DelEpochMember {
	if vm.epochMemberMgr != nil {
		return vm.epochMemberMgr.ConfirmDelEpochMember(reqDelEpochMember, remoteAddr)
	}

	return nil
}

func (vm *ValidatorManager) OnConfirmedDelEpochMember(delEpochMember *epoch.DelEpochMember) {
	if vm.epochMemberMgr != nil {
		vm.epochMemberMgr.OnConfirmedDelEpochMember(delEpochMember)
	}
}

// Received a notify handover command
func (vm *ValidatorManager) OnNotifyHandover(validatorId uint64) {
	utils.Log.Debugf("[ValidatorManager]OnNotifyHandover from %d", validatorId)

	if vm.CurrentEpoch == nil {
		utils.Log.Debug("Invalid Current epoch.")
		return
	}
	// Check local peer is generator
	curGenerator := vm.CurrentEpoch.GetGenerator()
	if curGenerator == nil {
		utils.Log.Debugf("[ValidatorManager]OnNotifyHandover, curGenerator is nil, nothing to do...")
		return
	}

	if curGenerator.GeneratorId != vm.Cfg.ValidatorId {
		utils.Log.Debug("Current generator is not local peer.")
		return
	}

	now := time.Now()

	if curGenerator.MinerTime.After(now) {
		// The miner time is not on, ignore
		return
	}

	// Will handvoer generator to next
	// Will handover to next validator, clear my generator
	vm.myValidator.ClearMyGenerator()

	// New block generated, should be hand over to next validator with next height
	vm.HandoverToNextGenerator()
}

func (vm *ValidatorManager) SignToken(tokenData []byte) (string, error) {
	if vm.myValidator == nil {
		return "", errors.New("local validator isnot ready")
	}
	token, err := vm.myValidator.CreateToken(tokenData)
	if err != nil {
		return "", errors.New("create token failed")
	}

	return token, nil
}

// monitor generator handover, if the generator is stoped, try to revote a new generator or recreate a new epoch
func (vm *ValidatorManager) monitorGeneratorHandOverHandler() {
	monitorInterval := GeneratorMonitorInterval_UonMember
	vm.moniterGeneratorTicker = time.NewTicker(monitorInterval)

exit:
	for {
		select {
		case <-vm.moniterGeneratorTicker.C:
			vm.monitorGeneratorHandOver()
		case <-vm.quit:
			break exit
		}
	}

	vm.moniterGeneratorTicker.Stop()
	vm.moniterGeneratorTicker = nil

	utils.Log.Debugf("[ValidatorManager]monitorGeneratorHandOverHandler done.")

}

func (vm *ValidatorManager) monitorGeneratorHandOver() {
	utils.Log.Debugf("[ValidatorManager]monitorGeneratorHandOver...")
	// 运行到这里，则在监控周期内， generator没有被轮转， 需要重新轮转
	// 检查当前的generator没有轮转的原因
	// 检查项目：
	// 1. 检查当前的epoch是否有效， 如果是无效, 则发起reqepoch请求
	// 2. 如果本地是epoch成员,则检查当前的generator是否在线, 如果不在线,则发起删除成员请求, 删除成功后, 有新成员作为下一个generator, 进入轮转
	// 3. 如果不是epoch成员，在监控时间到了以后, 所有的epoch成员都没有让generator轮转起来, 直接发起reqepoch请求
	// Check the current epoch is valid or not
	if vm.CurrentEpoch == nil {
		// 当前epoch无效, 有引导节点发起newepoch请求
		if vm.myValidator.IsBootStrapNode() {
			utils.Log.Debugf("[ValidatorManager]Request New Epoch with empty epoch by bootstrap node...")
			nextEpochIndex := vm.getCurrentEpochIndex() + 1
			if nextEpochIndex == 1 {
				// Start the first epoch
				vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochCreate)
			} else {
				vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochStopped)
			}
		}

		return
	}

	if vm.NextEpoch == nil && vm.CurrentEpoch.IsLastGenerator() {
		// current epoch is last generator, and next epoch is nil, the generator should request New Epoch
		if vm.CurrentEpoch.Generator != nil && vm.CurrentEpoch.Generator.GeneratorId == vm.Cfg.ValidatorId {
			// Current member is generator
			nextEpochIndex := vm.getCurrentEpochIndex() + 1
			vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochHandOver)
		}
	}

	txSizeInMempool := vm.Cfg.PosMiner.GetMempoolTxSize()
	utils.Log.Debugf("[ValidatorManager]Current txSizeInMempool = %d.", txSizeInMempool)

	if vm.CurrentEpoch.Generator == nil {
		utils.Log.Debugf("[ValidatorManager]Current epoch generator is nil, will set new generator...")
		// 获取当前的generator的位置
		posGenerator := vm.CurrentEpoch.GetCurGeneratorPos()

		if posGenerator < 0 {
			// if posGenerator is -1, the generator is not started, start now with the first member
			posGenerator = 0
		}

		if posGenerator >= vm.CurrentEpoch.GetMemberCount() {
			// The generator is exceed the member count, it should be the generator is disconnect, and to be remove
			utils.Log.Debugf("[ValidatorManager]The generator is exceed the member count, it will handover to next epoch...")
			if vm.myValidator.IsBootStrapNode() {
				vm.handoverToNextEpoch()
			}
			return
		}

		generatorId := vm.CurrentEpoch.GetMemberValidatorId(posGenerator)
		utils.Log.Debugf("[ValidatorManager]The New generatorid is %d in pos [%d]", generatorId, posGenerator)
		if generatorId == vm.Cfg.ValidatorId {
			utils.Log.Debugf("[ValidatorManager]The new generator is local node, Set local as generator.")
			nextHeight := vm.Cfg.PosMiner.GetBlockHeight() + 1
			handoverTime := time.Now()

			vm.SetLocalAsCurrentGenerator(nextHeight, handoverTime)

			if vm.NextEpoch == nil && vm.CurrentEpoch.IsLastGenerator() {
				// current epoch is last generator, and next epoch is nil, the generator should request New Epoch
				if vm.CurrentEpoch.Generator != nil && vm.CurrentEpoch.Generator.GeneratorId == vm.Cfg.ValidatorId {
					// Current member is generator
					nextEpochIndex := vm.getCurrentEpochIndex() + 1
					vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochHandOver)
				}
			}

			return
		} else {
			generator := vm.FindRemoteValidator(generatorId)
			if generator == nil || generator.IsConnected() == false {
				utils.Log.Debugf("[ValidatorManager]The new generator %d has disconnected.", generatorId)
				if vm.myValidator.IsBootStrapNode() {
					utils.Log.Debugf("[ValidatorManager] Will del generator %d by bootstrap node...", generatorId)
					// Generator is not connected, del generatorId
					vm.epochMemberMgr.ReqDelEpochMember(generatorId)
				}
			} else {
				utils.Log.Debugf("[ValidatorManager]The new generator %d is connected.", generatorId)
				pastChangeDuation := vm.getPastTimeFromLastChange()
				if pastChangeDuation < UnexceptionInterval {
					// The change time is not unexpected, ignore
					return
				}

				// The change time is exceed unexception time, current epoch is not running,will set to next epoch by bootstrap node
				if vm.myValidator.IsBootStrapNode() {
					vm.handoverToNextEpoch()
				}
			}
		}

		return
	}

	//getPastTimeFromLastMiner()
	pastMinerDuation := vm.getPastTimeFromLastMiner()
	if pastMinerDuation < generator.MinerInterval {
		// The miner time is not past, ignore
		return
	}

	if pastMinerDuation > UnexceptionInterval {
		// The miner is exceed unexception time, will reset epoch by bootstrap node
		utils.Log.Debugf("[ValidatorManager]The miner is exceeded the unexpected time %f, UnexceptionInterval = %f", pastMinerDuation.Seconds(), UnexceptionInterval.Seconds())
		// if vm.myValidator.IsBootStrapNode() {
		// 	vm.handoverToNextEpoch()
		// }

		// need to miner new block directly by IsBootStrapNode if exist tx in mempool
		txSizeInMempool := vm.Cfg.PosMiner.GetMempoolTxSize()
		if txSizeInMempool > 0 {
			if vm.myValidator.IsBootStrapNode() {
				utils.Log.Debugf("[ValidatorManager]Directly miner by bootstrap node, txSizeInMempool = %d.", txSizeInMempool)
				vm.OnTimeGenerateBlock()
			}
		}
		return
	}

	utils.Log.Debugf("[ValidatorManager]The miner is exceeded the expected time %f.", pastMinerDuation.Seconds())

	// if vm.isEpochMember == false {
	// 	// 如果不是epoch成员，则在监控时间到了以后, 所有的epoch成员都没有让generator轮转起来, 直接发起req newepoch请求
	// 	utils.Log.Debugf("[ValidatorManager]Request New Epoch for stopped epoch by not epoch member ...")
	// 	nextEpochIndex := vm.getCurrentEpochIndex() + 1
	// 	vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochStopped)

	// 	return
	// }

	// 当前节点是epoch成员
	posGenerator := vm.CurrentEpoch.GetCurGeneratorPos()

	if posGenerator >= 0 {
		if posGenerator >= vm.CurrentEpoch.GetMemberCount() {
			// The generator is exceed the member count, it should be the generator is disconnect, and to be remove
			utils.Log.Debugf("[ValidatorManager]The generator is exceed the member count, it will handover to next epoch...")
			if vm.myValidator.IsBootStrapNode() {
				vm.handoverToNextEpoch()
			}
			return
		}
		generatorId := vm.CurrentEpoch.GetMemberValidatorId(posGenerator)
		// curGenerator := vm.CurrentEpoch.GetGenerator()
		// if curGenerator == nil || curGenerator.GeneratorId != generatorId {
		// 	// current generator is not
		// }
		utils.Log.Debugf("[ValidatorManager]The epoch is started, but generator not handover to next epoch member ...")
		if generatorId == vm.Cfg.ValidatorId {
			utils.Log.Debugf("[ValidatorManager]Start handover to next generator...")
			// 当前的Generator是本地节点， 直接通知handover到下一个节点
			vm.OnNotifyHandover(generatorId)
			return
		}

		validator, isConnected := vm.epochMemberMgr.GetEpochMember(generatorId)
		if isConnected {
			utils.Log.Debugf("[ValidatorManager]Notify %d to handover...", generatorId)
			// 当前的Generator is online， 通知Generator进行handover
			CmdNotifyHandOver := validatorcommand.NewMsgNotifyHandover(vm.Cfg.ValidatorId)
			err := validator.SendCommand(CmdNotifyHandOver)
			if err == nil {
				// Notify message send success
				return
			}
			isConnected = false
		}
		utils.Log.Debugf("[ValidatorManager]The generator %d has disconnected.", generatorId)
		if vm.myValidator.IsBootStrapNode() {
			utils.Log.Debugf("[ValidatorManager] Will del generator %d by bootstrap node...", generatorId)
			// Generator is not connected, del generatorId
			vm.epochMemberMgr.ReqDelEpochMember(generatorId)
		}
	} else {
		utils.Log.Debugf("[ValidatorManager]The epoch isnot started ...")
		// 当前的Epoch还没有启动，开始启动
		nextEpochValidator := vm.CurrentEpoch.GetNextValidator()
		if nextEpochValidator == nil {
			utils.Log.Debugf("[ValidatorManager]Request New Epoch with No member in the epoch ...")
			if vm.myValidator.IsBootStrapNode() {
				// 当前epoch为空， 直接请求下一个epoch
				nextEpochIndex := vm.getCurrentEpochIndex() + 1
				vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochStopped)
			}
			return
		}

		utils.Log.Debugf("Start new epoch with validator : %d", nextEpochValidator.ValidatorId)
		if nextEpochValidator.ValidatorId == vm.GetMyValidatorId() {
			// The first validator of new current epoch is local validator
			utils.Log.Debugf("Start validator is local validator, start generator as epoch start.")
			nextHeight := vm.Cfg.PosMiner.GetBlockHeight() + 1
			handoverTime := time.Now()

			vm.SetLocalAsNextGenerator(nextHeight, handoverTime)
			return
		}

		// 得到下一个Generator的详细信息
		validator, isConnected := vm.epochMemberMgr.GetEpochMember(nextEpochValidator.ValidatorId)
		if isConnected {
			// 当前的Generator is online， 通知Generator进行handover
			utils.Log.Debugf("[ValidatorManager] Will Notify  next generator %d to handover...", nextEpochValidator.ValidatorId)
			CmdNotifyHandOver := validatorcommand.NewMsgNotifyHandover(vm.Cfg.ValidatorId)
			validator.SendCommand(CmdNotifyHandOver)
			return
		}

		utils.Log.Debugf("[ValidatorManager] The next generator %d is not connected, Will del it...", nextEpochValidator.ValidatorId)
		if vm.myValidator.IsBootStrapNode() {
			// Generator is not connected, del generatorId
			vm.epochMemberMgr.ReqDelEpochMember(nextEpochValidator.ValidatorId)
		}

	}

	// curGenerator := vm.epochMemberMgr.GetEpochMember(vm.Cfg.ValidatorId)
	// if curGenerator == nil {
	// 	// 当前epoch无generator
	// }

	// minerTime := curGenerator.MinerTime

	// if minerTime.Before(time.Now()) {
	// 	durationExpiration := time.Since(minerTime)
	// 	if durationExpiration > MaxExpiration {
	// 		// 当前的miner时间已经过期, 但是还没有generator更新，认定为轮转失败
	// 		if vm.isEpochMember == true {
	// 			// 如果Generator已经离线了，需要删除generator成员, 然后重新轮转
	// 			vm.epochMemberMgr.GetEpochMember(curGenerator.GeneratorId)
	// 		} else {
	// 			// 如果不是epoch成员，则在监控时间到了以后, 所有的epoch成员都没有让generator轮转起来, 直接发起reqepoch请求
	// 			nextEpochIndex := vm.getCurrentEpochIndex() + 1
	// 			vm.RequestNewEpoch(nextEpochIndex)
	// 		}
	// 	}
	// }

}

func (vm *ValidatorManager) handoverToNextEpoch() {
	utils.Log.Debugf("[ValidatorManager]Handover to next epoch ...")
	if vm.NextEpoch == nil {
		utils.Log.Debugf("[ValidatorManager]Request New Epoch with stopped epoch by bootstrap node...")
		nextEpochIndex := vm.getCurrentEpochIndex() + 1
		if nextEpochIndex == 1 {
			// Start the first epoch
			vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochCreate)
		} else {
			vm.RequestNewEpoch(nextEpochIndex, validatechain.NewEpochReason_EpochStopped)
		}
	} else {
		utils.Log.Debugf("[ValidatorManager]Has exist next epoch, will hand over to next epoch...")
		nextBlockHight := vm.Cfg.PosMiner.GetBlockHeight() + 1
		timeStamp := time.Now().Unix()
		handoverEpoch := &epoch.HandOverEpoch{
			ValidatorId:    vm.Cfg.ValidatorId,
			Timestamp:      timeStamp,
			NextEpochIndex: vm.NextEpoch.EpochIndex,
			NextHeight:     nextBlockHight,
		}
		tokenData := handoverEpoch.GetNextEpochTokenData()
		token, err := vm.myValidator.CreateToken(tokenData)
		if err != nil {
			utils.Log.Errorf("Create token failed:  %v", err)
			return
		}
		handoverEpoch.Token = token
		nextEpochCmd := validatorcommand.NewMsgNextEpoch(handoverEpoch)
		vm.BroadcastCommand(nextEpochCmd)

		// Notify local peer for changing to next epoch
		vm.OnNextEpoch(handoverEpoch)
		vm.needHandOver = false // HandOver completed.

		utils.Log.Debugf("[ValidatorManager]hand over to next epoch completed")
	}

}

// syncValidateChainHandler for sync validator list from remote peer on a timer
func (vm *ValidatorManager) syncValidateChainHandler() {

	// Sync VC
	vm.syncValidateChain()

	syncInterval := time.Second * 30
	syncTicker := time.NewTicker(syncInterval)
	defer syncTicker.Stop()

exit:
	for {
		utils.Log.Debugf("[ValidatorManager]Waiting next timer for syncing ValidateChain...")
		select {
		case <-syncTicker.C:
			vm.syncValidateChain()
		case <-vm.quit:
			break exit
		}
	}

	utils.Log.Debugf("[ValidatorManager]syncValidateChainHandler done.")
}

func (vm *ValidatorManager) getPastTimeFromLastMiner() time.Duration {
	if vm.CurrentEpoch == nil || vm.CurrentEpoch.Generator == nil {
		return 0
	}

	return time.Since(vm.CurrentEpoch.Generator.MinerTime)
}

func (vm *ValidatorManager) getPastTimeFromLastChange() time.Duration {
	if vm.CurrentEpoch == nil {
		return 0
	}

	return time.Since(vm.CurrentEpoch.LastChangeTime)
}

// syncValidateChain for sync validate chain list from remote peer on a timer
func (vm *ValidatorManager) syncValidateChain() {
	utils.Log.Debugf("[ValidatorManager]syncValidateChain ....")
	getVCStateCmd := validatorcommand.NewMsgGetVCState(vm.Cfg.ValidatorId)

	// Clear the sync validator and start sync VC State from all connected validators
	vm.vcSyncValidator = nil

	for _, validator := range vm.ConnectedList {
		utils.Log.Debugf("[syncValidateChain]Get VC State form %s...", validator.String())
		validator.SendCommand(getVCStateCmd)
	}

	utils.Log.Debugf("[ValidatorManager]syncValidateChain done.")
}

func (vm *ValidatorManager) syncVCBlock() {
	utils.Log.Debugf("[ValidatorManager]syncVCBlock ....")
	if vm.vcSyncValidator == nil {
		return
	}
	if vm.vcSyncValidator.IsConnected() == false {
		return
	}
	utils.Log.Debugf("[syncVCBlock]Get VC Block form %s...", vm.vcSyncValidator.String())
	// getVCListCmd := validatorcommand.NewMsgGetVCList()
	// vm.vcSyncValidator.GetVCBlock()

	utils.Log.Debugf("[ValidatorManager]syncVCBlock Done.")
}

// Received get vc state command
func (vm *ValidatorManager) GetVCState(validatorId uint64) (*validatorcommand.MsgVCState, error) {
	if vm.validateChain == nil {
		err := errors.New("ValidateChain is invalid")
		return nil, err
	}
	localState := vm.validateChain.GetCurrentState()
	if localState == nil {
		err := errors.New("ValidateChain state is invalid")
		return nil, err
	}
	vcStateCmd := validatorcommand.NewMsgVCState(localState.LatestHeight, localState.LatestHash, localState.LatestEpochIndex)
	return vcStateCmd, nil
}

// Received a vc state command
func (vm *ValidatorManager) OnVCState(vcStateCmd *validatorcommand.MsgVCState, validator *validator.Validator) {
	if vm.validateChain == nil {
		err := errors.New("ValidateChain is invalid")
		utils.Log.Error(err)
		return
	}
	localState := vm.validateChain.GetCurrentState()
	if localState == nil {
		err := errors.New("ValidateChain state is invalid")
		utils.Log.Error(err)
		return
	}

	if vcStateCmd.Height > localState.LatestHeight {
		if validator != nil && vm.vcSyncValidator == nil {
			// Start sync VC block from the validator
			vm.vcSyncValidator = validator
			getVCListCmd := validatorcommand.NewMsgGetVCList(vm.Cfg.ValidatorId, localState.LatestHeight, vcStateCmd.Height)
			//getVCListCmd.LogCommandInfo()
			vm.vcSyncValidator.SendCommand(getVCListCmd)
		}
	} else {
		// The validator has the latest VC block, check the VC block has blocks need to be sync
		missStart := int64(-1)
		start := int64(1)
		if localState.LatestHeight > 100 {
			start = localState.LatestHeight - 100 // Max save 100 blocks in local
		}

		// the vcblock height start form 1
		for i := start; i <= localState.LatestHeight; i++ {
			_, err := vm.validateChain.GetVCBlockHash(i)
			if err != nil {
				missStart = i
				break
			}
		}

		if missStart != -1 {
			if validator != nil {
				// Start sync VC block from the validator
				getVCListCmd := validatorcommand.NewMsgGetVCList(vm.Cfg.ValidatorId, missStart, localState.LatestHeight)
				//getVCListCmd.LogCommandInfo()
				validator.SendCommand(getVCListCmd)
			}
		}
	}
}

// Received get vc list command
func (vm *ValidatorManager) GetVCList(validatorId uint64, start int64, end int64) (*validatorcommand.MsgVCList, error) {
	utils.Log.Infof("[ValidatorManager]GetVCList: Start [%d] End [%d]", start, end)
	VCList := make([]*validatorcommand.VCItem, 0)
	count := 0
	for i := start; i <= end; i++ {
		hash, err := vm.validateChain.GetVCBlockHash(i)
		if err != nil {
			// generator := vm.myValidator.GetMyGenerator()
			// if generator != nil {
			// 	generator.ContinueNextSlot()
			// }
			utils.Log.Errorf("Get VC Block Hash [%d] failed: %v", i, err)
			continue
		}
		//utils.Log.Debugf("Add Height [%d] and VC Block Hash [%s] to VC List", i, hash.String())
		VCList = append(VCList, &validatorcommand.VCItem{Height: i, Hash: *hash})
		count++
		if count >= validatorcommand.MaxVCList {
			break
		}
	}
	vclistCmd := validatorcommand.NewMsgVCList(VCList)
	return vclistCmd, nil
}

// Received a vc list command
func (vm *ValidatorManager) OnVCList(vclistCmd *validatorcommand.MsgVCList, validator *validator.Validator) {
	if vclistCmd == nil || len(vclistCmd.VCList) == 0 {
		return
	}

	// Get all VC Block data from the validator
	for _, item := range vclistCmd.VCList {
		_, err := vm.validateChain.GetVCBlockHash(item.Height)
		if err != nil {
			utils.Log.Debugf("Request VC Block [%s] with Height [%d] ", item.Hash.String(), item.Height)
			// local missing VC Block, request the VC Block from the validator
			getVCBlockCmd := validatorcommand.NewMsgGetVCBlock(vm.Cfg.ValidatorId, validatorcommand.BlockType_VCBlock, item.Hash)
			validator.SendCommand(getVCBlockCmd)
		}
	}
}

// Received get vc block command
func (vm *ValidatorManager) GetVCBlock(validatorId uint64, blockType uint32, hash chainhash.Hash) (*validatorcommand.MsgVCBlock, error) {
	utils.Log.Infof("[ValidatorManager]GetVCBlock: Block type [%d] Hash [%s] from %d", blockType, hash.String(), validatorId)
	var blockData []byte
	if blockType == validatorcommand.BlockType_VCBlock {
		// Get VC Block Data
		vcBlockData, err := vm.vcStore.GetBlockData(hash[:])
		if err != nil {
			return nil, err
		}
		blockData = vcBlockData
	} else {
		// Get EP Block Data
		epBlockData, err := vm.vcStore.GetEPBlockData(hash[:])
		if err != nil {
			return nil, err
		}
		blockData = epBlockData
	}

	getVCBlockCmd := validatorcommand.NewMsgVCBlock(hash, blockType, blockData)
	return getVCBlockCmd, nil
}

// BroadcastVCBlock broadcasts a VC or EP block to connected peers based on the block type.
// It retrieves the block data from the VC store using the provided hash and block type.
// If the block type is a VC block, it fetches VC block data; otherwise, it fetches EP block data.
// The function constructs a new MsgVCBlock command with the retrieved data and broadcasts it.
// Returns an error if retrieving the block data fails.
func (vm *ValidatorManager) BroadcastVCBlock(blockType uint32, hash *chainhash.Hash) error {
	var blockData []byte
	if blockType == validatorcommand.BlockType_VCBlock {
		// Get VC Block Data
		vcBlockData, err := vm.vcStore.GetBlockData(hash[:])
		if err != nil {
			utils.Log.Errorf("Get VC Block [%s] failed: %v", hash.String(), err)
			return err
		}
		blockData = vcBlockData
	} else {
		// Get EP Block Data
		epBlockData, err := vm.vcStore.GetEPBlockData(hash[:])
		if err != nil {
			utils.Log.Errorf("Get EP Block [%s] failed: %v", hash.String(), err)
			return err
		}
		blockData = epBlockData
	}

	utils.Log.Debugf("Broadcast Block [%s] to validatechain...", hash.String())
	vcBlockCmd := validatorcommand.NewMsgVCBlock(*hash, blockType, blockData)
	vm.BroadcastCommand(vcBlockCmd)
	return nil
}

// Received a vc block command
func (vm *ValidatorManager) OnVCBlock(vcblockCmd *validatorcommand.MsgVCBlock, validator *validator.Validator) {
	utils.Log.Debugf("[ValidatorManager]OnVCBlock...")
	if vcblockCmd == nil || vcblockCmd.Payload == nil {
		utils.Log.Debugf("[ValidatorManager]Invalid vc block message.")
		return
	}

	if vcblockCmd.BlockType == validatorcommand.BlockType_VCBlock {
		utils.Log.Debugf("[ValidatorManager]New vc block [%s].", vcblockCmd.Hash.String())
		vcBlock, err := vm.validateChain.GetVCBlock(&vcblockCmd.Hash)
		if err == nil {
			utils.Log.Debugf("The Block [%s] already exists in local [%d] ", vcBlock.Header.Hash.String(), vcBlock.Header.Height)
			return
		}

		vcBlock = &validatechain.VCBlock{}
		err = vcBlock.Decode(vcblockCmd.Payload)
		if err != nil {
			utils.Log.Error(err)
			return
		}
		vcBlockHash, err := vcBlock.GetHash()
		if err != nil {
			utils.Log.Error(err)
			return
		}
		if vcBlockHash.IsEqual(&vcblockCmd.Hash) == false {
			utils.Log.Error("Block hash is invalid")
			return
		}
		// check the block is valid
		err = vm.isReceptVCBlock(vcBlock)
		if err != nil {
			utils.Log.Errorf("Block isnot recepted:%v", err)
			return
		}
		// Save VC Block data to local
		vm.SaveVCBlock(vcBlock)

	} else {
		// Save EP Block data to local
		utils.Log.Debugf("[ValidatorManager]New ep block [%s].", vcblockCmd.Hash.String())
		epBlock := &validatechain.EPBlock{}
		err := epBlock.Decode(vcblockCmd.Payload)
		if err != nil {
			utils.Log.Error(err)
			return
		}
		epBlockHash, err := epBlock.GetHash()
		if err != nil {
			utils.Log.Error(err)
			return
		}
		if epBlockHash.IsEqual(&vcblockCmd.Hash) == false {
			utils.Log.Error("Block hash is invalid")
			return
		}
		// Save EP Block data to local
		utils.Log.Debugf("[ValidatorManager]SaveEPBlock...")
		vm.validateChain.SaveEPBlock(epBlock)
	}

}

// func (vm *ValidatorManager) VoteNewEpoch(newEpoch *epoch.Epoch, reason uint32) {
// 	NewEpochVote := epoch.NewEpochVote{
// 		NewEpoch: confirmedEpoch,
// 		Reason:   reason,
// 	}
// }

func (vm *ValidatorManager) ConfirmNewEpoch(confirmedEpoch *epoch.Epoch, reason uint32, receivedEpoch map[uint64]*NewEpochVoteItem) {
	// Confirm New epoch
	// 1. save vcblock to vc store, and broadcast to vc
	// 2. broadcast the result to all validators

	vcBlock := validatechain.NewVCBlock()
	curState := vm.validateChain.GetCurrentState()

	vcBlock.Header.Height = curState.LatestHeight + 1 // height + 1
	vcBlock.Header.PrevHash = curState.LatestHash     // prev hash is current state hash
	vcBlock.Header.DataType = validatechain.DataType_NewEpoch
	dataBlock := &validatechain.DataNewEpoch{
		CreatorId:     vm.myValidator.ValidatorInfo.ValidatorId,
		PublicKey:     vm.myValidator.ValidatorInfo.PublicKey,
		EpochIndex:    confirmedEpoch.EpochIndex,
		CreateTime:    time.Now().Unix(),
		Reason:        reason,
		EpochItemList: make([]epoch.EpochItem, 0),
		EpochVoteList: make([]validatechain.EpochVoteItem, 0),
	}

	// Append EpochItemList
	for _, item := range confirmedEpoch.ItemList {
		dataBlock.EpochItemList = append(dataBlock.EpochItemList, *item)
	}

	// Append EpochVoteList
	if receivedEpoch != nil {
		for _, item := range receivedEpoch {
			if item == nil || item.Hash == nil || item.VoteData == nil {
				continue
			}
			dataBlock.EpochVoteList = append(dataBlock.EpochVoteList, validatechain.EpochVoteItem{
				ValidatorId: item.VoteData.VotorId,
				Hash:        *item.Hash,
			})
		}
	}
	vcBlock.Data = dataBlock

	err := vm.SaveVCBlock(vcBlock)
	if err != nil {
		return
	}

	confirmedEpoch.LastChangeTime = time.Unix(vcBlock.Header.CreateTime, 0) // vcBlock.Header.CreateTime
	confirmedEpoch.VCBlockHeight = vcBlock.Header.Height
	confirmedEpoch.VCBlockHash = &vcBlock.Header.Hash

	vm.BroadcastVCBlock(validatorcommand.BlockType_VCBlock, &vcBlock.Header.Hash)
}

func (vm *ValidatorManager) ConfirmDelEpoch(confirmedEpoch *epoch.Epoch, receivedResult *DelEpochMemberCollection) {
	// Confirm Del epoch
	// 1. save vcblock to vc store, and broadcast to vc
	// 2. broadcast the result to all validators

	vcBlock := validatechain.NewVCBlock()
	curState := vm.validateChain.GetCurrentState()

	vcBlock.Header.Height = curState.LatestHeight + 1 // height + 1
	vcBlock.Header.PrevHash = curState.LatestHash     // prev hash is current state hash
	vcBlock.Header.DataType = validatechain.DataType_DelEpochMember
	dataBlock := &validatechain.DataEpochDelMember{
		RequestId:           vm.myValidator.ValidatorInfo.ValidatorId,
		PublicKey:           vm.myValidator.ValidatorInfo.PublicKey,
		EpochIndex:          confirmedEpoch.EpochIndex,
		Reason:              validatechain.UpdateEpochReason_MemberRemoved,
		CreateTime:          time.Now().Unix(),
		EpochItemList:       make([]epoch.EpochItem, 0),
		EpochDelConfirmList: make([]validatechain.EpochDelConfirmItem, 0),
	}

	// Append EpochItemList
	for _, item := range confirmedEpoch.ItemList {
		dataBlock.EpochItemList = append(dataBlock.EpochItemList, *item)
	}

	// Append EpochVoteList
	for validatirId, item := range receivedResult.ResultList {
		if item == nil {
			continue
		}
		dataBlock.EpochDelConfirmList = append(dataBlock.EpochDelConfirmList, validatechain.EpochDelConfirmItem{
			ValidatorId: validatirId,
			PublicKey:   item.PublicKey,
			Result:      item.Result,
			Token:       item.Token,
		})
	}
	vcBlock.Data = dataBlock

	err := vm.SaveVCBlock(vcBlock)
	if err != nil {
		return
	}

	confirmedEpoch.LastChangeTime = time.Unix(vcBlock.Header.CreateTime, 0) // vcBlock.Header.CreateTime
	confirmedEpoch.VCBlockHeight = vcBlock.Header.Height
	confirmedEpoch.VCBlockHash = &vcBlock.Header.Hash

	vm.BroadcastVCBlock(validatorcommand.BlockType_VCBlock, &vcBlock.Header.Hash)
}

func (vm *ValidatorManager) VCBlock_MinerNewBlock(minerNewBlock *generator.MinerNewBlock) {
	// record Miner new block to validatechain
	// 1. save vcblock to vc store
	// 2. broadcast the vc block to all validators

	vcBlock := validatechain.NewVCBlock()
	curState := vm.validateChain.GetCurrentState()

	vcBlock.Header.Height = curState.LatestHeight + 1 // height + 1
	vcBlock.Header.PrevHash = curState.LatestHash     // prev hash is current state hash
	vcBlock.Header.DataType = validatechain.DataType_MinerNewBlock

	dataBlock := &validatechain.DataMinerNewBlock{
		GeneratorId:   minerNewBlock.GeneratorId,
		PublicKey:     minerNewBlock.PublicKey,
		Timestamp:     minerNewBlock.MinerTime,
		SatsnetHeight: minerNewBlock.Height,
		Hash:          *minerNewBlock.Hash,
		Token:         minerNewBlock.Token,
	}

	vcBlock.Data = dataBlock

	err := vm.SaveVCBlock(vcBlock)
	if err != nil {
		return
	}

	vm.BroadcastVCBlock(validatorcommand.BlockType_VCBlock, &vcBlock.Header.Hash)
}

func (vm *ValidatorManager) GetVCStore() *validatechaindb.ValidateChainStore {
	return vm.vcStore
}

func (vm *ValidatorManager) SaveVCBlock(vcBlock *validatechain.VCBlock) error {
	utils.Log.Debugf("[ValidatorManager]SaveVCBlock...")
	vm.saveVCBlockMtx.Lock()
	defer vm.saveVCBlockMtx.Unlock()
	// Save VC Block data to local
	err := vm.validateChain.SaveVCBlock(vcBlock)
	if err != nil {
		utils.Log.Debugf("[ValidatorManager]SaveVCBlock to DB failed : %v", err)
		return err
	}

	// Save VC Block Hash
	err = vm.validateChain.SaveVCBlockHash(int64(vcBlock.Header.Height), &vcBlock.Header.Hash)
	if err != nil {
		utils.Log.Debugf("[ValidatorManager]SaveVCBlockHash to DB failed : %v", err)
		return err
	}

	utils.Log.Debugf("[ValidatorManager]New block has Saved, Height: %d, Hash: %s.", vcBlock.Header.Height, vcBlock.Header.Hash.String())

	localState := vm.validateChain.GetCurrentState()
	if localState == nil {
		localState = &validatechain.ValidateChainState{}
	}

	utils.Log.Debugf("[ValidatorManager]Current vc state , LatestHeight: %d, LatestHash: %s, LatestEpochIndex:%d.", localState.LatestHeight, localState.LatestHash, localState.LatestEpochIndex)
	// Update Current State
	if localState.LatestHeight < int64(vcBlock.Header.Height) {
		newEpochIndex := localState.LatestEpochIndex

		vcd, ok := vcBlock.Data.(*validatechain.DataUpdateEpoch)
		if ok {
			// The Block is epoch indx, the epoch index is changed
			newEpochIndex = vcd.EpochIndex
		}

		newVCState := &validatechain.ValidateChainState{
			LatestHeight:     vcBlock.Header.Height,
			LatestHash:       vcBlock.Header.Hash,
			LatestEpochIndex: newEpochIndex,
		}
		utils.Log.Debugf("[ValidatorManager]Update new vc state , LatestHeight: %d, LatestHash: %s, LatestEpochIndex:%d.", newVCState.LatestHeight, newVCState.LatestHash, newVCState.LatestEpochIndex)
		err = vm.validateChain.UpdateCurrentState(newVCState)
		if err != nil {
			utils.Log.Debugf("[ValidatorManager]UpdateCurrentState to DB failed : %v", err)
			return err
		}

		vcd_newBlock, ok := vcBlock.Data.(*validatechain.DataMinerNewBlock)
		if ok {
			blockHash_satsnet := vcd_newBlock.Hash
			blochHeight_satsnet := vcd_newBlock.SatsnetHeight
			// Notify satsnet chain for a new block mined
			vm.Cfg.PosMiner.OnNewBlockMined(&blockHash_satsnet, blochHeight_satsnet)
		}
	}

	return nil
}

func (vm *ValidatorManager) isReceptVCBlock(vcBlock *validatechain.VCBlock) error {
	return nil
}

func (vm *ValidatorManager) checkValidatorConnectedHandler() {
	utils.Log.Debugf("[ValidatorManager]checkValidatorConnectedHandler ...")

	checkInterval := time.Second * 60
	checkTicker := time.NewTicker(checkInterval)
	defer checkTicker.Stop()

exit:
	for {
		utils.Log.Debugf("[ValidatorManager]Waiting next timer for check validator connected...")
		select {
		case <-checkTicker.C:
			vm.CheckValidatorConnected()
		case <-vm.quit:
			break exit
		}
	}

	utils.Log.Debugf("[ValidatorManager]checkValidatorConnectedHandler done.")

}

// 检查之前连接过的validator是否还连接中， 如果没有连接，则重新连接起来
func (vm *ValidatorManager) CheckValidatorConnected() {
	utils.Log.Debugf("[ValidatorManager]CheckValidatorConnected ...")

	if vm.ValidatorRecordMgr == nil {
		return
	}
	for _, record := range vm.ValidatorRecordMgr.ValidatorRecordList {
		if vm.isLocalValidator(record.Host) { // Not local validator, skip it (Remote validator is not connected, so no need to check it here)
			continue
		}
		hostIP := net.ParseIP(record.Host)
		if hostIP == nil {
			continue
		}

		now := time.Now()
		if now.Sub(record.LastConnectedTime) > time.Hour*24 {
			// The validator is not connected for 1 day, not check connected
			continue
		}

		isNewConnected := false
		validatorNode := vm.LookupValidator(hostIP)
		if validatorNode == nil {
			validatorCfg := vm.newValidatorConfig(vm.Cfg.ValidatorId, vm.Cfg.ValidatorPubKey, nil) // vm.ValidatorId is Local validator, validatorId is Remote validator when new validator connected

			addr, err := vm.getAddr(record.Host)
			if err != nil {
				continue
			}
			validatorNode, err = validator.NewValidator(validatorCfg, addr)
			if err != nil {
				utils.Log.Errorf("New Validator failed: %v", err)
				continue
			}
			//
			isNewConnected = true
		}
		if validatorNode.IsConnected() == false {
			// try Connect to the validator
			err := validatorNode.Connect()
			if err != nil {
				utils.Log.Errorf("Connect validator failed: %v", err)
				continue
			}
			validatorId := record.ValidatorId
			if validatorId == 0 && validatorNode.ValidatorInfo.ValidatorId != 0 {
				validatorId = validatorNode.ValidatorInfo.ValidatorId
			}
			// Update the validator record
			vm.ValidatorRecordMgr.UpdateValidatorRecord(validatorId, record.Host)

		}

		// The validator is connected
		if record.ValidatorId == 0 && validatorNode.ValidatorInfo.ValidatorId != 0 {
			// Update the validator record
			vm.ValidatorRecordMgr.UpdateValidatorRecord(validatorNode.ValidatorInfo.ValidatorId, record.Host)
		}

		if isNewConnected {
			// Add the validator to the connected list
			vm.AddActivieValidator(validatorNode)
		}
	}
}

func (vm *ValidatorManager) GetCurrentEpochMember(includeLocalValidator bool) ([]string, error) {
	if vm.CurrentEpoch == nil {
		return nil, errors.New("current epoch is nil")
	}

	memberList := make([]string, 0)

	for _, item := range vm.CurrentEpoch.ItemList {
		if includeLocalValidator == false && vm.isLocalValidator(item.Host) {
			utils.Log.Debugf("Validator is local validator")
			continue
		}
		memberList = append(memberList, item.Host)
	}

	return memberList, nil
}
