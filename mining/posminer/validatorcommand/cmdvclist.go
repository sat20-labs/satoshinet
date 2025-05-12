// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	// MaxVCList is the maximum number of items in a VCList message.
	MaxVCList = 100
)

type VCItem struct {
	Height int64
	Hash   chainhash.Hash
}

// MsgVCList implements the Message interface and get current generator
// message. The remote peer must then respond with current generator
// message of its own containing the negotiated values followed by a verack
// message (MsgGenerator).
type MsgVCList struct {
	VCList []*VCItem
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// The version message is special in that the protocol version hasn't been
// negotiated yet.  As a result, the pver field is ignored and any fields which
// are added in new versions are optional.  This also mean that r must be a
// *bytes.Buffer so the number of remaining bytes can be ascertained.
//
// This is part of the Message interface implementation.
func (msg *MsgVCList) BtcDecode(r io.Reader, pver uint32) error {
	buf, ok := r.(*bytes.Buffer)
	if !ok {
		return fmt.Errorf("MsgVCList.BtcDecode reader is not a " +
			"*bytes.Buffer")
	}

	count := uint32(0)

	err := utils.ReadElements(buf, &count)
	if err != nil {
		utils.Log.Errorf("MsgVCList:ReadElements failed: %v", err)
		return err
	}

	for i := 0; i < int(count); i++ {
		item := &VCItem{}
		err = utils.ReadElements(buf, &item.Height, &item.Hash)
		if err != nil {
			utils.Log.Errorf("MsgVCList:ReadElements failed: %v", err)
			return err
		}
		msg.VCList = append(msg.VCList, item)
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVCList) BtcEncode(w io.Writer, pver uint32) error {

	count := uint32(len(msg.VCList))
	if count > MaxVCList {
		count = MaxVCList
	}

	err := utils.WriteElements(w, count)
	if err != nil {
		utils.Log.Errorf("MsgVCList:WriteElements failed: %v", err)
		return err
	}

	for i := 0; i < int(count); i++ {
		item := msg.VCList[i]
		err = utils.WriteElements(w, item.Height, item.Hash)
		if err != nil {
			utils.Log.Errorf("MsgVCList:WriteElements failed: %v", err)
			return err
		}
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVCList) Command() string {
	return CmdVCList
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVCList) MaxPayloadLength(pver uint32) uint32 {
	// count 4 bytes + (Height 8 bytes + hash 32 bytes) * MaxVCList
	return 4 + 40*MaxVCList
}

func (msg *MsgVCList) LogCommandInfo() {
	utils.Log.Tracef("Command MsgVCList:")
	if msg.VCList == nil {
		return
	}
	utils.Log.Tracef("VCList count: %d", len(msg.VCList))
	for _, item := range msg.VCList {
		utils.Log.Tracef("Height: %d", item.Height)
		utils.Log.Tracef("Hash: %s", item.Hash.String())
	}
}

// NewMsgVCList returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgVCList(vclist []*VCItem) *MsgVCList {

	// Limit the timestamp to one second precision since the protocol
	// doesn't support better.
	return &MsgVCList{
		VCList: vclist,
	}

}
