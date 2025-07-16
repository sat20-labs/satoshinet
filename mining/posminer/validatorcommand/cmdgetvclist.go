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

// MsgGetVCList implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgGetVCList struct {
	// Request validator id
	ValidatorId string

	// start and end height with validate chain, it begin from latest block height, so Start > End, and id end == -1,
	// it means get all vc block list, Max size is 100, if size > 100, return 100 only
	Start int64 // start height of validatechain
	End   int64 // end height of validatechain
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgGetVCList) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgGetVCList.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ValidatorId, &msg.Start, &msg.End)
	if err != nil {
		utils.Log.Errorf("MsgGetVCList:ReadElements failed: %v", err)
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgGetVCList) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId, msg.Start, msg.End)
	if err != nil {
		utils.Log.Errorf("MsgGetVCList:WriteElements failed: %v", err)
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgGetVCList) Command() string {
	return CmdGetVCList
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgGetVCList) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 8 bytes + start 8 bytes + end 8 bytes
	return 24
}

func (msg *MsgGetVCList) LogCommandInfo() {
	utils.Log.Tracef("Command MsgGetVCList:")
	utils.Log.Tracef("ValidatorId: %s", msg.ValidatorId)
	utils.Log.Tracef("Start: %d", msg.Start)
	utils.Log.Tracef("End: %d", msg.End)
}

// NewMsgGetVCList returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgGetVCList(validatorId string, start, end int64) *MsgGetVCList {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgGetVCList{
		ValidatorId: validatorId,
		Start:       start,
		End:         end,
	}

}
