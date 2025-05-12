package localvalidator

import (
	"encoding/base64"
	"errors"
	"net"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/bootstrapnode"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validator"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorcommand"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorpeer"
)

type LocalValidator struct {
	validator.Validator
	validatorKey *ValidatorKey
	localPeer    *validatorpeer.LocalPeer
	myGenerator  *generator.Generator

	isBootStrapNode bool // current node is or not bootstrap node
}

func NewValidator(config *validator.Config, addrs []net.Addr) (*LocalValidator, error) {
	utils.Log.Tracef("NewValidator")
	localValidator := &LocalValidator{
		Validator: validator.Validator{
			Cfg: config,
		},
	}

	validatorKey := NewValidatorKey(localValidator.Cfg.LocalValidatorPubKey, localValidator.Cfg.ChainParams)
	localValidator.validatorKey = validatorKey

	publicKey, err := localValidator.validatorKey.GetPublicKey()
	if err != nil {
		utils.Log.Errorf("InitValidatorKey failed: %v", err)
		return nil, err
	}

	validatorInfo := validatorinfo.ValidatorInfo{
		ValidatorId: localValidator.Cfg.LocalValidatorId,
		CreateTime:  time.Now(),
	}
	copy(validatorInfo.PublicKey[:], publicKey[:])

	localValidator.UpdateValidatorInfo(&validatorInfo, validatorinfo.MaskValidatorId|validatorinfo.MaskPublicKey|validatorinfo.MaskCreateTime)

	localValidator.isBootStrapNode = bootstrapnode.IsBootStrapNode(validatorInfo.PublicKey[:])

	utils.Log.Tracef("Local validator ID: %d", localValidator.Cfg.LocalValidatorId)
	utils.Log.Tracef("Local validator PublicKey: %x", publicKey)

	// TODO Check local validator is valid or not
	// As an avaliable validator, it should be obtained validator ID from the validator committee when the validator is Pledged assets to the validator committee
	if localValidator.isValidLocalValidator() == false {
		err := errors.New("invalid validator")
		utils.Log.Errorf("Check validator failed: %v", err)
		return nil, err
	}

	peer, err := validatorpeer.NewLocalPeer(localValidator.newPeerConfig(config), addrs)
	if err != nil {
		utils.Log.Errorf("NewValidator failed: %v", err)
		return nil, err
	}
	localValidator.localPeer = peer
	utils.Log.Tracef("NewValidator success with peer: %v", peer.Addr())
	return localValidator, nil
}

// newPeerConfig returns the configuration for the given serverPeer.
func (v *LocalValidator) newPeerConfig(config *validator.Config) *validatorpeer.LocalPeerConfig {
	return &validatorpeer.LocalPeerConfig{
		LocalValidator: v,
		ChainParams:    config.ChainParams,
		Dial:           config.Dial,
		Lookup:         config.Lookup,
		ValidatorId:    v.Cfg.LocalValidatorId,
	}
}

// Addr returns the peer address.
//
// This function is safe for concurrent access.
func (v *LocalValidator) Start() {
	// Start the peer listener for local peer
	v.localPeer.Start()

	defaultConnectedAddr := v.localPeer.Addr()

	if defaultConnectedAddr == "" {
		// No any addr can be connected, return directly
		return
	}

	validatorInfo := validatorinfo.ValidatorInfo{
		CreateTime: time.Now(),
	}

	validatorHost := validatorinfo.GetAddrStringHost(defaultConnectedAddr)
	validatorInfo.Host = validatorHost

	utils.Log.Tracef("LocalValidator Started. Will update CreateTime: %v, Host: %v", validatorInfo.CreateTime.Format(time.DateTime), validatorInfo.Host)

	v.UpdateValidatorInfo(&validatorInfo, validatorinfo.MaskCreateTime|validatorinfo.MaskHost)

	utils.Log.Tracef("LocalValidator Started. validatorInfo: %v", v.ValidatorInfo)
}

