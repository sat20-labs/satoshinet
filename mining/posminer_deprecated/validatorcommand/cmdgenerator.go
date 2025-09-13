// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
)

const (
	// MaxMsgGeneratorLength is the maximum length of a MsgGenerator
	// message.
	MaxGeneratorTokenSize = 256
)

// MsgGenerator implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgGenerator struct {
	// Current validator id
	GeneratorInfo generator.Generator
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgGenerator) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgGenerator.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	err := utils.ReadElements(buf,
		&msg.GeneratorInfo.GeneratorId,
		&msg.GeneratorInfo.Timestamp,
		&msg.GeneratorInfo.Height,
		&msg.GeneratorInfo.Token)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgGenerator) BtcEncode(w io.Writer, pver uint32) error {

	err := utils.WriteElements(w,
		msg.GeneratorInfo.GeneratorId,
		msg.GeneratorInfo.Timestamp,
		msg.GeneratorInfo.Height,
		msg.GeneratorInfo.Token)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgGenerator) Command() string {
	return CmdGenerator
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgGenerator) MaxPayloadLength(pver uint32) uint32 {
	// GeneratorId 66 + 4 bytes + Height 4  +timestamp 8 bytes + token GeneratorTokenSize bytes
	return 82 + MaxGeneratorTokenSize
}

func (msg *MsgGenerator) LogCommandInfo() {
	utils.Log.Tracef("Command MsgGenerator:")
	utils.Log.Tracef("GeneratorId: %d", msg.GeneratorInfo.GeneratorId)
	timeStamp := time.Unix(msg.GeneratorInfo.Timestamp, 0)
	utils.Log.Tracef("Timestamp: %s", timeStamp.Format(time.DateTime))
	utils.Log.Tracef("Height: %d", msg.GeneratorInfo.Height)
	utils.Log.Tracef("Token: %s", msg.GeneratorInfo.Token)
}

// NewMsgGenerator returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgGenerator(generator *generator.Generator) *MsgGenerator {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	if generator != nil {
		return &MsgGenerator{
			GeneratorInfo: *generator,
		}
	} else {
		return &MsgGenerator{}
	}

}
