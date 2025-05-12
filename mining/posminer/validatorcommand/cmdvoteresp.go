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

const (
	// MaxMsgHandOverLength is the maximum length of a MsgHandOver
	// message.
	MaxVoteTokenSize = 256
)

type Vote struct {
	ValidatorId uint64 // The validator id of Vote
	VoteType    uint32 // Vote type
	VoteId      uint32 // The vote id
	Pass        uint32 // Pass or fail, 0 fail -- against this vote, 1 pass  -- agree this vote
	GeneratorId uint64 // The generator id of Vote result for vote a new generator, if the vote is a new epoch, the generator id is 0
	Token       string
}

// MsgVoteResp implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgVoteResp).
type MsgVoteResp struct {
	// Current validator id
	VoteInfo Vote
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgVoteResp) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgVoteResp.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf,
		&msg.VoteInfo.ValidatorId,
		&msg.VoteInfo.VoteType,
		&msg.VoteInfo.VoteId,
		&msg.VoteInfo.Pass,
		&msg.VoteInfo.GeneratorId,
		&msg.VoteInfo.Token)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVoteResp) BtcEncode(w io.Writer, pver uint32) error {

	err := utils.WriteElements(w,
		msg.VoteInfo.ValidatorId,
		msg.VoteInfo.VoteType,
		msg.VoteInfo.VoteId,
		msg.VoteInfo.Pass,
		msg.VoteInfo.GeneratorId,
		msg.VoteInfo.Token)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVoteResp) Command() string {
	return CmdVoteResp
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVoteResp) MaxPayloadLength(pver uint32) uint32 {
	// ValidatorId 8 bytes + VoteType 4 bytes +  VoteId 4 bytes,  Pass 4 bytes + GeneratorId 8 bytes + MaxVoteTokenSize
	return 28 + MaxVoteTokenSize
}

func (msg *MsgVoteResp) LogCommandInfo() {
	utils.Log.Tracef("Command MsgVoteResp:")
	utils.Log.Tracef("ValidatorId: %d", msg.VoteInfo.ValidatorId)
	voteType := "Unknown type"
	if msg.VoteInfo.VoteType == VoteType_NewGenerator {
		voteType = "New Generator"
	} else if msg.VoteInfo.VoteType == VoteType_NewEpoch {
		voteType = "New Epoch"
	}

	utils.Log.Tracef("VoteType: %s", voteType)
	utils.Log.Tracef("VoteId: %d", msg.VoteInfo.VoteId)
	utils.Log.Tracef("Pass: %d", msg.VoteInfo.Pass)
	utils.Log.Tracef("GeneratorId: %d", msg.VoteInfo.GeneratorId)
}

// NewMsgVoteResp returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgVoteResp(voteInfo *Vote) *MsgVoteResp {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgVoteResp{
		VoteInfo: *voteInfo,
	}

}
