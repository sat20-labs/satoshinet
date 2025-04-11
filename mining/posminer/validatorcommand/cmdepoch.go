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
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	MaxMsgEpochLength = (24 + 256*60) * 2
)

// MsgEpoch implements the Message interface try to get all validators
// message.  It is used for a peer to get all validators by sorts from the another peer.
// The remote peer must then respond validators message.
type MsgEpoch struct {
	CurrentEpoch *epoch.Epoch
	NextEpoch    *epoch.Epoch
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgEpoch) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgEpoch.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	// Read CurrentEpoch
	epoch, err := ReadEpoch(buf)
	if err != nil {
		return err
	}

	msg.CurrentEpoch = epoch

	// Read NextEpoch
	epoch, err = ReadEpoch(buf)
	if err != nil {
		return err
	}

	msg.NextEpoch = epoch

	return nil
}

func ReadEpoch(buf *bytes.Buffer) (*epoch.Epoch, error) {
	var exist uint32
	err := utils.ReadElements(buf, &exist)
	if err != nil {
		return nil, err
	}

	if exist == 0 {
		return nil, nil
	}

	receivedEpoch := &epoch.Epoch{
		ItemList: make([]*epoch.EpochItem, 0),
	}

	// Read EpochIndex, CreateHeight, CreateTime
	err = utils.ReadElements(buf, &receivedEpoch.EpochIndex, &receivedEpoch.CreateHeight, (*utils.Int64Time)(&receivedEpoch.CreateTime))
	if err != nil {
		return nil, err
	}

	// Read ItemList
	itemCount := uint32(0)
	err = utils.ReadElements(buf, &itemCount)
	if err != nil {
		return nil, err
	}

	for i := 0; i < int(itemCount); i++ {
		var epochItem epoch.EpochItem
		err = utils.ReadElements(buf, &epochItem.ValidatorId)
		if err != nil {
			return nil, err
		}
		err = utils.ReadElements(buf, &epochItem.Host)
		if err != nil {
			return nil, err
		}
		err = utils.ReadElements(buf, &epochItem.PublicKey)
		if err != nil {
			return nil, err
		}
		err = utils.ReadElements(buf, &epochItem.Index)
		if err != nil {
			return nil, err
		}
		receivedEpoch.ItemList = append(receivedEpoch.ItemList, &epochItem)
	}

	// read Generator
	err = utils.ReadElements(buf, &exist)
	if err != nil {
		return nil, err
	}
	if exist != 0 {
		// Generator exist, read Generator
		var generator generator.Generator
		err := utils.ReadElements(buf,
			&generator.GeneratorId,
			&generator.Timestamp,
			&generator.Height,
			&generator.Token,
			(*utils.Int64Time)(&generator.MinerTime))
		if err != nil {
			return nil, err
		}
		receivedEpoch.Generator = &generator
	}

	// read CurGeneratorPos
	err = utils.ReadElements(buf, &receivedEpoch.CurGeneratorPos)
	if err != nil {
		return nil, err
	}

	// read epoch change info
	var blockHash chainhash.Hash
	err = utils.ReadElements(buf, (*utils.Int64Time)(&receivedEpoch.LastChangeTime), &receivedEpoch.VCBlockHeight, &blockHash)
	if err != nil {
		return nil, err
	}

	receivedEpoch.VCBlockHash = &blockHash

	return receivedEpoch, nil

}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgEpoch) BtcEncode(w io.Writer, pver uint32) error {
	err := WriteEpoch(w, msg.CurrentEpoch)
	if err != nil {
		return err
	}

	err = WriteEpoch(w, msg.NextEpoch)
	if err != nil {
		return err
	}
	return nil
}

