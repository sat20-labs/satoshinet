package validator

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorcommand"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorpeer"
)

type ValidatorListener interface {
	// An new validator peer is connected
	OnNewValidatorPeerConnected(net.Addr, *validatorinfo.ValidatorInfo)

	// An validator peer is disconnected
	OnValidatorPeerDisconnected(*Validator)

	// An validator peer is inactive
	OnValidatorPeerInactive(netAddr net.Addr)

	// Current validator list is updated
	OnValidatorInfoUpdated(*validatorinfo.ValidatorInfo, net.Addr)

	// Current validator list is updated
	OnValidatorListUpdated([]validatorinfo.ValidatorInfo, net.Addr)

	// Get current validator list in record this peer
	GetValidatorList(uint64) []*validatorinfo.ValidatorInfo

	// Current Epoch is updated
	OnEpochSynced(*epoch.Epoch, *epoch.Epoch, net.Addr)

	// Get current epoch info in record this peer
	GetLocalEpoch(uint64) (*epoch.Epoch, *epoch.Epoch, error)

	// Req new epoch from remote peer
	ReqNewEpoch(uint64, int64, uint32) (*chainhash.Hash, error)

	// OnNextEpoch from remote peer
	OnNextEpoch(*epoch.HandOverEpoch)

	// Current epoch is updated
	OnUpdateEpoch(*epoch.Epoch)

	// Current generator is updated
	OnGeneratorUpdated(*generator.Generator, uint64)

	// New epoch command is received
	OnNewEpoch(uint64, *chainhash.Hash)

	// Current generator is updated
	OnGeneratorHandOver(*generator.GeneratorHandOver, net.Addr)

	// Get current generator info in record this peer
	GetGenerator() *generator.Generator

	// Get local validator info in record this peer
	GetLocalValidatorInfo() *validatorinfo.ValidatorInfo

	// OnTimeGenerateBlock is invoke when time to generate block.
	OnTimeGenerateBlock() (*chainhash.Hash, int32, error)

	// OnConfirmEpoch is invoke when received a confirm epoch command
	OnConfirmEpoch(*epoch.Epoch, net.Addr)

	// Received a Del epoch member command
	ConfirmDelEpochMember(*validatorcommand.MsgReqDelEpochMember, net.Addr) *epoch.DelEpochMember

	// Received a confirmed del epoch member command
	OnConfirmedDelEpochMember(*epoch.DelEpochMember)

	// Received a notify handover command
	OnNotifyHandover(uint64)

	// Received get vc state command
	GetVCState(uint64) (*validatorcommand.MsgVCState, error)

	// Received a vc state command
	OnVCState(*validatorcommand.MsgVCState, *Validator)

	// Received get vc list command
	GetVCList(uint64, int64, int64) (*validatorcommand.MsgVCList, error)

	// Received a vc list command
	OnVCList(*validatorcommand.MsgVCList, *Validator)

	// Received get vc block command
	GetVCBlock(uint64, uint32, chainhash.Hash) (*validatorcommand.MsgVCBlock, error)

	// Received a vc block command
	OnVCBlock(*validatorcommand.MsgVCBlock, *Validator)
}

// Config is the struct to hold configuration options useful to Validator.
type Config struct {
	LocalValidatorId uint64
	LocalValidatorPubKey []byte
	//RemoteValidatorId uint64 // Just remote validator id will be used
	RemoteValidatorInfo *validatorinfo.ValidatorInfo
	// The listener for process message from/to this validator peer
	Listener ValidatorListener // ChainParams identifies which chain parameters the cpu miner is
	// associated with.
	ChainParams *chaincfg.Params
	BtcdDir     string

	// Dial connects to the address on the named network. It cannot be nil.
	Dial   func(net.Addr) (net.Conn, error)
	Lookup func(string) ([]net.IP, error)
}

type Validator struct {
	ValidatorInfo validatorinfo.ValidatorInfo
	infoMtx       sync.Mutex // protects the validator info

	//	PublicKey []byte
	//  ValidatorId     uint64
	//  CreateTime time.Time
	//	ActivitionCount int
	//  GeneratorCount int
	//	DiscountCount   int
	//	FaultCount      int
	IsActivition bool
	IsGenerator  bool
	//GeneratorInfo Generator
	//	ValidatorScore  int
	Cfg *Config

	peer             *validatorpeer.RemotePeer
	isLocalValidator bool
}

