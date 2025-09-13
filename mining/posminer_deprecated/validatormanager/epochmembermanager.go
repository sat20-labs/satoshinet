package validatormanager

import (
	"net"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/validator"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/validatorcommand"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	EpochReconnectMaxTimes = 5
)

// EpochMemberManager是一个管理当前Epoch除了自己之外的成员的管理器
// 1. 监听成员是否离线
// 2. 离线成员在一定时间内尝试重新连接，多次重新连接失败后可以申请剔除
// 2. 管理是否需要剔除成员
// 3. 处理成员被剔除

type DisconnectEpochMember struct {
	Validator      *validator.Validator
	ReconnectTimes int
}

type DelEpochMemberResult struct {
	ValidatorId string
	Result      uint32
	Token       string
}

type DelEpochMemberCollection struct {
	ResultList map[string]*DelEpochMemberResult
	StartTime  time.Time
}

type EpochMemberManager struct {
	ValidatorMgr     *ValidatorManager
	CurrentEpoch     *epoch.Epoch
	ConnectedList    map[string]*validator.Validator
	connectedListMtx sync.RWMutex

	DisconnectedList    map[string]*DisconnectEpochMember
	disconnectedListMtx sync.RWMutex

	receivedDelEpochMemberResult map[string]*DelEpochMemberCollection
}

func CreateEpochMemberManager(validatorMgr *ValidatorManager) *EpochMemberManager {
	return &EpochMemberManager{
		ValidatorMgr:                 validatorMgr,
		disconnectedListMtx:          sync.RWMutex{},
		connectedListMtx:             sync.RWMutex{},
		receivedDelEpochMemberResult: make(map[string]*DelEpochMemberCollection),
	}
}

func (em *EpochMemberManager) UpdateCurrentEpoch(currentEpoch *epoch.Epoch) {
	em.CurrentEpoch = currentEpoch

	em.updateValidatorsList()
}

func (em *EpochMemberManager) GetEpochMember(validatorID string) (*validator.Validator, bool) {

	utils.Log.Tracef("connectedListMtx4 Locked")
	em.connectedListMtx.RLock()
	defer func() {
		em.connectedListMtx.RUnlock()
		utils.Log.Tracef("connectedListMtx4 Unocked")
	}()

	if em.ConnectedList != nil {
		validatorItem, ok := em.ConnectedList[validatorID]
		if ok {
			return validatorItem, true
		}
	}

	utils.Log.Tracef("disconnectedListMtx1 Locked")
	em.disconnectedListMtx.RLock()
	defer func() {
		defer em.disconnectedListMtx.RUnlock()
		utils.Log.Tracef("disconnectedListMtx1 Unocked")
	}()

	if em.DisconnectedList != nil {
		disconnectItem, ok := em.DisconnectedList[validatorID]
		if ok {
			return disconnectItem.Validator, false
		}
	}

	return nil, false
}

func (em *EpochMemberManager) updateValidatorsList() {

	utils.Log.Tracef("[EpochMemberManager]Update validators list...")
	// 将原来的数据清空
	utils.Log.Tracef("connectedListMtx5 Locked")
	em.connectedListMtx.Lock()
	defer func() {
		em.connectedListMtx.Unlock()
		utils.Log.Tracef("connectedListMtx5 Unocked")
	}()

	utils.Log.Tracef("disconnectedListMtx2 Locked")
	em.disconnectedListMtx.Lock()
	defer func() {
		defer em.disconnectedListMtx.Unlock()
		utils.Log.Tracef("disconnectedListMtx2 Unocked")
	}()

	em.ConnectedList = make(map[string]*validator.Validator)
	em.DisconnectedList = make(map[string]*DisconnectEpochMember)

	utils.Log.Tracef("[EpochMemberManager]Old validators list has cleared.")

	if em.CurrentEpoch == nil {
		// 当前没有需要管理的epoch
		utils.Log.Tracef("[EpochMemberManager]Empty current epoch, not to be managed.")
		return
	}

	for _, epochItem := range em.CurrentEpoch.ItemList {
		if epochItem.ValidatorId == em.ValidatorMgr.Cfg.ValidatorId {
			// is local validator
			continue
		}
		validatorItem := em.ValidatorMgr.FindRemoteValidator(epochItem.ValidatorId)
		if validatorItem == nil {
			// 没有找到对应的validator, 需要将这个成员按照离线处理
			// Add the validator
			validatorCfg := em.ValidatorMgr.newValidatorConfig(em.ValidatorMgr.Cfg.ValidatorId, nil)

			addr, err := em.ValidatorMgr.getAddr(epochItem.Host)
			if err != nil {
				continue
			}
			validatorNew, err := validator.NewValidator(validatorCfg, addr)
			if err != nil {
				utils.Log.Errorf("New Validator failed: %v", err)
				continue
			}
			em.DisconnectedList[epochItem.ValidatorId] = &DisconnectEpochMember{
				Validator:      validatorNew,
				ReconnectTimes: 0,
			}
		} else {
			em.ConnectedList[epochItem.ValidatorId] = validatorItem
		}
	}

	if len(em.DisconnectedList) > 0 {
		// Disconnected list is not empty, try to reconnect
		go em.reconnectEpochHandler()
	}
	utils.Log.Tracef("[EpochMemberManager]Update validators list Done.")
}