// OnPeerConnected is invoked when a remote peer connects to the local peer .
func (v *LocalValidator) OnPeerConnected(addr net.Addr, validatorInfo *validatorinfo.ValidatorInfo) {
	// It will nitify the validator manager, an new validator peer is connected
	utils.Log.Tracef("[LocalValidator]Receive a new validator peer connected[%s]", addr.String())
	if v.Cfg == nil || v.Cfg.Listener == nil {
		return
	}
	v.Cfg.Listener.OnNewValidatorPeerConnected(addr, validatorInfo)
	return
}

// GetAllValidators invoke when the peer receiver GetValidators command.
func (v *LocalValidator) GetAllValidators(validatorID uint64) []*validatorinfo.ValidatorInfo {
	// Will invoke validator manager to get all validators in local
	utils.Log.Tracef("[LocalValidator]Receive GetValidators command from validator [%d]", validatorID)
	if v.Cfg == nil || v.Cfg.Listener == nil {
		return nil
	}
	validatorList := v.Cfg.Listener.GetValidatorList(validatorID)
	return validatorList
}

// GetAllValidators invoke when the peer receiver GetValidators command.
func (v *LocalValidator) GetLocalEpoch(validatorID uint64) (*epoch.Epoch, *epoch.Epoch, error) {
	// Will invoke validator manager to get all validators in local
	utils.Log.Tracef("[LocalValidator]Receive GetLocalEpoch command from validator [%d]", validatorID)
	if v.Cfg == nil || v.Cfg.Listener == nil {
		return nil, nil, errors.New("Not implement manager listener")
	}
	currentEpoch, nextEpoch, err := v.Cfg.Listener.GetLocalEpoch(validatorID)
	return currentEpoch, nextEpoch, err
}

func (v *LocalValidator) GetValidatorAddrsList() []net.Addr {
	// The address doesn't change after initialization, therefore it is not
	// protected by a mutex.
	if v.localPeer == nil {
		return nil
	}
	return v.localPeer.GetPeerAddrsList()
}

func (v *LocalValidator) isValidLocalValidator() bool {
	if v.Cfg.LocalValidatorId == 0 {
		utils.Log.Errorf("Invalid validator ID")
		return false
	}
	// Check the validator is valid or not

	return true
}

func (v *LocalValidator) OnAllValidatorsDeclare(validatorList []validatorinfo.ValidatorInfo, remoteAddr net.Addr) {
	v.Cfg.Listener.OnValidatorListUpdated(validatorList, remoteAddr)
}

// GetLocalValidatorInfo invoke when local validator info.
func (v *LocalValidator) GetLocalValidatorInfo(uint64) *validatorinfo.ValidatorInfo {
	utils.Log.Tracef("[LocalValidator]GetLocalValidatorInfo")
	utils.Log.Tracef("ValidatorId: %d", v.ValidatorInfo.ValidatorId)
	utils.Log.Tracef("PublicKey: %x", v.ValidatorInfo.PublicKey)
	utils.Log.Tracef("CreateTime: %s", v.ValidatorInfo.CreateTime.Format(time.DateTime))
	return &v.ValidatorInfo
}

func (v *LocalValidator) IsBootStrapNode() bool {
	return v.isBootStrapNode
}

func (v *LocalValidator) BecomeGenerator(height int32, handOverTime time.Time) error {
	utils.Log.Tracef("[LocalValidator]BecomeGenerator...")
	myGenerator := generator.NewGenerator(&v.ValidatorInfo, height, handOverTime.Unix(), "")

	err := myGenerator.SetHandOverTime(handOverTime)
	if err != nil {
		utils.Log.Tracef("Set miner time failed: %v", err)
		return err
	}

	tokenData := myGenerator.GetTokenData()

	utils.Log.Tracef("tokenData = %x", tokenData)

	token, err := v.CreateToken(tokenData)
	if err != nil {
		return errors.New("create token failed")
	}

	utils.Log.Tracef("token = %s", token)

	myGenerator.SetToken(token)
	v.myGenerator = myGenerator

	myGenerator.SetLocalMiner(v)
	return nil
}