func NewValidator(config *Config, addr net.Addr) (*Validator, error) {
	utils.Log.Debugf("NewValidator")
	validator := &Validator{
		Cfg: config,
	}
	peer, err := validatorpeer.NewRemotePeer(validator.newPeerConfig(config), addr)
	if err != nil {
		utils.Log.Errorf("NewValidator failed: %v", err)
		return nil, err
	}
	peerHost := validatorinfo.GetAddrHost(addr)
	validator.peer = peer
	validator.ValidatorInfo.Host = peerHost.String()
	utils.Log.Debugf("NewValidator success with peer: %v", peer.Addr())
	utils.Log.Debugf("NewValidator Host: %s", validator.ValidatorInfo.Host)
	if config.RemoteValidatorInfo != nil {
		validator.ValidatorInfo.ValidatorId = config.RemoteValidatorInfo.ValidatorId
		validator.ValidatorInfo.PublicKey = config.RemoteValidatorInfo.PublicKey
		validator.ValidatorInfo.CreateTime = config.RemoteValidatorInfo.CreateTime
	}

	utils.Log.Debugf("new validator info : ")
	utils.Log.Debugf("ValidatorId: %d", validator.ValidatorInfo.ValidatorId)
	utils.Log.Debugf("PublicKey: %x", validator.ValidatorInfo.PublicKey)
	utils.Log.Debugf("CreateTime: %s", validator.ValidatorInfo.CreateTime.Format(time.DateTime))
	utils.Log.Debugf("Host: %s", validator.ValidatorInfo.Host)
	utils.Log.Debugf("--------------------------------------------------")

	return validator, nil
}

// newPeerConfig returns the configuration for the given serverPeer.
func (v *Validator) newPeerConfig(config *Config) *validatorpeer.RemotePeerConfig {

	return &validatorpeer.RemotePeerConfig{
		RemoteValidatorListener: v,
		ChainParams:             config.ChainParams,
		Dial:                    config.Dial,
		Lookup:                  config.Lookup,
		LocalValidatorId:        v.Cfg.LocalValidatorId,
		RemoteValidatorId:       v.ValidatorInfo.ValidatorId,
	}
}

// String returns the validator's info
// string.
//
// This function is safe for concurrent access.
func (v *Validator) String() string {
	addr := ""
	if v.peer == nil {
		//return "Remote Validator:not connected"
		addr = "not connected"
	} else {
		addr = v.peer.Addr()
	}
	v.infoMtx.Lock()
	validatorId := v.ValidatorInfo.ValidatorId
	v.infoMtx.Unlock()
	return fmt.Sprintf("Remote Validator ID: %d, Addr:%s", validatorId, addr)
}

// Addr returns the peer address.
//
// This function is safe for concurrent access.
func (v *Validator) GetValidatorAddr() net.Addr {
	// The address doesn't change after initialization, therefore it is not
	// protected by a mutex.
	if v.peer == nil {
		return nil
	}
	return v.peer.GetPeerAddr()
}

func (v *Validator) IsValidatorPubKey(pubkey []byte) bool {
	// The address doesn't change after initialization, therefore it is not
	// protected by a mutex.
	if v.peer == nil {
		return false
	}

	return bytes.Equal(v.ValidatorInfo.PublicKey[:], pubkey)
}

func (v *Validator) RequestAllValidatorsInfo() error {
	if v.peer == nil {
		err := errors.New("invalid peer for the validator")
		utils.Log.Errorf("GetAllValidatorsInfo failed: %v", err)
		return err
	}

	//v.peer.RequestAllValidatorsInfo()

	// Generate RequestAllValidatorsInfo command to payload, and send it to peer

	return nil
}

// func (v *Validator) Reconnect() bool {
// 	utils.Log.Debugf("Received a reconnected notify")
// 	// CHeck current peer is connected
// 	if v.peer == nil {
// 		// No any activie peer,cannot be reconnected
// 		return false
// 	}

// 	if v.peer.Connected() == false {
// 		// current peer is not connected, will connect it
// 		utils.Log.Debugf("Will connect to the validator: %v", v.peer.Addr())
// 		err := v.peer.Connect()
// 		if err != nil {
// 			utils.Log.Errorf("Connect failed: %v", err)
// 			return false
// 		}
// 	}
// 	if v.Cfg.RemoteValidatorInfo == nil {
// 		// Not get remote validator id
// 		utils.Log.Debugf("The validator Id is invalid, will request validator info from the remote peer")
// 		validatorInfo := v.GetLocalValidatorInfo(0)
// 		v.peer.RequestValidatorId(validatorInfo)
// 	}
// 	return true
// }

