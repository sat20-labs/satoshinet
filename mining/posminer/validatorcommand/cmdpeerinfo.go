// Copyright (c) 2013-2015 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
)

// MsgPeerInfo defines a bitcoin verack message which is used for a peer to
// acknowledge a version message (MsgVersion) after it has used the information
// to negotiate parameters.  It implements the Message interface.
//
// This message has no payload.
type MsgPeerInfo struct {
	// Version of the protocol the node is using.
	ProtocolVersion int32

	// Current validator id
	ValidatorId string

	// validator Host
	Host string

	// Time the validator connected to Validator network.  This is encoded as an int64 on the wire.
	CreateTime time.Time
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgPeerInfo) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgPeerInfo.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ProtocolVersion, &msg.ValidatorId, &msg.Host,
		(*utils.Int64Time)(&msg.CreateTime))
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgPeerInfo) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ProtocolVersion, msg.ValidatorId, msg.Host,
		msg.CreateTime.Unix())
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgPeerInfo) Command() string {
	return CmdPeerInfo
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgPeerInfo) MaxPayloadLength(pver uint32) uint32 {

	// Protocol version 4 bytes + validatorId 66 + 4 bytes + timestamp 8 bytes
	return 82 + MaxHostSize
}

func (msg *MsgPeerInfo) LogCommandInfo() {
	utils.Log.Tracef("Command MsgPeerInfo:")
	utils.Log.Tracef("ProtocolVersion: %d", msg.ProtocolVersion)
	utils.Log.Tracef("ValidatorId: %s", msg.ValidatorId)
	utils.Log.Tracef("Host: %s", msg.Host)
	utils.Log.Tracef("CreateTime: %s", msg.CreateTime.Format(time.DateTime))
}

// NewMsgPeerInfo returns a new bitcoin verack message that conforms to the
// Message interface.
func NewMsgPeerInfo(validatorInfo *validatorinfo.ValidatorInfo) *MsgPeerInfo {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgPeerInfo{
		ProtocolVersion: int32(VALIDATOR_VERION),
		ValidatorId:     validatorInfo.ValidatorId,
		Host:            validatorInfo.Host,
		CreateTime:      validatorInfo.CreateTime,
	}

}
