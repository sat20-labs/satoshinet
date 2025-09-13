// Copyright (c) 2013-2015 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"io"

	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
)

// MsgPing implements the Message interface and represents a bitcoin ping
// message.
//
// For versions BIP0031Version and earlier, it is used primarily to confirm
// that a connection is still valid.  A transmission error is typically
// interpreted as a closed connection and that the peer should be removed.
// For versions AFTER BIP0031Version it contains an identifier which can be
// returned in the pong message to determine network timing.
//
// The payload for this message just consists of a nonce used for identifying
// it later.
type MsgPing struct {
	// Unique value associated with message that is used to identify
	// specific ping message.
	Nonce uint64
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgPing) BtcDecode(r io.Reader, pver uint32) error {
	nonce := uint64(0)
	err := utils.ReadElements(r, &nonce)
	if err != nil {
		return err
	}
	msg.Nonce = nonce

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgPing) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.Nonce)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgPing) Command() string {
	return CmdPing
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgPing) MaxPayloadLength(pver uint32) uint32 {
	// Nonce 8 bytes.
	plen := uint32(8)

	return plen
}

func (msg *MsgPing) LogCommandInfo() {
	utils.Log.Tracef("Command Ping, Nonce: %d", msg.Nonce)
}

// NewMsgPing returns a new bitcoin ping message that conforms to the Message
// interface.  See MsgPing for details.
func NewMsgPing(nonce uint64) *MsgPing {
	return &MsgPing{
		Nonce: nonce,
	}
}