func (em *EpochMemberManager) OnValidatorDisconnected(validatorID string) {

	utils.Log.Tracef("[EpochMemberManager]A epoch member is disconnected: %d", validatorID)

	utils.Log.Tracef("connectedListMtx6 Locked")
	em.connectedListMtx.Lock()
	defer func() {
		em.connectedListMtx.Unlock()
		utils.Log.Tracef("connectedListMtx6 Unocked")
	}()

	if em.ConnectedList == nil || em.DisconnectedList == nil {
		utils.Log.Errorf("[EpochMemberManager]Invalid epoch manager for em.ConnectedList = %v or em.DisconnectedList = %v", em.ConnectedList, em.DisconnectedList)
		return
	}

	validatorItem, ok := em.ConnectedList[validatorID]
	if !ok {
		// The validator is not in connected list
		utils.Log.Tracef("[EpochMemberManager]The disconnected epoch member isnot in connected list, nothing to do: %d", validatorID)
		return
	}

	utils.Log.Tracef("[EpochMemberManager]The %d will be removed from connected list, and added to disconnected list", validatorID)

	// remove it from connected list
	delete(em.ConnectedList, validatorID)

	utils.Log.Tracef("disconnectedListMtx3 Locked")
	em.disconnectedListMtx.Lock()
	defer func() {
		defer em.disconnectedListMtx.Unlock()
		utils.Log.Tracef("disconnectedListMtx3 Unocked")
	}()

	// and add it to disconnected list
	em.DisconnectedList[validatorID] = &DisconnectEpochMember{
		Validator:      validatorItem,
		ReconnectTimes: 0,
	}

	if len(em.DisconnectedList) > 0 {
		// Disconnected list is not empty, try to reconnect
		go em.reconnectEpochHandler()
	}
}

// reconnectEpochHandler for reconnect epoch member when a epoch member is disconnected on a timer
func (em *EpochMemberManager) reconnectEpochHandler() {
	utils.Log.Tracef("[EpochMemberManager]reconnectEpochHandler ...")
	reconnectInterval := time.Second * 1
	reconnectTicker := time.NewTicker(reconnectInterval)
	defer reconnectTicker.Stop()

exit:
	for {
		utils.Log.Tracef("[EpochMemberManager]Waiting next timer for reconnect disconnected epoch member...")
		select {
		case <-reconnectTicker.C:
			isExit := em.reconnectEpochMember()
			if isExit {
				break exit
			}
		}
	}

	utils.Log.Tracef("[EpochMemberManager]reconnectEpochHandler done.")
}