func (v *LocalValidator) CreateToken(tokenData []byte) (string, error) {
	// Sign token data with validator private key
	signature, err := v.validatorKey.SignData(tokenData)
	if err != nil {
		utils.Log.Errorf("Sign token failed: %v", err)
		return "", err
	}
	token := base64.StdEncoding.EncodeToString(signature)
	return token, nil
}

// Get My Generator , if local validators isnot a generator, it will return nil
func (v *LocalValidator) GetMyGenerator() *generator.Generator {
	return v.myGenerator
}

// Get My Generator , if local validators isnot a generator, it will return nil
func (v *LocalValidator) ClearMyGenerator() {
	v.myGenerator = nil
}

// Get Generator info in local, it should be saved in manager
func (v *LocalValidator) GetGenerator(validatorId uint64) *generator.Generator {
	utils.Log.Tracef("[LocalValidator]GetGenerator from <%d>...", validatorId)
	generator := v.Cfg.Listener.GetGenerator()
	return generator
}

func (v *LocalValidator) OnTimeGenerateBlock() (*chainhash.Hash, int32, error) {
	utils.Log.Tracef("[LocalValidator]OnTimeGenerateBlock")

	return v.Cfg.Listener.OnTimeGenerateBlock()
}

func (v *LocalValidator) ContinueNextSlot() {
	if v.myGenerator != nil {
		v.myGenerator.ContinueNextSlot()
	}
}

func (v *LocalValidator) OnHandOverGenerator(handOverInfo generator.GeneratorHandOver, remoteAddr net.Addr) {
	v.Cfg.Listener.OnGeneratorHandOver(&handOverInfo, remoteAddr)
}

// Req new epoch from remote peer
func (v *LocalValidator) ReqNewEpoch(validatorID uint64, epochIndex int64, reason uint32) (*chainhash.Hash, error) {

	return v.Cfg.Listener.ReqNewEpoch(validatorID, epochIndex, reason)
}

// Received a confirm epoch command
func (v *LocalValidator) OnConfirmEpoch(epoch *epoch.Epoch, remoteAddr net.Addr) {

	v.Cfg.Listener.OnConfirmEpoch(epoch, remoteAddr)
}

// Handover to next epoch
func (v *LocalValidator) OnNextEpoch(handoverEpoch *epoch.HandOverEpoch) {

	v.Cfg.Listener.OnNextEpoch(handoverEpoch)
}

// Notify current epoch is updated
func (v *LocalValidator) OnUpdateEpoch(updatedEpoch *epoch.Epoch) {

	v.Cfg.Listener.OnUpdateEpoch(updatedEpoch)
}

// Received a Del epoch member command
func (v *LocalValidator) ConfirmDelEpochMember(reqDelEpochMember *validatorcommand.MsgReqDelEpochMember, remoteAddr net.Addr) *epoch.DelEpochMember {

	return v.Cfg.Listener.ConfirmDelEpochMember(reqDelEpochMember, remoteAddr)
}

// Received a notify handover command
func (v *LocalValidator) OnNotifyHandover(validatorId uint64, remoteAddr net.Addr) {
	utils.Log.Tracef("[LocalValidator]OnNotifyHandover from %d", validatorId)

	v.Cfg.Listener.OnNotifyHandover(validatorId)
}

// Received get vc state command
func (v *LocalValidator) GetVCState(validatorId uint64) (*validatorcommand.MsgVCState, error) {
	return v.Cfg.Listener.GetVCState(validatorId)
}

// Received get vc list command
func (v *LocalValidator) GetVCList(validatorId uint64, start int64, end int64) (*validatorcommand.MsgVCList, error) {
	return v.Cfg.Listener.GetVCList(validatorId, start, end)
}

// Received get vc block command
func (v *LocalValidator) GetVCBlock(validatorId uint64, blockType uint32, hash chainhash.Hash) (*validatorcommand.MsgVCBlock, error) {
	return v.Cfg.Listener.GetVCBlock(validatorId, blockType, hash)
}

// Received a vc block command
func (v *LocalValidator) OnVCBlock(vcblock *validatorcommand.MsgVCBlock, remoteAddr net.Addr) {
	v.Cfg.Listener.OnVCBlock(vcblock, nil)
}
