// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
)

// MsgGetValidators implements the Message interface try to get all validators
// message.  It is used for a peer to get all validators by sorts from the another peer.
// The remote peer must then respond validators message.
type MsgGetValidators struct {
	// Current validator id
	ValidatorId string
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgGetValidators) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgGetValidators.BtcDecode reader is not a " +
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
func (msg *MsgGetValidators) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgGetValidators) Command() string {
	return CmdGetValidators
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgGetValidators) MaxPayloadLength(pver uint32) uint32 {
	//  validatorId 66 + 4 bytes
	return 70
}

func (msg *MsgGetValidators) LogCommandInfo() {
	utils.Log.Tracef("Command MsgGetValidators:")
	utils.Log.Tracef("ValidatorId: %s", msg.ValidatorId)
}

// NewMsgGetValidators returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgGetValidators(validatorId string) *MsgGetValidators {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgGetValidators{
		ValidatorId: validatorId,
	}

}
