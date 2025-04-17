package validatormanager

import (
	"errors"
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechain"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorcommand"
)

// NewEpochManager是一个处理NewEpoch事件的管理器
// 1. 发送ReqNewEpoch事件时创建NewEpochManager
// 2. 记录所有发送的Validator的ID， 用于处理NewEpoch时的依据
// 3. 接收到NewEpoch事件时交给NewEpochManager进行处理

type NewEpochVoteItem struct {
	VoteData *validatechain.DataEpochVote // 投票的epoch
	Hash     *chainhash.Hash              //  投票的epblock的hash
}

type NewEpochManager struct {
	ValidatorMgr  *ValidatorManager
	started       bool
	reason        uint32
	receivedEpoch map[uint64]*NewEpochVoteItem
}

func CreateNewEpochManager(validatorMgr *ValidatorManager, reason uint32) *NewEpochManager {
	return &NewEpochManager{
		ValidatorMgr:  validatorMgr,
		receivedEpoch: make(map[uint64]*NewEpochVoteItem),
		started:       false,
		reason:        reason,
	}
}

func (nem *NewEpochManager) Start() {
	// All commands are sent, will due to result in 5 seconds
	go nem.newEpochHandler()
	nem.started = true
}

func (nem *NewEpochManager) NewReqEpoch(validatorId uint64) {
	if nem.started == true {
		err := errors.New("NewEpochManager is started, cannot add validatorId as invited validator")
		utils.Log.Errorf("NewReqEpoch failed: %v", err)
		return
	}
	nem.receivedEpoch[validatorId] = &NewEpochVoteItem{}
}

func (nem *NewEpochManager) AddReceivedEpoch(validatorId uint64, hash *chainhash.Hash) error {
	if _, ok := nem.receivedEpoch[validatorId]; !ok {
		err := errors.New("Isnot invited validatorId for received epoch")
		utils.Log.Errorf("AddReceivedEpoch failed: %v", err)
		return err

	}
	// Update the epoch of the validator
	nem.receivedEpoch[validatorId].Hash = hash
	return nil
}

// newEpochHandler starts a goroutine that waits for a specified duration
// and then triggers the handleNewEpoch function. It utilizes a channel to
// signal the completion of the task. The function blocks the main goroutine
// until the task is finished or the channel receives a signal, with an
// interval of 5 seconds before executing the handleNewEpoch.
func (nem *NewEpochManager) newEpochHandler() {
	utils.Log.Debugf("[NewEpochManager]newEpochHandler ...")

	exitNewEpochHandler := make(chan struct{})
	duration := time.Second * 5
	time.AfterFunc(duration, func() {
		nem.handleNewEpoch()
		exitNewEpochHandler <- struct{}{}
	})

	// 这里阻塞主 goroutine 等待任务执行（可根据需要改为其他逻辑）
	select {
	case exitNewEpochHandler <- struct{}{}:
		utils.Log.Debugf("[NewEpochManager]newEpochHandler done .")
		return
	}
}

type ValidEpochItem struct {
	EpochCount int
	VoteItem   *NewEpochVoteItem
}

type NewEpochResult struct {
	invalidEpoch []*NewEpochVoteItem
	validEpoch   []*ValidEpochItem
}

