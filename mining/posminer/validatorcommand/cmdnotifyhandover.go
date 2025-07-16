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

// MsgNotifyHandover implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgNotifyHandover struct {
	// Request validator id
	ValidatorId string
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgNotifyHandover) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgNotifyHandover.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ValidatorId)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgNotifyHandover) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgNotifyHandover) Command() string {
	return CmdNotifyHandOver
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgNotifyHandover) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 + 4 bytes
	return 70
}

func (msg *MsgNotifyHandover) LogCommandInfo() {
	utils.Log.Tracef("Command MsgNotifyHandover:")
	utils.Log.Tracef("ValidatorId: %d", msg.ValidatorId)
}

// NewMsgNotifyHandover returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgNotifyHandover(validatorId string) *MsgNotifyHandover {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgNotifyHandover{
		ValidatorId: validatorId,
	}

}
