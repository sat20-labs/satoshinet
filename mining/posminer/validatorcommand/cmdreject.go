// Copyright (c) 2014-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

// RejectCode represents a numeric value by which a remote peer indicates
// why a message was rejected.
type RejectCode uint8

// These constants define the various supported reject codes.
const (
	RejectMalformed       RejectCode = 0x01
	RejectInvalid         RejectCode = 0x10
	RejectObsolete        RejectCode = 0x11
	RejectDuplicate       RejectCode = 0x12
	RejectNonstandard     RejectCode = 0x40
	RejectDust            RejectCode = 0x41
	RejectInsufficientFee RejectCode = 0x42
	RejectCheckpoint      RejectCode = 0x43
)

// Map of reject codes back strings for pretty printing.
var rejectCodeStrings = map[RejectCode]string{
	RejectMalformed:       "REJECT_MALFORMED",
	RejectInvalid:         "REJECT_INVALID",
	RejectObsolete:        "REJECT_OBSOLETE",
	RejectDuplicate:       "REJECT_DUPLICATE",
	RejectNonstandard:     "REJECT_NONSTANDARD",
	RejectDust:            "REJECT_DUST",
	RejectInsufficientFee: "REJECT_INSUFFICIENTFEE",
	RejectCheckpoint:      "REJECT_CHECKPOINT",
}

// String returns the RejectCode in human-readable form.
func (code RejectCode) String() string {
	if s, ok := rejectCodeStrings[code]; ok {
		return s
	}

	return fmt.Sprintf("Unknown RejectCode (%d)", uint8(code))
}

// MsgReject implements the Message interface and represents a bitcoin reject
// message.
//
// This message was not added until protocol version RejectVersion.
type MsgReject struct {
	// Cmd is the command for the message which was rejected such as
	// as CmdBlock or CmdTx.  This can be obtained from the Command function
	// of a Message.
	Cmd string

	// RejectCode is a code indicating why the command was rejected.  It
	// is encoded as a uint8 on the wire.
	Code RejectCode

	// Reason is a human-readable string with specific details (over and
	// above the reject code) about why the command was rejected.
	Reason string
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgReject) BtcDecode(r io.Reader, pver uint32) error {

	// // Command that was rejected.
	// buf := binarySerializer.Borrow()
	// defer binarySerializer.Return(buf)

	// cmd, err := readVarStringBuf(r, pver, buf)
	// if err != nil {
	// 	return err
	// }
	// msg.Cmd = cmd

	// // Code indicating why the command was rejected.
	// if _, err := io.ReadFull(r, buf[:1]); err != nil {
	// 	return err
	// }
	// msg.Code = RejectCode(buf[0])

	// // Human readable string with specific details (over and above the
	// // reject code above) about why the command was rejected.
	// reason, err := readVarStringBuf(r, pver, buf)
	// if err != nil {
	// 	return err
	// }
	// msg.Reason = reason

	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgReject.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf,
		&msg.Cmd,
		&msg.Code,
		&msg.Reason,
	)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgReject) BtcEncode(w io.Writer, pver uint32) error {

	// Command that was rejected.
	// buf := binarySerializer.Borrow()
	// defer binarySerializer.Return(buf)

	// err := writeVarStringBuf(w, pver, msg.Cmd, buf)
	// if err != nil {
	// 	return err
	// }

	// // Code indicating why the command was rejected.
	// buf[0] = byte(msg.Code)
	// if _, err := w.Write(buf[:1]); err != nil {
	// 	return err
	// }

	// // Human readable string with specific details (over and above the
	// // reject code above) about why the command was rejected.
	// err = writeVarStringBuf(w, pver, msg.Reason, buf)
	// if err != nil {
	// 	return err
	// }

	err := utils.WriteElements(w,
		msg.Cmd,
		msg.Code,
		msg.Reason)
	if err != nil {
		return err
	}
	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgReject) Command() string {
	return CmdReject
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgReject) MaxPayloadLength(pver uint32) uint32 {
	plen := uint32(MaxCommandPayload)

	return plen
}

func (msg *MsgReject) LogCommandInfo() {
	utils.Log.Tracef("Command MsgReject:")
	utils.Log.Tracef("Cmd: %s", msg.Cmd)
	utils.Log.Tracef("RejectCode: %s", msg.Code.String())
	utils.Log.Tracef("Reason: %s", msg.Reason)
}

// NewMsgReject returns a new bitcoin reject message that conforms to the
// Message interface.  See MsgReject for details.
func NewMsgReject(command string, code RejectCode, reason string) *MsgReject {
	return &MsgReject{
		Cmd:    command,
		Code:   code,
		Reason: reason,
	}
}
