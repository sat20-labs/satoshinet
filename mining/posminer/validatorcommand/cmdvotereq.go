// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	// Vote type
	VoteType_NewGenerator = 1
	VoteType_NewEpoch     = 2
)

// 投票请求仅用于当前出块的验证者离线， 则它的下一个验证者提交投票， 确认有下一个验证者进行出块，
// 选举成功后发送投票结果， 并根据选举结果递进成为出块者，并更新出块时间，进行出块准备
// 如果当前出块的验证者是最后一个出块的验证者， 则它的上一个验证者提交投票， 确认进行下一个Epoch选举，
// 选举成功后发送投票结果， 并更新到下一个Epoch进行出块
// 投票时间为2s， 超过时间没有投票者为无效投票，规定时间内投票人数不足为无效投票， 无效投票不计入选举结果

type VoteRequest struct {
	ValidatorId uint64    // The validator id of request vite
	VoteType    uint32    // Vote type
	VoteId      uint32    // The vote id
	EpochIndex  int64     // The epoch index
	VoteCount   uint32    // The vote count
	StartTime   time.Time // The start time of the vote
	EndTime     time.Time // The end time of the vote
}

// MsgVoteReq implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgVoteReq).
type MsgVoteReq struct {
	// Current validator id
	VoteReqInfo VoteRequest
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgVoteReq) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgVoteReq.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf,
		&msg.VoteReqInfo.ValidatorId,
		&msg.VoteReqInfo.VoteType,
		&msg.VoteReqInfo.VoteId,
		&msg.VoteReqInfo.EpochIndex,
		&msg.VoteReqInfo.VoteCount,
		(*utils.Int64Time)(&msg.VoteReqInfo.StartTime),
		(*utils.Int64Time)(&msg.VoteReqInfo.EndTime))
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVoteReq) BtcEncode(w io.Writer, pver uint32) error {

	err := utils.WriteElements(w,
		msg.VoteReqInfo.ValidatorId,
		msg.VoteReqInfo.VoteType,
		msg.VoteReqInfo.VoteId,
		msg.VoteReqInfo.EpochIndex,
		msg.VoteReqInfo.VoteCount,
		msg.VoteReqInfo.StartTime.Unix(),
		msg.VoteReqInfo.EndTime.Unix())
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVoteReq) Command() string {
	return CmdVoteReq
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVoteReq) MaxPayloadLength(pver uint32) uint32 {
	// ValidatorId 8 bytes + VoteType 4 bytes + VoteId 4 bytes  + EpochIndex 8 bytes + VoteCount 4 bytes + StartTime 8 bytes + EndTime 8 bytes
	return 44
}

func (msg *MsgVoteReq) LogCommandInfo() {
	utils.Log.Tracef("Command MsgVoteReq:")
	utils.Log.Tracef("ValidatorId: %d", msg.VoteReqInfo.ValidatorId)
	voteType := "Unknown type"
	if msg.VoteReqInfo.VoteType == VoteType_NewGenerator {
		voteType = "New Generator"
	} else if msg.VoteReqInfo.VoteType == VoteType_NewEpoch {
		voteType = "New Epoch"
	}

	utils.Log.Tracef("VoteType: %s", voteType)
	utils.Log.Tracef("VoteId: %d", msg.VoteReqInfo.VoteId)
	utils.Log.Tracef("EpochIndex: %d", msg.VoteReqInfo.EpochIndex)
	utils.Log.Tracef("VoteCount: %d", msg.VoteReqInfo.VoteCount)

	utils.Log.Tracef("StartTime: %s", msg.VoteReqInfo.StartTime.Format(time.DateTime))
	utils.Log.Tracef("EndTime: %d", msg.VoteReqInfo.EndTime.Format(time.DateTime))
}

// NewMsgVoteReq returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgVoteReq(voteReq *VoteRequest) *MsgVoteReq {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgVoteReq{
		VoteReqInfo: *voteReq,
	}

}
