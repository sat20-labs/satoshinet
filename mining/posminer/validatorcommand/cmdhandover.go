// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	// MaxMsgHandOverLength is the maximum length of a MsgHandOver
	// message.
	MaxHandOverTokenSize = 256
)

// MsgHandOver implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgHandOver).
type MsgHandOver struct {
	// Current validator id
	HandOverInfo generator.GeneratorHandOver
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgHandOver) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgHandOver.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf,
		&msg.HandOverInfo.ValidatorId,
		&msg.HandOverInfo.HandOverType,
		&msg.HandOverInfo.Timestamp,
		&msg.HandOverInfo.GeneratorId,
		&msg.HandOverInfo.Height,
		&msg.HandOverInfo.Token)
	if err != nil {
		utils.Log.Errorf("MsgHandOver:ReadElements failed: %v", err)
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgHandOver) BtcEncode(w io.Writer, pver uint32) error {

	err := utils.WriteElements(w,
		msg.HandOverInfo.ValidatorId,
		msg.HandOverInfo.HandOverType,
		msg.HandOverInfo.Timestamp,
		msg.HandOverInfo.GeneratorId,
		msg.HandOverInfo.Height,
		msg.HandOverInfo.Token)
	if err != nil {
		utils.Log.Errorf("MsgHandOver:WriteElements failed: %v", err)
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgHandOver) Command() string {
	return CmdHandOver
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgHandOver) MaxPayloadLength(pver uint32) uint32 {
	// ValidatorId 8 bytes + HandOverType 4 bytes + GeneratorId 8 bytes  +timestamp 8 bytes + block height 4 bytes + MaxHandOverTokenSize bytes
	return 32 + MaxHandOverTokenSize
}

func (msg *MsgHandOver) LogCommandInfo() {
	utils.Log.Tracef("Command MsgHandOver:")
	utils.Log.Tracef("ValidatorId: %d", msg.HandOverInfo.ValidatorId)
	handoverType := "Unknown type"
	if msg.HandOverInfo.HandOverType == generator.HandOverTypeByEpochOrder {
		handoverType = "Order"
	} else if msg.HandOverInfo.HandOverType == generator.HandOverTypeByVote {
		handoverType = "Vote"
	}
	utils.Log.Tracef("HandOverType: %s", handoverType)
	timeStamp := time.Unix(msg.HandOverInfo.Timestamp, 0)
	utils.Log.Tracef("Timestamp: %s", timeStamp.Format(time.DateTime))
	utils.Log.Tracef("GeneratorId: %d", msg.HandOverInfo.GeneratorId)
	utils.Log.Tracef("Height: %d", msg.HandOverInfo.Height)
	utils.Log.Tracef("Token: %s", msg.HandOverInfo.Token)
}

// NewMsgHandOver returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgHandOver(handover *generator.GeneratorHandOver) *MsgHandOver {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgHandOver{
		HandOverInfo: *handover,
	}

}