func (v *Validator) Connect() error {
	if v.peer == nil {
		err := errors.New("invalid peer for the validator")
		utils.Log.Errorf("Start validator [%s] failed: %v", v.String(), err)
		return err
	}

	if !v.peer.Connected() {
		utils.Log.Debugf("Will Connect to the validator: %v", v.peer.Addr())
		err := v.peer.Connect()
		if err != nil {
			utils.Log.Errorf("Connect failed: %v", err)
			return err
		}
		v.ValidatorInfo.Host = v.peer.RemoteAddr().String()
	}

	if !v.IsValidInfo() {
		// Not get remote validator id
		utils.Log.Debugf("The validator Id is invalid, will request validator info from the remote peer")
		validatorInfo := v.GetLocalValidatorInfo(0)
		v.peer.RequestValidatorId(validatorInfo)

		go v.checkValidatorValid()
	}

	return nil
}

func (v *Validator) Stop() {
	if v.peer == nil {
		return
	}
	v.peer.Disconnect()
}

func (v *Validator) IsValidInfo() bool {

	if v.ValidatorInfo.ValidatorId == 0 {
		return false
	}
	if v.ValidatorInfo.CreateTime.IsZero() {
		return false
	}
	if v.ValidatorInfo.Host == "" {
		return false
	}
	return true
}

func (v *Validator) checkValidatorValid() {
	utils.Log.Debugf("[Validator]checkValidatorValid started.")
	tryCount := 0
	checkInterval := time.Second * 5
	checkTicker := time.NewTicker(checkInterval)
	defer checkTicker.Stop()

exit:
	for {
		utils.Log.Debugf("[ValidatorManager]Waiting next timer for check validator valid...")
		<-checkTicker.C
		if v.IsValidInfo() == false {
			tryCount++
			if tryCount > 5 {
				// The validator info is invalid, will disconnect the peer
				v.Stop()
				break exit
			}
			validatorInfo := v.GetLocalValidatorInfo(0)
			v.peer.RequestValidatorId(validatorInfo)
		} else {
			// Validator info is valid, will stop the check
			break exit
		}
	}

	utils.Log.Debugf("[Validator]checkValidatorValid done.")
}

// This function is safe for concurrent access.
func (v *Validator) SyncAllValidators() error {
	if v.peer == nil {
		err := errors.New("invalid peer for the validator")
		utils.Log.Errorf("SyncAllValidator failed: %v", err)
		return err
	}

	if v.peer.Connected() == false {
		err := errors.New("validator peer isnot connected")
		utils.Log.Errorf("SyncAllValidator failed: %v", err)
		return err
	}

	utils.Log.Debugf("Will request get all validators to the validator: %s", v.peer.Addr())
	err := v.peer.RequestGetValidators()
	if err != nil {
		utils.Log.Errorf("RequestGetValidators failed: %v", err)
		return err
	}
	return nil
}

func (v *Validator) GetEpoch() error {
	if v.peer == nil {
		err := errors.New("invalid peer for the validator")
		utils.Log.Errorf("GetEpoch failed: %v", err)
		return err
	}

	if v.peer.Connected() == false {
		err := errors.New("validator peer isnot connected")
		utils.Log.Errorf("GetEpoch failed: %v", err)
		return err
	}

	utils.Log.Debugf("Will request get epoch to the validator: %s", v.peer.Addr())
	err := v.peer.RequestGetEpoch()
	if err != nil {
		utils.Log.Errorf("RequestGetEpoch failed: %v", err)
		return err
	}
	return nil
}

func (v *Validator) GetGenerator() error {
	if v.peer == nil {
		err := errors.New("invalid peer for the validator")
		utils.Log.Errorf("GetGenerator failed: %v", err)
		return err
	}

	if v.peer.Connected() == false {
		err := errors.New("validator peer isnot connected")
		utils.Log.Errorf("GetGenerator failed: %v", err)
		return err
	}

	utils.Log.Debugf("Will request get generator to the validator: %s", v.peer.Addr())
	err := v.peer.RequestGetGenerator()
	if err != nil {
		utils.Log.Errorf("RequestGetGenerator failed: %v", err)
		return err
	}
	return nil
}

