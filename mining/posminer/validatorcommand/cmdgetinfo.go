// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
)

const (
	// MaxHostSize is the maximum number of host bytes allowed.
	MaxHostSize = 256
)

// MsgGetInfo implements the Message interface and represents a bitcoin version
// message.  It is used for a peer to advertise itself as soon as an outbound
// connection is made.  The remote peer then uses this information along with
// its own to negotiate.  The remote peer must then respond with a version
// message of its own containing the negotiated values followed by a verack
// message (MsgVerAck).  This exchange must take place before any further
// communication is allowed to proceed.
type MsgGetInfo struct {
	// Version of the protocol the node is using.
	ProtocolVersion int32

	// Current validator id
	ValidatorId uint64

	// validator public key
	PublicKey [btcec.PubKeyBytesLenCompressed]byte

	// validator Host
	Host string

	// Time the validator connected to Validator network.  This is encoded as an int64 on the wire.
	CreateTime time.Time
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgGetInfo) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgGetInfo.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf, &msg.ProtocolVersion, &msg.ValidatorId, &msg.PublicKey, &msg.Host,
		(*utils.Int64Time)(&msg.CreateTime))
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgGetInfo) BtcEncode(w io.Writer, pver uint32) error {
	err := utils.WriteElements(w, msg.ProtocolVersion, msg.ValidatorId, msg.PublicKey, msg.Host,
		msg.CreateTime.Unix())
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgGetInfo) Command() string {
	return CmdGetInfo
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgGetInfo) MaxPayloadLength(pver uint32) uint32 {

	// Protocol version 4 bytes + validatorId 8 bytes  + btcec.PubKeyBytesLenCompressed bytes + timestamp 8 bytes
	return 20 + btcec.PubKeyBytesLenCompressed + MaxHostSize
}

func (msg *MsgGetInfo) LogCommandInfo() {
	utils.Log.Tracef("Command MsgGetInfo:")
	utils.Log.Tracef("ProtocolVersion: %d", msg.ProtocolVersion)
	utils.Log.Tracef("ValidatorId: %d", msg.ValidatorId)
	utils.Log.Tracef("PublicKey: %x", msg.PublicKey)
	utils.Log.Tracef("Host: %x", msg.Host)
	utils.Log.Tracef("CreateTime: %s", msg.CreateTime.Format(time.DateTime))
}

// NewMsgGetInfo returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgGetInfo(validatorInfo *validatorinfo.ValidatorInfo) *MsgGetInfo {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgGetInfo{
		ProtocolVersion: int32(VALIDATOR_VERION),
		ValidatorId:     validatorInfo.ValidatorId,
		PublicKey:       validatorInfo.PublicKey,
		Host:            validatorInfo.Host,
		CreateTime:      validatorInfo.CreateTime,
	}

}
