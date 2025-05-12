// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
)

const (
	MaxMsgValidatorsLength = 10240 * 69 // 69 len of validatorinfo.ValidatorInfo to be sent
)

// MsgValidators implements the Message interface try to get all validators
// message.  It is used for a peer to get all validators by sorts from the another peer.
// The remote peer must then respond validators message.
type MsgValidators struct {
	// Current validator id
	ValidatorCount int32
	Validators     []validatorinfo.ValidatorInfo
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgValidators) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgValidators.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ValidatorCount)
	if err != nil {
		return err
	}

	for i := 0; i < int(msg.ValidatorCount); i++ {
		var validator validatorinfo.ValidatorInfo
		// Addr            string  // Not send to remote
		// ValidatorId     uint64
		// PublicKey       [btcec.PubKeyBytesLenCompressed]byte
		// CreateTime      time.Time
		// ActivitionCount int32
		// GeneratorCount  int32
		// DiscountCount   int32
		// FaultCount      int32
		// ValidatorScore  int32
		err = utils.ReadElements(buf, &validator.ValidatorId)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.Host)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.PublicKey)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, (*utils.Int64Time)(&validator.CreateTime))
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.ActivitionCount)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.GeneratorCount)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.DiscountCount)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.FaultCount)
		if err != nil {
			return err
		}
		err = utils.ReadElements(buf, &validator.ValidatorScore)
		if err != nil {
			return err
		}

		msg.Validators = append(msg.Validators, validator)
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgValidators) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorCount)
	if err != nil {
		return err
	}

	for _, validator := range msg.Validators {
		// Addr            string  // Not send to remote
		// ValidatorId     uint64
		// PublicKey       [btcec.PubKeyBytesLenCompressed]byte
		// CreateTime      time.Time
		// ActivitionCount int32
		// GeneratorCount  int32
		// DiscountCount   int32
		// FaultCount      int32
		// ValidatorScore  int32
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
		err = utils.WriteElements(w, validator.CreateTime.Unix())
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.ActivitionCount)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.GeneratorCount)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.DiscountCount)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.FaultCount)
		if err != nil {
			return err
		}
		err = utils.WriteElements(w, validator.ValidatorScore)
		if err != nil {
			return err
		}

	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgValidators) Command() string {
	return CmdValidators
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgValidators) MaxPayloadLength(pver uint32) uint32 {
	//  MaxMsgValidatorsLength bytes
	return MaxMsgValidatorsLength
}

func (msg *MsgValidators) LogCommandInfo() {
	utils.Log.Tracef("Command MsgValidators:")
	utils.Log.Tracef("Validator Count: %d", msg.ValidatorCount)
	for index, validator := range msg.Validators {
		utils.Log.Tracef("---------------------------------------------")
		utils.Log.Tracef("No: %d", index)
		utils.Log.Tracef("Validator Id: %d", validator.ValidatorId)
		utils.Log.Tracef("Validator Host: %s", validator.Host)
		utils.Log.Tracef("Validator PublicKey: %x", validator.PublicKey)
		utils.Log.Tracef("Validator CreateTime: %s", validator.CreateTime.Format("2006-01-02 15:04:05"))
		utils.Log.Tracef("Validator ActivitionCount: %d", validator.ActivitionCount)
		utils.Log.Tracef("Validator GeneratorCount: %d", validator.GeneratorCount)
		utils.Log.Tracef("Validator DiscountCount: %d", validator.DiscountCount)
		utils.Log.Tracef("Validator FaultCount: %d", validator.FaultCount)
		utils.Log.Tracef("Validator ValidatorScore: %d", validator.ValidatorScore)
		utils.Log.Tracef("")
	}
	utils.Log.Tracef("---------------------------------------------")
}

// NewMsgValidators returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgValidators(validatorList []*validatorinfo.ValidatorInfo) *MsgValidators {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	validatorsMsg := &MsgValidators{}
	validatorsMsg.ValidatorCount = int32(len(validatorList))
	validatorsMsg.Validators = make([]validatorinfo.ValidatorInfo, 0)

	for _, validator := range validatorList {
		validatorItem := validatorinfo.ValidatorInfo{
			ValidatorId:     validator.ValidatorId,
			Host:            validator.Host,
			PublicKey:       validator.PublicKey,
			CreateTime:      validator.CreateTime,
			ActivitionCount: validator.ActivitionCount,
			GeneratorCount:  validator.GeneratorCount,
			DiscountCount:   validator.DiscountCount,
			FaultCount:      validator.FaultCount,
			ValidatorScore:  validator.ValidatorScore,
		}
		validatorsMsg.Validators = append(validatorsMsg.Validators, validatorItem)
	}

	return validatorsMsg

}
