// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

// MsgConfirmDelEpoch implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgConfirmDelEpoch struct {
	DelEpochMember *epoch.DelEpochMember
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgConfirmDelEpoch) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgConfirmDelEpoch.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	msg.DelEpochMember = &epoch.DelEpochMember{}

	err := utils.ReadElements(buf, &msg.DelEpochMember.ValidatorId, &msg.DelEpochMember.DelValidatorId, &msg.DelEpochMember.DelCode, &msg.DelEpochMember.EpochIndex, &msg.DelEpochMember.Result, &msg.DelEpochMember.Token)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgConfirmDelEpoch) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.DelEpochMember.ValidatorId, msg.DelEpochMember.DelValidatorId, msg.DelEpochMember.DelCode, msg.DelEpochMember.EpochIndex, msg.DelEpochMember.Result, msg.DelEpochMember.Token)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgConfirmDelEpoch) Command() string {
	return CmdConfirmDelEpoch
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgConfirmDelEpoch) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 bytes + DelValidatorId 66 bytes + DelCode 4 bytes + EpochIndex 8 bytes + Result 4 bytes + + token 256 bytes
	return 148 + 256
}

func (msg *MsgConfirmDelEpoch) LogCommandInfo() {
	utils.Log.Tracef("Command MsgConfirmDelEpoch:")
	utils.Log.Tracef("ValidatorId: %d", msg.DelEpochMember.ValidatorId)
	utils.Log.Tracef("DelValidatorId: %d", msg.DelEpochMember.DelValidatorId)
	utils.Log.Tracef("DelCode: %d", msg.DelEpochMember.DelCode)
	utils.Log.Tracef("EpochIndex: %d", msg.DelEpochMember.EpochIndex)
	utils.Log.Tracef("Result: %d", msg.DelEpochMember.Result)
	utils.Log.Tracef("Token: %s", msg.DelEpochMember.Token)
}

// NewMsgConfirmDelEpoch returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgConfirmDelEpoch(delEpochMember *epoch.DelEpochMember) *MsgConfirmDelEpoch {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgConfirmDelEpoch{
		DelEpochMember: delEpochMember,
	}
}