func (em *EpochMemberManager) reconnectEpochMember() bool {

	utils.Log.Tracef("disconnectedListMtx4 Locked")
	em.disconnectedListMtx.Lock()
	disConnectedList := em.DisconnectedList
	em.disconnectedListMtx.Unlock()
	utils.Log.Tracef("disconnectedListMtx4 Unocked")

	if disConnectedList == nil {
		return true
	}

	for validatorID, validatorItem := range disConnectedList {
		utils.Log.Tracef("[EpochMemberManager]Try to reconnect validator: %d...", validatorID)

		if validatorItem.Validator.IsConnected() == false {
			err := validatorItem.Validator.Connect()
			if err != nil {
				validatorItem.ReconnectTimes++ // reconnect times + 1
				utils.Log.Errorf("[EpochMemberManager]Connect validator failed : %v [%d]", err, validatorItem.ReconnectTimes)

				if validatorItem.ReconnectTimes > EpochReconnectMaxTimes {
					// 已经确认离线，不再尝试重连
					delete(disConnectedList, validatorID)

					// 得到已经离线的成员的POS
					posGenerator := em.CurrentEpoch.GetCurGeneratorPos()
					posDisconnected := em.CurrentEpoch.GetValidatorPos(validatorID)
					if posDisconnected < posGenerator {
						// 离线成员已经出块，不需要从Epoch去剔除成员
						continue
					}

					// 有离线的下一个成员来负责发起剔除成员的请求， 如果离线的成员是最后一个成员，则有它的上一个成员来负责发起剔除成员的请求
					reqPos := posDisconnected + 1

					memCount := len(em.CurrentEpoch.ItemList)

					if posDisconnected == int32(memCount-1) {
						reqPos = posDisconnected - 1
					}

					if reqPos < 0 {
						continue
					}

					if em.CurrentEpoch.ItemList[reqPos].ValidatorId == em.ValidatorMgr.Cfg.ValidatorId {
						// 需要发起剔除成员的请求的成员是自己， 则申请剔除离线成员
						utils.Log.Tracef("[EpochMemberManager]Request to delete validator from epoch list: %d...", validatorID)

						if em.ValidatorMgr.myValidator.IsBootStrapNode() {
							// 在多次尝试重连失败后，需要剔除成员
							em.ReqDelEpochMember(validatorID)
						}
						continue
					}
				}
				continue
			}
		}
		// The validator is connected, remove it from disconnected list
		utils.Log.Tracef("[EpochMemberManager]Reconnected to validator: %d...", validatorID)

		// remove it from connected list
		delete(disConnectedList, validatorID)

		utils.Log.Tracef("connectedListMtx8 Locked")
		em.connectedListMtx.Lock()
		// and add it to disconnected list
		em.ConnectedList[validatorID] = validatorItem.Validator
		em.connectedListMtx.Unlock()
		utils.Log.Tracef("connectedListMtx8 Unocked")

	}

	utils.Log.Tracef("disconnectedListMtx8 Locked")
	em.disconnectedListMtx.Lock()
	em.DisconnectedList = disConnectedList
	em.disconnectedListMtx.Unlock()
	utils.Log.Tracef("disconnectedListMtx8 Unocked")

	// Disconnected list is empty, exit
	if len(em.DisconnectedList) == 0 {
		return true
	}
	return false
}

func (em *EpochMemberManager) ReqDelEpochMember(delValidatorID string) {
	CmdReqDelEpochMember := validatorcommand.NewMsgReqDelEpochMember(em.ValidatorMgr.Cfg.ValidatorId,
		validatorcommand.CmdDelEpochMemberTarget_Consult,
		delValidatorID,
		epoch.DelCode_Disconnect,
		em.CurrentEpoch.EpochIndex)
	// vm.newEpochMgr = CreateNewEpochManager(vm)
	//em.ValidatorMgr.BroadcastCommand(CmdReqDelEpochMember)

	delMemberCollection := &DelEpochMemberCollection{
		ResultList: make(map[string]*DelEpochMemberResult),
		StartTime:  time.Now(),
	}

	em.receivedDelEpochMemberResult[delValidatorID] = delMemberCollection

	utils.Log.Tracef("connectedListMtx1 Locked")
	em.connectedListMtx.Lock()
	defer func() {
		em.connectedListMtx.Unlock()
		utils.Log.Tracef("connectedListMtx1 Unocked")
	}()

	utils.Log.Tracef("Will broadcast DelEpoch command from all connected validators...")
	for validatorId, validator := range em.ConnectedList {
		delMemberCollection.ResultList[validatorId] = &DelEpochMemberResult{ValidatorId: validator.ValidatorInfo.ValidatorId, Result: epoch.DelEpochMemberResult_NotConfirm, Token: ""}
		validator.SendCommand(CmdReqDelEpochMember)
	}

	// Add local validator confirm result
	delEpochMember := &epoch.DelEpochMember{
		ValidatorId:    em.ValidatorMgr.myValidator.ValidatorInfo.ValidatorId,
		DelValidatorId: delValidatorID,
		DelCode:        epoch.DelCode_Disconnect,
		EpochIndex:     em.CurrentEpoch.EpochIndex,
		Result:         epoch.DelEpochMemberResult_Agree,
	}

	tokenData := delEpochMember.GetDelEpochMemTokenData()
	// Sign the token by local validator private key
	token, err := em.ValidatorMgr.SignToken(tokenData)
	if err == nil {
		resultLocal := &DelEpochMemberResult{ValidatorId: em.ValidatorMgr.myValidator.ValidatorInfo.ValidatorId, Result: epoch.DelEpochMemberResult_Agree, Token: token}
		delMemberCollection.ResultList[em.ValidatorMgr.myValidator.ValidatorInfo.ValidatorId] = resultLocal
	}

	go em.delEpochMemberHandler(delValidatorID)
}

