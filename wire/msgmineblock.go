// Copyright (c) 2013-2015 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package wire

import (
	"io"
)

// MsgMineBlock implements the Message interface and represents a bitcoin ping
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
type MsgMineBlock struct {
	// Unique value associated with message that is used to identify
	// specific ping message.
	Nonce uint64

	// embedding data
	SubCmd  string
	Payload []byte
	Sig     []byte
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgMineBlock) BtcDecode(r io.Reader, pver uint32, enc MessageEncoding) error {
	// There was no nonce for BIP0031Version and earlier.
	// NOTE: > is not a mistake here.  The BIP0031 was defined as AFTER
	// the version unlike most others.
	// if pver > BIP0031Version {
	// 	nonce, err := binarySerializer.Uint64(r, littleEndian)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	msg.Nonce = nonce
	// }

	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)

	nonce, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	msg.Nonce = nonce

	subCmd, err := readVarStringBuf(r, pver, buf)
	if err == nil {
		msg.SubCmd = subCmd

		payload, err := ReadVarBytesBuf(r, pver, buf, MaxBlockPayload, "block")
		if err != nil {
			return err
		}
		msg.Payload = payload

		sig, err := ReadVarBytesBuf(r, pver, buf, 256, "sig")
		if err != nil {
			return err
		}
		msg.Sig = sig
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgMineBlock) BtcEncode(w io.Writer, pver uint32, enc MessageEncoding) error {
	// There was no nonce for BIP0031Version and earlier.
	// NOTE: > is not a mistake here.  The BIP0031 was defined as AFTER
	// the version unlike most others.
	// if pver > BIP0031Version {
	// 	err := binarySerializer.PutUint64(w, littleEndian, msg.Nonce)
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)

	err := WriteVarIntBuf(w, pver, msg.Nonce, buf)
	if err != nil {
		return err
	}

	// 兼容区块浏览器，在subcmd为空时，不编码
	if msg.SubCmd != "" {
		err = writeVarStringBuf(w, pver, msg.SubCmd, buf)
		if err != nil {
			return err
		}

		err = WriteVarBytesBuf(w, pver, msg.Payload, buf)
		if err != nil {
			return err
		}

		err = WriteVarBytesBuf(w, pver, msg.Sig, buf)
		if err != nil {
			return err
		}
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgMineBlock) Command() string {
	return CmdMineBlock
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgMineBlock) MaxPayloadLength(pver uint32) uint32 {
	plen := uint32(0)
	// There was no nonce for BIP0031Version and earlier.
	// NOTE: > is not a mistake here.  The BIP0031 was defined as AFTER
	// the version unlike most others.
	// if pver > BIP0031Version {
	// 	// Nonce 8 bytes.
	// 	plen += 8
	// }
	plen = 8 + CommandSize + MaxBlockPayload

	return plen
}

// NewMsgMineBlock returns a new bitcoin ping message that conforms to the Message
// interface.  See MsgMineBlock for details.
func NewMsgMineBlock(nonce uint64) *MsgMineBlock {
	return &MsgMineBlock{
		Nonce: nonce,
	}
}