// This function is safe for concurrent access.
func (v *Validator) SetLocalValidator() {
	v.isLocalValidator = true
}

// This function is safe for concurrent access.
func (v *Validator) GetValidatorId() uint64 {
	return v.ValidatorInfo.ValidatorId
}

// Addr returns the peer address.
//
// This function is safe for concurrent access.
func (v *Validator) IsConnected() bool {
	if v.peer == nil {
		return false
	}
	return v.peer.Connected()
}

func (v *Validator) SendCommand(command validatorcommand.Message) error {
	if v.peer == nil {
		err := errors.New("invalid peer for the validator")
		utils.Log.Errorf("SendCommand failed: %v", err)
		return err
	}

	if v.peer.Connected() == false {
		err := errors.New("validator peer isnot connected")
		utils.Log.Errorf("SendCommand failed: %v", err)
		return err
	}

	utils.Log.Debugf("Will send command %s to the validator: %s", command.Command(), v.peer.Addr())
	return v.peer.SendCommand(command)
}

func (v *Validator) GetLastReceived() time.Time {
	if v.peer == nil {
		return time.Time{}
	}
	return v.peer.LastRecv()
}

// OnPeerDisconnected is invoked when a remote peer connects to the local peer .
func (v *Validator) OnPeerDisconnected(addr net.Addr) {
	if v.Cfg == nil || v.Cfg.Listener == nil {
		return
	}

	v.Cfg.Listener.OnValidatorPeerDisconnected(v)
	return
}

func (v *Validator) OnValidatorInfoUpdated(validatorInfo *validatorinfo.ValidatorInfo, changeMask validatorinfo.ValidatorInfoMask) {
	// v.Cfg.RemoteValidatorId = peerInfo.ValidatorId

	// utils.Log.Debugf("validator id updated: %d", v.Cfg.RemoteValidatorId)

	v.UpdateValidatorInfo(validatorInfo, changeMask)

	v.Cfg.Listener.OnValidatorInfoUpdated(&v.ValidatorInfo, v.GetValidatorAddr())
}

func (v *Validator) UpdateValidatorInfo(validatorInfo *validatorinfo.ValidatorInfo, changeMask validatorinfo.ValidatorInfoMask) {

	v.infoMtx.Lock()
	defer v.infoMtx.Unlock()

	utils.Log.Debugf("validator info will be updated: ")
	utils.Log.Debugf("changeMask: %x", changeMask)
	utils.Log.Debugf("ValidatorId: %d", validatorInfo.ValidatorId)
	utils.Log.Debugf("PublicKey: %x", validatorInfo.PublicKey)
	utils.Log.Debugf("CreateTime: %s", validatorInfo.CreateTime.Format(time.DateTime))
	utils.Log.Debugf("Host: %s", validatorInfo.Host)
	utils.Log.Debugf("--------------------------------------------------")

	if changeMask&validatorinfo.MaskValidatorId != 0 {
		v.ValidatorInfo.ValidatorId = validatorInfo.ValidatorId
	}
	if changeMask&validatorinfo.MaskPublicKey != 0 {
		//v.ValidatorInfo.PublicKey = bytes.Clone(validatorInfo.PublicKey)
		copy(v.ValidatorInfo.PublicKey[:], validatorInfo.PublicKey[:])
	}
	if changeMask&validatorinfo.MaskActivitionCount != 0 {
		v.ValidatorInfo.ActivitionCount = validatorInfo.ActivitionCount
	}
	if changeMask&validatorinfo.MaskGeneratorCount != 0 {
		v.ValidatorInfo.GeneratorCount = validatorInfo.GeneratorCount
	}
	if changeMask&validatorinfo.MaskDiscountCount != 0 {
		v.ValidatorInfo.DiscountCount = validatorInfo.DiscountCount
	}
	if changeMask&validatorinfo.MaskFaultCount != 0 {
		v.ValidatorInfo.FaultCount = validatorInfo.FaultCount
	}
	if changeMask&validatorinfo.MaskCreateTime != 0 {
		v.ValidatorInfo.CreateTime = validatorInfo.CreateTime
	}
	if changeMask&validatorinfo.MaskHost != 0 {
		v.ValidatorInfo.Host = validatorInfo.Host
	}

	utils.Log.Debugf("validator info updated: ")
	utils.Log.Debugf("ValidatorId: %d", v.ValidatorInfo.ValidatorId)
	utils.Log.Debugf("PublicKey: %x", v.ValidatorInfo.PublicKey)
	utils.Log.Debugf("CreateTime: %s", v.ValidatorInfo.CreateTime.Format(time.DateTime))
	utils.Log.Debugf("Host: %s", v.ValidatorInfo.Host)
	utils.Log.Debugf("--------------------------------------------------")
}

