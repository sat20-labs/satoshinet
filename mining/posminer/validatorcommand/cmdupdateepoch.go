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

const (
	MaxMsgUpdateEpochLength = (20 + 256*60)
)

// MsgUpdateEpoch implements the Message interface try to get all validators
// message.  It is used for a peer to get all validators by sorts from the another peer.
// The remote peer must then respond validators message.
type MsgUpdateEpoch struct {
	CurrentEpoch *epoch.Epoch
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgUpdateEpoch) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgUpdateEpoch.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	// Read CurrentEpoch
	epoch, err := ReadEpoch(buf)
	if err != nil {
		return err
	}

	msg.CurrentEpoch = epoch

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgUpdateEpoch) BtcEncode(w io.Writer, pver uint32) error {
	err := WriteEpoch(w, msg.CurrentEpoch)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgUpdateEpoch) Command() string {
	return CmdUpdateEpoch
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgUpdateEpoch) MaxPayloadLength(pver uint32) uint32 {
	//  MaxMsgUpdateEpochLength bytes
	return MaxMsgUpdateEpochLength
}

func (msg *MsgUpdateEpoch) LogCommandInfo() {
	utils.Log.Tracef("Command MsgUpdateEpoch:")
	// utils.Log.Tracef("Validator Count: %d", msg.ValidatorCount)
	// for index, validator := range msg.Validators {
	// 	utils.Log.Tracef("-------------------------------------")
	// 	utils.Log.Tracef("No: %d", index)
	// 	utils.Log.Tracef("Validator Id: %d", validator.ValidatorId)
	// 	utils.Log.Tracef("Validator Host: %s", validator.Host)
	// 	utils.Log.Tracef("Validator PublicKey: %x", validator.PublicKey)
	// 	utils.Log.Tracef("Validator Index: %d", validator.Index)
	// 	utils.Log.Tracef("")
	// }
	showEpoch("MsgUpdateEpoch: CurrentEpoch", msg.CurrentEpoch)
	utils.Log.Tracef("-------------------------------------")
}

// NewMsgUpdateEpoch returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgUpdateEpoch(currentEpoch *epoch.Epoch) *MsgUpdateEpoch {

	updateEpochMsg := &MsgUpdateEpoch{
		CurrentEpoch: currentEpoch,
	}
	return updateEpochMsg

}
