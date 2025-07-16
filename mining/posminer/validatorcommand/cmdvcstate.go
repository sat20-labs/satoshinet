// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

// MsgVCState implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgVCState struct {
	Height     int64          // Last block height
	Hash       chainhash.Hash // Last block hash
	EpochIndex int64          // last epoch index
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgVCState) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgVCState.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.Height, &msg.Hash, &msg.EpochIndex)
	if err != nil {
		utils.Log.Errorf("MsgVCState:ReadElements failed: %v", err)
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVCState) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.Height, msg.Hash, msg.EpochIndex)
	if err != nil {
		utils.Log.Errorf("MsgVCState:WriteElements failed: %v", err)
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVCState) Command() string {
	return CmdVCState
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVCState) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 bytes + hash 32 bytes + epoch index 8 bytes
	return 106
}

func (msg *MsgVCState) LogCommandInfo() {
	utils.Log.Tracef("Command MsgVCState:")
	utils.Log.Tracef("Height: %d", msg.Height)
	utils.Log.Tracef("Hash: %s", msg.Hash.String())
	utils.Log.Tracef("EpochIndex: %d", msg.EpochIndex)
}

// NewMsgVCState returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgVCState(height int64, hash chainhash.Hash, epochIndex int64) *MsgVCState {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgVCState{
		Height:     height,
		Hash:       hash,
		EpochIndex: epochIndex,
	}

}