func WriteEpoch(w io.Writer, epoch *epoch.Epoch) error {

	var exist uint32
	if epoch == nil {
		exist = 0
	} else {
		exist = 1
	}
	err := utils.WriteElements(w, exist)
	if err != nil {
		return err
	}

	if exist == 0 {
		// Epoch not exist
		return nil
	}

	// EpochIndex      uint32               // Epoch Index, start from 0
	// CreateHeight    int32                // 创建Epoch时当前Block的高度
	// CreateTime      time.Time            // 当前Epoch的创建时间
	// ItemList        []*EpochItem         // 当前Epoch包含的验证者列表，（已排序）， 在一个Epoch结束前不会改变
	// Generator       *generator.Generator // 当前Generator
	// CurGeneratorPos int32                // 当前Generator在ItemList中的位置

	// Write EpochIndex, CreateHeight, CreateTime
	err = utils.WriteElements(w, epoch.EpochIndex, epoch.CreateHeight, epoch.CreateTime.Unix())
	if err != nil {
		return err
	}

	// Write ItemList
	itemCount := uint32(len(epoch.ItemList))
	err = utils.WriteElements(w, &itemCount)
	if err != nil {
		return err
	}

	for i := 0; i < int(itemCount); i++ {
		epochItem := epoch.ItemList[i]
		err = utils.WriteElements(w, epochItem.ValidatorId)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, epochItem.Host)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, epochItem.PublicKey)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, epochItem.Index)
		if err != nil {
			return err
		}
	}

	// write Generator
	if epoch.Generator == nil {
		exist = 0
	} else {
		exist = 1
	}
	err = utils.WriteElements(w, &exist)
	if err != nil {
		return err
	}
	if exist != 0 {
		// Generator exist, write Generator
		err := utils.WriteElements(w,
			epoch.Generator.GeneratorId,
			epoch.Generator.Timestamp,
			epoch.Generator.Height,
			epoch.Generator.Token,
			epoch.Generator.MinerTime.Unix(),
		)
		if err != nil {
			return err
		}
	}

	// write CurGeneratorPos
	err = utils.WriteElements(w, epoch.CurGeneratorPos)
	if err != nil {
		return err
	}

	// write epoch change info
	var blockHash chainhash.Hash
	if epoch.VCBlockHash != nil {
		blockHash = *epoch.VCBlockHash
	}
	err = utils.WriteElements(w, epoch.LastChangeTime.Unix(), epoch.VCBlockHeight, blockHash)
	if err != nil {
		return err
	}

	return nil

}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgEpoch) Command() string {
	return CmdEpoch
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgEpoch) MaxPayloadLength(pver uint32) uint32 {
	//  MaxMsgEpochLength bytes
	return MaxMsgEpochLength
}

func (msg *MsgEpoch) LogCommandInfo() {
	utils.Log.Debugf("Command MsgEpoch:")
	showEpoch("MsgEpoch: CurrentEpoch", msg.CurrentEpoch)
	showEpoch("MsgEpoch: NexEpoch", msg.NextEpoch)
	utils.Log.Debugf("-------------------------------------")
}

// NewMsgEpoch returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgEpoch(currentEpoch, nextEpoch *epoch.Epoch) *MsgEpoch {

	epochMsg := &MsgEpoch{
		CurrentEpoch: currentEpoch,
		NextEpoch:    nextEpoch,
	}

	return epochMsg

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

		generator := epoch.Generator
		if generator == nil {
			utils.Log.Debugf("	No generator")
		} else {
			utils.Log.Debugf("	Generator ID: %d", generator.GeneratorId)
			utils.Log.Debugf("	Generator TimeStamp: %s", time.Unix(generator.Timestamp, 0).Format("2006-01-02 15:04:05"))
			utils.Log.Debugf("	Generator Token: %s", generator.Token)
			utils.Log.Debugf("	Generator Block Height: %d", generator.Height)
		}

		//Generator     *generator.Generator // 当前Generator
		utils.Log.Debugf("CurGeneratorPos: %d", epoch.CurGeneratorPos)

	}
	utils.Log.Debugf("*********************************        End        ********************************")
}
