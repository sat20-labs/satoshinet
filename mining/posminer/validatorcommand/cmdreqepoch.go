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

// MsgReqEpoch implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgReqEpoch struct {
	// Request validator id
	ValidatorId string
	EpochIndex  int64
	Reason      uint32
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgReqEpoch) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgReqEpoch.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ValidatorId, &msg.EpochIndex, &msg.Reason)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgReqEpoch) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId, msg.EpochIndex, msg.Reason)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgReqEpoch) Command() string {
	return CmdReqEpoch
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgReqEpoch) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 + 4 bytes + EpochIndex 8 bytes + Reason 4 bytes
	return 82
}

func (msg *MsgReqEpoch) LogCommandInfo() {
	utils.Log.Tracef("Command MsgReqEpoch:")
	utils.Log.Tracef("ValidatorId: %s", msg.ValidatorId)
	utils.Log.Tracef("EpochIndex: %d", msg.EpochIndex)
	utils.Log.Tracef("Reason: %d", msg.Reason)
}

// NewMsgReqEpoch returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgReqEpoch(validatorId string, epochIndex int64, reason uint32) *MsgReqEpoch {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgReqEpoch{
		ValidatorId: validatorId,
		EpochIndex:  epochIndex,
		Reason:      reason,
	}

}