func (v *Validator) SyncVaildatorInfo(validatorInfo *validatorinfo.ValidatorInfo) {
	v.UpdateValidatorInfo(validatorInfo, validatorinfo.MaskAll)
}

func (v *Validator) LogCurrentStats() {
	// Log validator info
	utils.Log.Debugf("validator ID: %d", v.ValidatorInfo.ValidatorId)
	utils.Log.Debugf("validator Public: %x", v.ValidatorInfo.PublicKey[:])
	utils.Log.Debugf("validator Host: %s", v.ValidatorInfo.Host)
	utils.Log.Debugf("validator CreateTime: %s", v.ValidatorInfo.CreateTime.Format("2006-01-02 15:04:05"))

	//Log validator stats
	if v.peer == nil {
		utils.Log.Debugf("validator peer is nil")
		return
	}
	v.peer.LogConnStats()
	// utils.Log.Debugf("validator LastSend: %s", v.peer.LastSend().Format("2006-01-02 15:04:05"))
	// utils.Log.Debugf("validator LastRecv: %s", v.peer.LastRecv().Format("2006-01-02 15:04:05"))
	// utils.Log.Debugf("validator LastPingTime: %s", v.peer.LastPingTime().Format("2006-01-02 15:04:05"))
	// utils.Log.Debugf("validator LastPingNonce: %d", v.peer.LastPingNonce())
	// utils.Log.Debugf("validator LastPingMicros: %d", v.peer.LastPingMicros())
}

// GetLocalValidatorInfo invoke when local validator info.
func (v *Validator) GetLocalValidatorInfo(uint64) *validatorinfo.ValidatorInfo {
	validatorInfo := v.Cfg.Listener.GetLocalValidatorInfo()

	// utils.Log.Debugf("[Validator]GetLocalValidatorInfo")
	// utils.Log.Debugf("ValidatorId: %d", validatorInfo.ValidatorId)
	// utils.Log.Debugf("PublicKey: %x", validatorInfo.PublicKey)
	// utils.Log.Debugf("CreateTime: %s", validatorInfo.CreateTime.Format(time.DateTime))
	return validatorInfo

}

// OnAllValidatorsResponse is invoked when a remote peer response all validators.
func (v *Validator) OnAllValidatorsResponse(validatorList []validatorinfo.ValidatorInfo) {

	v.Cfg.Listener.OnValidatorListUpdated(validatorList, v.peer.GetPeerAddr())
}

// OnEpochResponse is invoked when a remote peer response epoch list.
func (v *Validator) OnEpochResponse(currentEpoch *epoch.Epoch, nextEpoch *epoch.Epoch) {

	v.Cfg.Listener.OnEpochSynced(currentEpoch, nextEpoch, v.peer.GetPeerAddr())
}

func (v *Validator) OnGeneratorResponse(generatorInfo *generator.Generator) {
	v.Cfg.Listener.OnGeneratorUpdated(generatorInfo, v.ValidatorInfo.ValidatorId)
}

func (v *Validator) OnNewEpoch(validatorId uint64, hash *chainhash.Hash) {
	v.Cfg.Listener.OnNewEpoch(validatorId, hash)
}

func (v *Validator) OnConfirmedDelEpochMember(delEpochMember *epoch.DelEpochMember) {

	v.Cfg.Listener.OnConfirmedDelEpochMember(delEpochMember)
}

// Received a vc state command
func (v *Validator) OnVCState(vsState *validatorcommand.MsgVCState, remoteAddr net.Addr) {
	v.Cfg.Listener.OnVCState(vsState, v)
}

// Received a vc list command
func (v *Validator) OnVCList(vclist *validatorcommand.MsgVCList, remoteAddr net.Addr) {
	v.Cfg.Listener.OnVCList(vclist, v)
}

// Received a vc block command
func (v *Validator) OnVCBlock(vcblock *validatorcommand.MsgVCBlock, remoteAddr net.Addr) {
	v.Cfg.Listener.OnVCBlock(vcblock, v)
}
