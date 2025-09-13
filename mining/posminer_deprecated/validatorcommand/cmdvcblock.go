// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
)

const (
	MaxSize_Payload = 10 * 1024 * 1024
)

// MsgVCBlock implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgVCBlock struct {
	Hash      chainhash.Hash
	BlockType uint32
	Payload   []byte
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgVCBlock) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgVCBlock.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.Hash, &msg.BlockType, (*utils.VarByte)(&msg.Payload))
	if err != nil {
		utils.Log.Errorf("MsgVCBlock:ReadElements failed: %v", err)
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVCBlock) BtcEncode(w io.Writer, pver uint32) error {

	err := utils.WriteElements(w, msg.Hash, msg.BlockType, (utils.VarByte)(msg.Payload))
	if err != nil {
		utils.Log.Errorf("MsgVCBlock:WriteElements failed: %v", err)
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVCBlock) Command() string {
	return CmdVCBlock
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVCBlock) MaxPayloadLength(pver uint32) uint32 {
	// Hash 32 bytes + BlockType 4 bytes + MaxSize_Payload
	return 36 + MaxSize_Payload
}

func (msg *MsgVCBlock) LogCommandInfo() {
	utils.Log.Tracef("Command MsgVCBlock:")
	utils.Log.Tracef("BlockHash: %s", msg.Hash.String())
	utils.Log.Tracef("BlockType: %d", msg.BlockType)
	utils.Log.Tracef("Block Payload Length: %d", len(msg.Payload))
}

// NewMsgVCBlock returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgVCBlock(hash chainhash.Hash, blockType uint32, playload []byte) *MsgVCBlock {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgVCBlock{
		Hash:      hash,
		BlockType: blockType,
		Payload:   playload,
	}

}
