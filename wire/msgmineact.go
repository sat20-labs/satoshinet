// Copyright (c) 2013-2015 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wire

import (
	"fmt"
	"io"
)

const (
	MAX_REJECT_REASON = 512
)

// MsgMineAck implements the Message interface and represents a bitcoin pong
// message which is used primarily to confirm that a connection is still valid
// in response to a bitcoin ping message (MsgPing).
//
// This message was not added until protocol versions AFTER BIP0031Version.
type MsgMineAck struct {
	// Unique value associated with message that is used to identify
	// specific ping message.
	Nonce uint64

	// 如果有subcmd，这里是subcmd的执行结果
	Code RejectCode
	Reason string
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgMineAck) BtcDecode(r io.Reader, pver uint32, enc MessageEncoding) error {
	// NOTE: <= is not a mistake here.  The BIP0031 was defined as AFTER
	// the version unlike most others.
	// if pver <= BIP0031Version {
	// 	str := fmt.Sprintf("pong message invalid for protocol "+
	// 		"version %d", pver)
	// 	return messageError("MsgMineAck.BtcDecode", str)
	// }

	// nonce, err := binarySerializer.Uint64(r, littleEndian)
	// if err != nil {
	// 	return err
	// }
	// msg.Nonce = nonce

	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)

	nonce, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	msg.Nonce = nonce

	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return err
	}
	msg.Code = RejectCode(buf[0])

	// Human readable string with specific details (over and above the
	// reject code above) about why the command was rejected.
	reason, err := readVarStringBuf(r, pver, buf)
	if err != nil {
		return err
	}
	msg.Reason = reason

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgMineAck) BtcEncode(w io.Writer, pver uint32, enc MessageEncoding) error {
	// NOTE: <= is not a mistake here.  The BIP0031 was defined as AFTER
	// the version unlike most others.
	// if pver <= BIP0031Version {
	// 	str := fmt.Sprintf("pong message invalid for protocol "+
	// 		"version %d", pver)
	// 	return messageError("MsgMineAck.BtcEncode", str)
	// }

	// return binarySerializer.PutUint64(w, littleEndian, msg.Nonce)
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)

	err := WriteVarIntBuf(w, pver, msg.Nonce, buf)
	if err != nil {
		return err
	}

	// Code indicating why the command was rejected.
	buf[0] = byte(msg.Code)
	if _, err := w.Write(buf[:1]); err != nil {
		return err
	}

	if len(msg.Reason) > MAX_REJECT_REASON {
		return fmt.Errorf("length of reason too long")
	}

	// Human readable string with specific details (over and above the
	// reject code above) about why the command was rejected.
	err = writeVarStringBuf(w, pver, msg.Reason, buf)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgMineAck) Command() string {
	return CmdMineAck
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgMineAck) MaxPayloadLength(pver uint32) uint32 {
	plen := uint32(0)
	// The pong message did not exist for BIP0031Version and earlier.
	// NOTE: > is not a mistake here.  The BIP0031 was defined as AFTER
	// the version unlike most others.
	//if pver > BIP0031Version {
		// Nonce 8 bytes.
		plen += 8 + 1 + MAX_REJECT_REASON
	//}

	return plen
}

// NewMsgMineAck returns a new bitcoin pong message that conforms to the Message
// interface.  See MsgMineAck for details.
func NewMsgMineAck(nonce uint64) *MsgMineAck {
	return &MsgMineAck{
		Nonce: nonce,
		Code: 0,
		Reason: "",
	}
}

func NewMsgMineAckWithCode(nonce uint64, code RejectCode, reason string) *MsgMineAck {
	return &MsgMineAck{
		Nonce: nonce,
		Code: code,
		Reason: reason,
	}
}
