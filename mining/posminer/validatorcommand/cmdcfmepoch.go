// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	MaxMsgConfirmEpochLength = 20 + 256*60 // 69 len of validatorinfo.ValidatorInfo to be sent
)

// MsgConfirmEpoch implements the Message interface try to get all validators
// message.  It is used for a peer to get all validators by sorts from the another peer.
// The remote peer must then respond validators message.
type MsgConfirmEpoch struct {
	// ValidatorId of the epoch confirmed
	ValidatorId uint64 // Validator Id

	// epoch info for the confirmed epoch
	EpochIndex   int64             // Epoch Index, start from 0
	CreateHeight int32             // 创建Epoch时当前Block的高度
	CreateTime   time.Time         // 当前Epoch的创建时间
	ItemList     []epoch.EpochItem // 当前Epoch包含的验证者列表，（已排序）， 在一个Epoch结束前不会改变

	LastChangeTime time.Time      // 最后一次改变的时间, 用于判断是否需要更新， Epoch的改变包括创建， 转正，generator流转，成员删除
	VCBlockHeight  int64          // 记录当前epoch改变的的VCblock高度
	VCBlockHash    chainhash.Hash // 记录当前epoch改变的的VCblock hash
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgConfirmEpoch) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgConfirmEpoch.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}
	err := utils.ReadElements(buf, &msg.ValidatorId)
	if err != nil {
		return err
	}

	err = utils.ReadElements(buf, &msg.EpochIndex, &msg.CreateHeight, (*utils.Int64Time)(&msg.CreateTime))
	if err != nil {
		return err
	}
	validatorCount := uint32(0)
	err = utils.ReadElements(buf, &validatorCount)
	if err != nil {
		return err
	}
	for i := 0; i < int(validatorCount); i++ {
		epochItem := epoch.EpochItem{}
		err = utils.ReadElements(buf, &epochItem.ValidatorId)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &epochItem.Host)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &epochItem.PublicKey)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &epochItem.Index)
		if err != nil {
			return err
		}
		msg.ItemList = append(msg.ItemList, epochItem)
	}

	err = utils.ReadElements(buf, (*utils.Int64Time)(&msg.LastChangeTime), &msg.VCBlockHeight, &msg.VCBlockHash)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgConfirmEpoch) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId)
	if err != nil {
		return err
	}
	err = utils.WriteElements(w, msg.EpochIndex, msg.CreateHeight, msg.CreateTime.Unix())
	if err != nil {
		return err
	}

	validatorCount := uint32(len(msg.ItemList))
	err = utils.WriteElements(w, validatorCount)
	if err != nil {
		return err
	}

	for _, validator := range msg.ItemList {
		err = utils.WriteElements(w, validator.ValidatorId)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.Host)
		if err != nil {
			return err
		}

		err = utils.WriteElements(w, validator.PublicKey)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.Index)
		if err != nil {
			return err
		}

	}

	err = utils.WriteElements(w, msg.LastChangeTime.Unix(), msg.VCBlockHeight, msg.VCBlockHash)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgConfirmEpoch) Command() string {
	return CmdConfirmEpoch
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgConfirmEpoch) MaxPayloadLength(pver uint32) uint32 {
	//  ValidatorId 8 bytes + EpochIndex 8 bytes + CreateHeight 4 bytes + CreateTime 8 bytes + ValidatorCount 4 bytes
	//  Max validator count is 256 (Max Epoch Size) , EpochItem is 60 bytes + LastChangeTime 8 bytes + VCBlockHeight 8 bytes + VCBlockHash 32 bytes
	return 32 + 256*60 + 48 // 69 len of validatorinfo.ValidatorInfo to be sent

}

func (msg *MsgConfirmEpoch) LogCommandInfo() {
	utils.Log.Debugf("Command MsgConfirmEpoch:")
	utils.Log.Debugf("Validator Id: %d", msg.ValidatorId)

	utils.Log.Debugf("Epoch Index: %d", msg.EpochIndex)
	utils.Log.Debugf("Create Height: %d", msg.CreateHeight)
	utils.Log.Debugf("Create Time: %s", msg.CreateTime.Format(time.DateTime))

	for index, validator := range msg.ItemList {
		utils.Log.Debugf("-------------------------------------")
		utils.Log.Debugf("No: %d", index)
		utils.Log.Debugf("Validator Id: %d", validator.ValidatorId)
		utils.Log.Debugf("Validator Host: %s", validator.Host)
		utils.Log.Debugf("Validator PublicKey: %x", validator.PublicKey)
		utils.Log.Debugf("Validator Index: %d", validator.Index)
		utils.Log.Debugf("")
	}

	utils.Log.Debugf("Last Change Time: %s", msg.LastChangeTime.Format(time.DateTime))
	utils.Log.Debugf("VC Block Height: %d", msg.VCBlockHeight)
	utils.Log.Debugf("VC Block Hash: %s", msg.VCBlockHash.String())
	utils.Log.Debugf("-------------------------------------")
}

// NewMsgConfirmEpoch returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgConfirmEpoch(validatorId uint64, newEpoch *epoch.Epoch) *MsgConfirmEpoch {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	newEpochMsg := &MsgConfirmEpoch{
		ValidatorId:    validatorId,
		EpochIndex:     newEpoch.EpochIndex,
		CreateHeight:   newEpoch.CreateHeight,
		CreateTime:     newEpoch.CreateTime,
		ItemList:       make([]epoch.EpochItem, 0),
		LastChangeTime: newEpoch.LastChangeTime,
		VCBlockHash:    *newEpoch.VCBlockHash,
		VCBlockHeight:  newEpoch.VCBlockHeight,
	}

	for _, epochItem := range newEpoch.ItemList {
		validatorItem := epoch.EpochItem{
			ValidatorId: epochItem.ValidatorId,
			Host:        epochItem.Host,
			PublicKey:   epochItem.PublicKey,
			Index:       epochItem.Index,
		}
		newEpochMsg.ItemList = append(newEpochMsg.ItemList, validatorItem)
	}

	return newEpochMsg
}
