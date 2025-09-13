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
	BlockType_VCBlock = 1
	BlockType_EPBlock = 2
)

// MsgGetVCBlock implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgGetVCBlock struct {
	// Request validator id
	ValidatorId string
	BlockType   uint32
	BlockHash   chainhash.Hash
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgGetVCBlock) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgGetVCBlock.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ValidatorId, &msg.BlockType, &msg.BlockHash)
	if err != nil {
		utils.Log.Errorf("MsgGetVCBlock:ReadElements failed: %v", err)
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgGetVCBlock) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ValidatorId, msg.BlockType, msg.BlockHash)
	if err != nil {
		utils.Log.Errorf("MsgGetVCBlock:WriteElements failed: %v", err)
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgGetVCBlock) Command() string {
	return CmdGetVCBlock
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgGetVCBlock) MaxPayloadLength(pver uint32) uint32 {
	// validatorId 66 + 4 bytes + BlockType 4 bytes + Block Hash 32 bytes
	return 106
}

func (msg *MsgGetVCBlock) LogCommandInfo() {
	utils.Log.Tracef("Command MsgGetVCBlock:")
	utils.Log.Tracef("ValidatorId: %s", msg.ValidatorId)
	utils.Log.Tracef("BlockType: %d", msg.BlockType)
	utils.Log.Tracef("BlockHash: %s", msg.BlockHash.String())
}

// NewMsgGetVCBlock returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgGetVCBlock(validatorId string, blockType uint32, hash chainhash.Hash) *MsgGetVCBlock {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgGetVCBlock{
		ValidatorId: validatorId,
		BlockType:   blockType,
		BlockHash:   hash,
	}

}