func (em *EpochMemberManager) OnConfirmedDelEpochMember(delEpochMember *epoch.DelEpochMember) {
	if delEpochMember == nil {
		return
	}

	confirmedValidatorId := delEpochMember.ValidatorId

	utils.Log.Tracef("connectedListMtx2 Locked")
	em.connectedListMtx.Lock()
	defer func() {
		em.connectedListMtx.Unlock()
		utils.Log.Tracef("connectedListMtx2 Unocked")
	}()

	confirmedValidator := em.ConnectedList[confirmedValidatorId]
	if confirmedValidator == nil {
		return
	}
	verified := delEpochMember.VerifyToken(confirmedValidator.ValidatorInfo.ValidatorId)
	if !verified {
		// The confirm command isnot verified
		return
	}

	delValidatorID := delEpochMember.DelValidatorId

	delValidatorCollection, ok := em.receivedDelEpochMemberResult[delValidatorID]
	if !ok {
		return
	}

	// record validator confirm result
	result := &DelEpochMemberResult{ValidatorId: confirmedValidator.ValidatorInfo.ValidatorId, Result: delEpochMember.Result, Token: delEpochMember.Token}
	delValidatorCollection.ResultList[confirmedValidatorId] = result
}

func (em *EpochMemberManager) delEpochMemberHandler(delValidatorID string) {
	utils.Log.Tracef("[EpochMemberManager]delEpochMemberHandler ...")

	exitDelEpochHandler := make(chan struct{})
	duration := time.Second * 5
	time.AfterFunc(duration, func() {
		em.handleDelEpochMember(delValidatorID)
		exitDelEpochHandler <- struct{}{}
	})

	// 这里阻塞主 goroutine 等待任务执行（可根据需要改为其他逻辑）
	<-exitDelEpochHandler
	utils.Log.Tracef("[EpochMemberManager]delEpochMemberHandler done .")
}

func (em *EpochMemberManager) handleDelEpochMember(delValidatorID string) {

	delValidatorCollection, ok := em.receivedDelEpochMemberResult[delValidatorID]
	if !ok {
		return
	}

	totalCount := len(delValidatorCollection.ResultList)
	agreeCount := 0
	for _, result := range delValidatorCollection.ResultList {
		if result.Result == epoch.DelEpochMemberResult_Agree {
			agreeCount++
		}
	}

	minAgreeCount := (totalCount * 2) / 3

	if agreeCount >= minAgreeCount {
		// Agree for del validatorID from epoch member

		// remove req result
		delete(em.receivedDelEpochMemberResult, delValidatorID)

		// Del the member and broadcast for Update epoch
		//em.NotifyEpochMemberDeleted(delValidatorID)
		em.CurrentEpoch.DelEpochMember(delValidatorID)
		em.ValidatorMgr.ConfirmDelEpoch(em.CurrentEpoch, delValidatorCollection)
		//
		updateEpochCmd := validatorcommand.NewMsgUpdateEpoch(em.CurrentEpoch)
		em.ValidatorMgr.BroadcastCommand(updateEpochCmd)

		// CurrentEpoch has been updated, check continue handover
		em.ValidatorMgr.CheckContinueHandOver()
	} else {
		// Disagree for del validatorID from epoch member
		utils.Log.Tracef("[EpochMemberManager]Disagree for del validatorID from epoch member: %d", delValidatorID)
	}
}

func (em *EpochMemberManager) NotifyEpochMemberDeleted(delValidatorID string) {
	CmdConfirmDelEpochMember := validatorcommand.NewMsgReqDelEpochMember(em.ValidatorMgr.Cfg.ValidatorId,
		validatorcommand.CmdDelEpochMemberTarget_Confirm,
		delValidatorID,
		epoch.DelCode_Disconnect,
		em.CurrentEpoch.EpochIndex)

	utils.Log.Tracef("Will broadcast ReqEpoch command from all connected validators...")

	utils.Log.Tracef("connectedListMtx3 Locked")
	em.connectedListMtx.Lock()
	defer func() {
		em.connectedListMtx.Unlock()
		utils.Log.Tracef("connectedListMtx3 Unocked")
	}()

	for _, validator := range em.ConnectedList {
		validator.SendCommand(CmdConfirmDelEpochMember)
	}
}

