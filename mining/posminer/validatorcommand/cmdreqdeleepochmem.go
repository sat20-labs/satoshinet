// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	CmdDelEpochMemberTarget_Consult = uint32(0) // The command target is consult
	CmdDelEpochMemberTarget_Confirm = uint32(1) // The command target is notify for confirmed the epoch member is deleted
)

// MsgReqDelEpochMember implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgReqDelEpochMember struct {
	// Request validator id
	ValidatorId    string
	Target         uint32
	DelValidatorId string // The validator id to be deleted
	DelCode        uint32 // The reason code for delete epoch member
	EpochIndex     int64  // The epoch index for confirm epoch member delete
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgReqDelEpochMember) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgReqDelEpochMember.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ValidatorId, &msg.Target, &msg.DelValidatorId, &msg.DelCode, &msg.EpochIndex)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgReqDelEpochMember) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId, msg.Target, msg.DelValidatorId, msg.DelCode, msg.EpochIndex)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgReqDelEpochMember) Command() string {
	return CmdDelEpochMem
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgReqDelEpochMember) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 bytes + Target 4 bytes + DelValidatorId 66 bytes + DelCode 4 bytes + EpochIndex 8 bytes
	return 148
}

func (msg *MsgReqDelEpochMember) LogCommandInfo() {
	utils.Log.Tracef("Command MsgReqDelEpochMember:")
	utils.Log.Tracef("ValidatorId: %s", msg.ValidatorId)
	utils.Log.Tracef("DelValidatorId: %s", msg.DelValidatorId)
	utils.Log.Tracef("DelCode: %d", msg.DelCode)
	utils.Log.Tracef("EpochIndex: %d", msg.EpochIndex)
}

// NewMsgReqDelEpochMember returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgReqDelEpochMember(validatorId string, target uint32, delValidatorId string, delCode uint32, epochIndex int64) *MsgReqDelEpochMember {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgReqDelEpochMember{
		ValidatorId:    validatorId,
		Target:         target,
		DelValidatorId: delValidatorId,
		DelCode:        delCode,
		EpochIndex:     epochIndex,
	}

}