func (nem *NewEpochManager) handleNewEpoch() {
	utils.Log.Debugf("[NewEpochManager]handleNewEpoch ...")

	utils.Log.Debugf("****************************************************************************************")
	utils.Log.Debugf("Received Epochs: summary:")

	invitedCount := len(nem.receivedEpoch)

	for validatorId, voteItem := range nem.receivedEpoch {
		if voteItem == nil || voteItem.Hash == nil {
			utils.Log.Debugf("Not received new epoch by validator [%d]", validatorId)
			continue
		}
		//title := fmt.Sprintf("Received Epoch from %d", validatorId)
		voteItemData, err := nem.ValidatorMgr.validateChain.GetEPBlock(voteItem.Hash)
		if err != nil {
			utils.Log.Debugf("Cannot get epblock by hash [%s] from validator [%d]", voteItem.Hash.String(), validatorId)
			continue
		}
		voteItem.VoteData = voteItemData.Data

		title := fmt.Sprintf("Received Epoch vote from %d", validatorId)
		showVoteData(title, voteItem.VoteData)
	}

	// view the req epoch result
	result := NewEpochResult{
		invalidEpoch: make([]*NewEpochVoteItem, 0),
		validEpoch:   make([]*ValidEpochItem, 0),
	}

	for _, voteItem := range nem.receivedEpoch {
		if !isValidVote(voteItem) {
			result.invalidEpoch = append(result.invalidEpoch, voteItem)
			continue
		}

		matched := 0
		// Find the epoch match with valid epoch
		for _, validItem := range result.validEpoch {
			isSame := isSameVote(voteItem, validItem.VoteItem)
			if isSame {
				matched++ 
				validItem.EpochCount++
			}
		}
		if matched > 0 {
			result.validEpoch = append(result.validEpoch, &ValidEpochItem{
				EpochCount: matched,
				VoteItem:   voteItem,
			})
		}
	}

	utils.Log.Debugf("Received Epochs: invaited count:%d", invitedCount)

	// check the valid epoch result
	minValidCount := (invitedCount * 2) / 3
	if minValidCount < 2 {
		minValidCount = 2 // at least 2 validators
	}

	utils.Log.Debugf("Received Epochs: min valid count:%d", minValidCount)
	localValidatorId := nem.ValidatorMgr.GetMyValidatorId()

	for _, validItem := range result.validEpoch {
		utils.Log.Debugf("valid item: %v", validItem)
		if validItem.EpochCount >= minValidCount {
			utils.Log.Debugf("valid epoch vote count: %d", validItem.EpochCount)
			showVoteData("confirmed epoch vote:", validItem.VoteItem.VoteData)

			// The new epoch is confirmed, the result will be sent to all validators
			epochConfirmed := &epoch.Epoch{
				EpochIndex:      validItem.VoteItem.VoteData.EpochIndex,
				CreateTime:      time.Now(), // epoch confirmed time
				CurGeneratorPos: epoch.Pos_Epoch_NotStarted,
				ItemList:        make([]*epoch.EpochItem, 0),
			}

			for index, item := range validItem.VoteItem.VoteData.EpochItemList {
				item.Index = uint32(index)
				epochConfirmed.ItemList = append(epochConfirmed.ItemList, &item)
			}
			if len(epochConfirmed.ItemList) < 2 {
				// not enough validators for epoch
				utils.Log.Debugf("Not enough validators for epoch")
				return
			}

			// save to vc block, and broadcast to other validators
			showEpoch("confirmed epoch:", epochConfirmed)

			nem.ValidatorMgr.ConfirmNewEpoch(epochConfirmed, nem.reason, nem.receivedEpoch)

			// Notify local peer for the confirmed epoch
			nem.ValidatorMgr.OnConfirmEpoch(epochConfirmed, nil)

			confirmedEpochCmg := validatorcommand.NewMsgConfirmEpoch(localValidatorId, epochConfirmed)
			nem.ValidatorMgr.BroadcastCommand(confirmedEpochCmg)

			// Check current epoch is valid
			if nem.ValidatorMgr.GetCurrentEpoch() == nil {
				// Current epoch is not valid, will req new epoch to miner new block
				nextEpoch := nem.ValidatorMgr.GetNextEpoch()
				if nextEpoch != nil {
					nextBlockHight := nem.ValidatorMgr.GetCurrentBlockHeight() + 1
					timeStamp := time.Now().Unix()
					handoverEpoch := &epoch.HandOverEpoch{
						ValidatorId:    localValidatorId,
						Timestamp:      timeStamp,
						NextEpochIndex: nextEpoch.EpochIndex,
						NextHeight:     nextBlockHight,
					}
					tokenData := handoverEpoch.GetNextEpochTokenData()
					token, err := nem.ValidatorMgr.myValidator.CreateToken(tokenData)
					if err != nil {
						utils.Log.Errorf("Create token failed: %v", err)
						return
					}
					handoverEpoch.Token = token
					nextEpochCmd := validatorcommand.NewMsgNextEpoch(handoverEpoch)

					time.Sleep(300 * time.Millisecond) // Deply 300ms for next epoch for other validators need process cfm epoch first
					nem.ValidatorMgr.BroadcastCommand(nextEpochCmd)

					// Notify local peer for changing to next epoch
					nem.ValidatorMgr.OnNextEpoch(handoverEpoch)
				}

			}

			// valid epoch only one
			break

		} else {
			utils.Log.Debugf("invalid epoch:")
			utils.Log.Debugf("epoch count: %d", validItem.EpochCount)
			//showEpoch("invalid epoch:", validItem.epoch)
		}
	}

	utils.Log.Debugf("Received Epochs: summary end.")
	utils.Log.Debugf("****************************************************************************************")
}

func showVoteData(title string, voteData *validatechain.DataEpochVote) {
	utils.Log.Debugf("--------------- %s -----------------", title)
	utils.Log.Debugf("VotorId: %d", voteData.VotorId)
	utils.Log.Debugf("PublicKey: %x", voteData.PublicKey)
	utils.Log.Debugf("EpochIndex: %d", voteData.EpochIndex)
	utils.Log.Debugf("CreateTime: %s", time.Unix(voteData.CreateTime, 0).Format(time.DateTime))
	utils.Log.Debugf("Reason: %d", voteData.Reason)
	utils.Log.Debugf("Token: %s", voteData.Token)
	for _, item := range voteData.EpochItemList {
		utils.Log.Debugf("ValidatorId: %d", item.ValidatorId)
		utils.Log.Debugf("PublicKey: %x", item.PublicKey)
		utils.Log.Debugf("Host: %s", item.Host)
		utils.Log.Debugf("Index: %d", item.Index)
		utils.Log.Debugf("----------------------------------------")
	}
}

func isValidVote(voteItem *NewEpochVoteItem) bool {
	if voteItem == nil || voteItem.VoteData == nil {
		return false
	}

	if len(voteItem.VoteData.EpochItemList) == 0 {
		return false
	}

	return true
}

func isSameVote(vote1 *NewEpochVoteItem, vote2 *NewEpochVoteItem) bool {
	if vote1 == nil || vote1.VoteData == nil || vote2 == nil || vote2.VoteData == nil {
		return false
	}

	if vote1.VoteData.EpochIndex != vote2.VoteData.EpochIndex {
		return false
	}

	if len(vote1.VoteData.EpochItemList) != len(vote2.VoteData.EpochItemList) {
		return false
	}

	for i := 0; i < len(vote1.VoteData.EpochItemList); i++ {
		if vote1.VoteData.EpochItemList[i].ValidatorId != vote2.VoteData.EpochItemList[i].ValidatorId {
			return false
		}
	}

	return true
}