// Received a Del epoch member command
func (em *EpochMemberManager) ConfirmDelEpochMember(reqDelEpochMember *validatorcommand.MsgReqDelEpochMember, remoteAddr net.Addr) *epoch.DelEpochMember {
	utils.Log.Tracef("[ValidatorManager]ConfirmDelEpochMember received from validator [%s]...", remoteAddr.String())

	if reqDelEpochMember == nil {
		return nil
	}

	utils.Log.Tracef("[ValidatorManager]ConfirmDelEpochMember Will confirm the [%d] is or not connected?", reqDelEpochMember.DelValidatorId)

	switch reqDelEpochMember.Target {
	case validatorcommand.CmdDelEpochMemberTarget_Consult:
		// 需要Check指定要删除的validator是否已经离线， 如果确认离线， 则回复确认消息
		delValidator, isConnected := em.GetEpochMember(reqDelEpochMember.DelValidatorId)
		if !isConnected {
			// 回复同意删除消息
			utils.Log.Tracef("The validator [%d] is not connected", reqDelEpochMember.DelValidatorId)
			confirmDelMember := em.NewConfirmDelMember(reqDelEpochMember.DelValidatorId, reqDelEpochMember, epoch.DelEpochMemberResult_Agree)
			return confirmDelMember
		}

		// 发送一个ping消息给要删除的validator， 确认是否已经离线
		nonce, _ := wire.RandomUint64()
		delValidator.SendCommand(validatorcommand.NewMsgPing(nonce))

		em.checkMemberConnectedHandler(delValidator, reqDelEpochMember)
	case validatorcommand.CmdDelEpochMemberTarget_Confirm:
		// 已经确认删除，从本地的Epoch中删除validator
		em.CurrentEpoch.DelEpochMember(reqDelEpochMember.DelValidatorId)
		return nil
	}
	return nil
}

func (em *EpochMemberManager) checkMemberConnectedHandler(delValidator *validator.Validator, reqDelEpochMember *validatorcommand.MsgReqDelEpochMember) *epoch.DelEpochMember {
	utils.Log.Tracef("[EpochMemberManager]checkMemberConnectedHandler ...")

	exitCheckHandler := make(chan struct{})
	var cfmDelEpochMember *epoch.DelEpochMember
	duration := time.Second * 1
	time.AfterFunc(duration, func() {
		cfmDelEpochMember = em.handleCheckMemberConnected(delValidator, reqDelEpochMember)
		exitCheckHandler <- struct{}{}
	})

	// 这里阻塞主 goroutine 等待任务执行（可根据需要改为其他逻辑）
	<-exitCheckHandler
	utils.Log.Tracef("[NewEpochManager]newEpochHandler done .")
	return cfmDelEpochMember
}

func (em *EpochMemberManager) handleCheckMemberConnected(delValidator *validator.Validator, reqDelEpochMember *validatorcommand.MsgReqDelEpochMember) *epoch.DelEpochMember {
	utils.Log.Tracef("[EpochMemberManager]handleCheckMemberConnected ...")

	lastReceived := delValidator.GetLastReceived()

	now := time.Now()
	// 计算时间间隔
	duration := now.Sub(lastReceived)

	// 判断是否小于1秒
	if duration < time.Second {
		utils.Log.Tracef("[EpochMemberManager]The member is response in 1 second, it's connected .")
		// 回复不同意删除消息
		confirmDelMember := em.NewConfirmDelMember(reqDelEpochMember.ValidatorId, reqDelEpochMember, epoch.DelEpochMemberResult_Reject)
		return confirmDelMember

	} else {
		utils.Log.Tracef("[EpochMemberManager]The member is not response pong in 1 second, it's disconnected .")
		// 回复同意删除消息
		confirmDelMember := em.NewConfirmDelMember(reqDelEpochMember.ValidatorId, reqDelEpochMember, epoch.DelEpochMemberResult_Agree)
		return confirmDelMember
	}
}

func (em *EpochMemberManager) NewConfirmDelMember(delValidatorId string, reqDelEpochMember *validatorcommand.MsgReqDelEpochMember, result uint32) *epoch.DelEpochMember {
	delEpochMember := &epoch.DelEpochMember{
		ValidatorId:    em.ValidatorMgr.Cfg.ValidatorId,
		DelValidatorId: delValidatorId,
		DelCode:        reqDelEpochMember.DelCode,
		EpochIndex:     reqDelEpochMember.EpochIndex,
		Result:         result,
	}

	tokenData := delEpochMember.GetDelEpochMemTokenData()
	// Sign the token by local validator private key
	token, err := em.ValidatorMgr.SignToken(tokenData)
	if err != nil {
		utils.Log.Errorf("Sign token failed: %v", err)
		return nil
	}

	delEpochMember.Token = token

	return delEpochMember
}
