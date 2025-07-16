// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

// MsgNextEpoch implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgNextEpoch struct {
	HandoverEpoch epoch.HandOverEpoch
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgNextEpoch) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgNextEpoch.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.HandoverEpoch.ValidatorId, &msg.HandoverEpoch.Timestamp, &msg.HandoverEpoch.Token, &msg.HandoverEpoch.NextEpochIndex, &msg.HandoverEpoch.NextHeight)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgNextEpoch) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.HandoverEpoch.ValidatorId, msg.HandoverEpoch.Timestamp, msg.HandoverEpoch.Token, msg.HandoverEpoch.NextEpochIndex, msg.HandoverEpoch.NextHeight)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgNextEpoch) Command() string {
	return CmdNextEpoch
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgNextEpoch) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 bytes + timestamp 8bytes + token 256 bytes + nextEpochIndex 4bytes + nextHeight 4bytes
	return 338
}

func (msg *MsgNextEpoch) LogCommandInfo() {
	utils.Log.Tracef("Command MsgNextEpoch:")
	utils.Log.Tracef("ValidatorId: %d", msg.HandoverEpoch.ValidatorId)
	utils.Log.Tracef("Timestamp: %s", time.Unix(msg.HandoverEpoch.Timestamp, 0).Format(time.DateTime))
	utils.Log.Tracef("Token: %s", msg.HandoverEpoch.Token)
	utils.Log.Tracef("NextEpochIndex: %d", msg.HandoverEpoch.NextEpochIndex)
	utils.Log.Tracef("NextHeight: %d", msg.HandoverEpoch.NextHeight)
}

// NewMsgNextEpoch returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgNextEpoch(HandoverEpoch *epoch.HandOverEpoch) *MsgNextEpoch {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgNextEpoch{
		HandoverEpoch: *HandoverEpoch,
	}
}
