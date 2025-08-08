// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorcommand

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/wire"
)

const VALIDATOR_VERION = 1

// MessageHeaderSize is the number of bytes in a bitcoin message header.
// Bitcoin network (magic) 4 bytes + command 12 bytes + payload length 4 bytes +
// checksum 4 bytes.
const CommandHeaderSize = 24

// CommandNameSize is the fixed size of all commands in the common bitcoin message
// header.  Shorter commands must be zero padded.
const CommandNameSize = 12

// MaxMessagePayload is the maximum bytes a message can be regardless of other
// individual limits imposed by messages themselves.
const MaxCommandPayload = (1024 * 1024 * 32) // 32MB

// Commands used in satsnet posminer message headers which describe the type of message.
const (
	CmdGetInfo         = "getinfo"      // command for get remote peer info
	CmdPeerInfo        = "peerinfo"     // Ack current peer info to requester
	CmdGetValidators   = "getvalidors"  // command for get validators list
	CmdValidators      = "validators"   // send validators list in local peer to remote peer
	CmdGetEpoch        = "getepoch"     // command for get epoch info， if exist next epoch，return next epoch together
	CmdEpoch           = "epoch"        // send epoch info for epoch in local peer to remote peer， if exist next epoch，return next epoch together
	CmdUpdateEpoch     = "updateepoch"  // send updateepoch info for currentepoch when current epoch is updated (handover generator)
	CmdReqEpoch        = "reqepoch"     // Req a new epoch for next epoch, in normal, it will be senbt by last validator in current epoch, it will be response newepoch command
	CmdNewEpoch        = "newepoch"     // Create a new epoch for next epoch
	CmdConfirmEpoch    = "cfmepoch"     // Confirm a new epoch for next epoch
	CmdNextEpoch       = "nextepoch"    // Turn to next epoch, in normal, it will be sent by last validator in current epoch
	CmdDelEpochMem     = "delepochmem"  // Req for del epoch member if the member is disconnected
	CmdConfirmDelEpoch = "cfmdelepoch"  // Confirm for del epoch member if the member is disconnected
	CmdGetGenerator    = "getgenerator" // command for get generator
	CmdGenerator       = "generator"    // declare generator by new generator
	CmdHandOver        = "handover"     // handover generator by prev generator or voter (Epoch member)
	CmdNotifyHandOver  = "ntyhandover"  // notify generator to handover
	CmdPing            = "ping"         // send ping to remote peer
	CmdPong            = "pong"         // Ack pong to remote peer for response of ping
	CmdReject          = "reject"       // send reject to remote peer
	CmdVoteReq         = "votereq"      // send vote request to remote peer
	CmdVoteResp        = "voteresp"     // response vote resp from local peer
	CmdVoteResult      = "voteresult"   // send vote result to remote peer

	// validatechain  block commands
	CmdGetVCState = "getvcstate" // request validatechain State
	CmdVCState    = "vcstate"    // response validatechain State
	CmdGetVCList  = "getvclist"  // request validatechain block list
	CmdVCList     = "vclist"     // response validatechain block list
	CmdGetVCBlock = "getvcblock" // request validatechain block
	CmdVCBlock    = "vcblock"    // response validatechain block
)

// ErrUnknownMessage is the error returned when decoding an unknown message.
var ErrUnknownCommand = fmt.Errorf("received unknown command")

// ErrInvalidHandshake is the error returned when a peer sends us a known
// message that does not belong in the version-verack handshake.
var ErrInvalidHandshake = fmt.Errorf("invalid command during handshake")

// Message is an interface that describes a bitcoin message.  A type that
// implements Message has complete control over the representation of its data
// and may therefore contain additional or fewer fields than those which
// are used directly in the protocol encoded message.
type Message interface {
	BtcDecode(io.Reader, uint32) error
	BtcEncode(io.Writer, uint32) error
	Command() string
	MaxPayloadLength(uint32) uint32
	LogCommandInfo()
}

// makeEmptyMessage creates a message of the appropriate concrete type based
// on the command.
func makeEmptyMessage(command string) (Message, error) {
	var msg Message
	switch command {
	// case CmdSyncInfo:
	// 	msg = &MsgSyncInfo{}

	case CmdGetInfo:
		msg = &MsgGetInfo{}

	case CmdPeerInfo:
		msg = &MsgPeerInfo{}

	case CmdGetValidators:
		msg = &MsgGetValidators{}

	case CmdValidators:
		msg = &MsgValidators{}

	case CmdGetEpoch:
		msg = &MsgGetEpoch{}

	case CmdEpoch:
		msg = &MsgEpoch{}

	case CmdReqEpoch:
		msg = &MsgReqEpoch{}

	case CmdNewEpoch:
		msg = &MsgNewEpoch{}

	case CmdConfirmEpoch:
		msg = &MsgConfirmEpoch{}

	case CmdNextEpoch:
		msg = &MsgNextEpoch{}

	case CmdUpdateEpoch:
		msg = &MsgUpdateEpoch{}

	case CmdDelEpochMem:
		msg = &MsgReqDelEpochMember{}

	case CmdConfirmDelEpoch:
		msg = &MsgConfirmDelEpoch{}

	case CmdGetGenerator:
		msg = &MsgGetGenerator{}

	case CmdGenerator:
		msg = &MsgGenerator{}

	case CmdHandOver:
		msg = &MsgHandOver{}

	case CmdNotifyHandOver:
		msg = &MsgNotifyHandover{}

	case CmdPing:
		msg = &MsgPing{}

	case CmdPong:
		msg = &MsgPong{}

	case CmdReject:
		msg = &MsgReject{}

	case CmdVoteReq:
		msg = &MsgVoteReq{}

	case CmdVoteResp:
		msg = &MsgVoteResp{}

	case CmdVoteResult:
		msg = &MsgVoteResult{}

	case CmdGetVCState:
		msg = &MsgGetVCState{}

	case CmdVCState:
		msg = &MsgVCState{}

	case CmdGetVCList:
		msg = &MsgGetVCList{}

	case CmdVCList:
		msg = &MsgVCList{}

	case CmdGetVCBlock:
		msg = &MsgGetVCBlock{}

	case CmdVCBlock:
		msg = &MsgVCBlock{}

	default:
		return nil, ErrUnknownCommand
	}
	return msg, nil
}

// messageHeader defines the header structure for all bitcoin protocol messages.
type messageHeader struct {
	magic    wire.BitcoinNet // 4 bytes
	command  string          // 12 bytes
	length   uint32          // 4 bytes
	checksum [4]byte         // 4 bytes
}

// readMessageHeader reads a bitcoin message header from r.
func readMessageHeader(r io.Reader) (int, *messageHeader, error) {
	// Since readElements doesn't return the amount of bytes read, attempt
	// to read the entire header into a buffer first in case there is a
	// short read so the proper amount of read bytes are known.  This works
	// since the header is a fixed size.
	var headerBytes [CommandHeaderSize]byte
	n, err := io.ReadFull(r, headerBytes[:])
	if err != nil {
		return n, nil, err
	}
	hr := bytes.NewReader(headerBytes[:])

	// Create and populate a messageHeader struct from the raw header bytes.
	hdr := messageHeader{}
	var command [CommandNameSize]byte
	utils.ReadElements(hr, &hdr.magic, &command, &hdr.length, &hdr.checksum)

	// Strip trailing zeros from command string.
	hdr.command = string(bytes.TrimRight(command[:], "\x00"))

	return n, &hdr, nil
}

// discardInput reads n bytes from reader r in chunks and discards the read
// bytes.  This is used to skip payloads when various errors occur and helps
// prevent rogue nodes from causing massive memory allocation through forging
// header length.
func discardInput(r io.Reader, n uint32) {
	maxSize := uint32(10 * 1024) // 10k at a time
	numReads := n / maxSize
	bytesRemaining := n % maxSize
	if n > 0 {
		buf := make([]byte, maxSize)
		for i := uint32(0); i < numReads; i++ {
			io.ReadFull(r, buf)
		}
	}
	if bytesRemaining > 0 {
		buf := make([]byte, bytesRemaining)
		io.ReadFull(r, buf)
	}
}

// WriteMessageN writes a bitcoin Message to w including the necessary header
// information and returns the number of bytes written.    This function is the
// same as WriteMessage except it also returns the number of bytes written.
func WriteMessageN(w io.Writer, msg Message, pver uint32, btcnet wire.BitcoinNet) (int, error) {
	return WriteMessageWithEncodingN(w, msg, pver, btcnet)
}

// WriteMessage writes a bitcoin Message to w including the necessary header
// information.  This function is the same as WriteMessageN except it doesn't
// doesn't return the number of bytes written.  This function is mainly provided
// for backwards compatibility with the original API, but it's also useful for
// callers that don't care about byte counts.
func WriteMessage(w io.Writer, msg Message, pver uint32, btcnet wire.BitcoinNet) error {
	_, err := WriteMessageN(w, msg, pver, btcnet)
	return err
}

// WriteMessageWithEncodingN writes a bitcoin Message to w including the
// necessary header information and returns the number of bytes written.
// This function is the same as WriteMessageN except it also allows the caller
// to specify the message encoding format to be used when serializing wire
// messages.
func WriteMessageWithEncodingN(w io.Writer, msg Message, pver uint32,
	btcnet wire.BitcoinNet) (int, error) {

	totalBytes := 0

	// Enforce max command size.
	var command [CommandNameSize]byte
	cmd := msg.Command()
	if len(cmd) > CommandNameSize {
		err := fmt.Errorf("WriteMessage:command [%s] is too long [max %v]",
			cmd, CommandNameSize)
		return totalBytes, err
	}
	copy(command[:], []byte(cmd))

	// Encode the message payload.
	var bw bytes.Buffer
	err := msg.BtcEncode(&bw, pver)
	if err != nil {
		return totalBytes, err
	}
	payload := bw.Bytes()
	lenp := len(payload)

	// Enforce maximum overall message payload.
	if lenp > MaxCommandPayload {
		err := fmt.Errorf("WriteMessage:message payload is too large - encoded "+
			"%d bytes, but maximum message payload is %d bytes, %s",
			lenp, MaxCommandPayload, cmd)
		return totalBytes, err
	}

	// Enforce maximum message payload based on the message type.
	mpl := msg.MaxPayloadLength(pver)
	if uint32(lenp) > mpl {
		err := fmt.Errorf("WriteMessage:message payload is too large - encoded "+
			"%d bytes, but maximum message payload size for "+
			"messages of type [%s] is %d.", lenp, cmd, mpl)
		return totalBytes, err
	}

	// Create header for the message.
	hdr := messageHeader{}
	hdr.magic = btcnet
	hdr.command = cmd
	hdr.length = uint32(lenp)
	copy(hdr.checksum[:], chainhash.DoubleHashB(payload)[0:4])

	// Encode the header for the message.  This is done to a buffer
	// rather than directly to the writer since WriteElements doesn't
	// return the number of bytes written.
	hw := bytes.NewBuffer(make([]byte, 0, CommandHeaderSize))
	utils.WriteElements(hw, hdr.magic, command, hdr.length, hdr.checksum)

	// Write header.
	n, err := w.Write(hw.Bytes())
	totalBytes += n
	if err != nil {
		return totalBytes, err
	}

	// Only write the payload if there is one, e.g., verack messages don't
	// have one.
	if len(payload) > 0 {
		n, err = w.Write(payload)
		totalBytes += n
	}

	return totalBytes, err
}

// ReadMessage reads, validates, and parses the next bitcoin Message
// from r for the provided protocol version and bitcoin network.  It returns the
// number of bytes read in addition to the parsed Message and raw bytes which
// comprise the message.  This function is the same as ReadMessageN except it
// allows the caller to specify which message encoding is to to consult when
// decoding wire messages.
func ReadMessage(r io.Reader, pver uint32, btcnet wire.BitcoinNet) (int, Message, []byte, error) {

	totalBytes := 0
	n, hdr, err := readMessageHeader(r)
	totalBytes += n
	if err != nil {
		err = fmt.Errorf("ReadMessage:unable to read message header: %v", err)
		return totalBytes, nil, nil, err
	}

	utils.Log.Tracef("ReadMessage: message command: %s", hdr.command)

	// Enforce maximum message payload.
	if hdr.length > MaxCommandPayload {
		err := fmt.Errorf("ReadMessage:message payload is too large - header "+
			"indicates %d bytes, but max message payload is %d "+
			"bytes. %s", hdr.length, MaxCommandPayload, hdr.command)
		return totalBytes, nil, nil, err

	}

	// Check for messages from the wrong bitcoin network.
	if hdr.magic != btcnet {
		discardInput(r, hdr.length)
		err := fmt.Errorf("ReadMessage:message from other network [%v]", hdr.magic)
		return totalBytes, nil, nil, err
	}

	// Check for malformed commands.
	command := hdr.command
	if !utf8.ValidString(command) {
		discardInput(r, hdr.length)
		err := fmt.Errorf("ReadMessage:invalid command %v", []byte(command))
		return totalBytes, nil, nil, err
	}

	// Create struct of appropriate message type based on the command.
	msg, err := makeEmptyMessage(command)
	if err != nil {
		// makeEmptyMessage can only return ErrUnknownMessage and it is
		// important that we bubble it up to the caller.
		discardInput(r, hdr.length)
		return totalBytes, nil, nil, err
	}

	// Check for maximum length based on the message type as a malicious client
	// could otherwise create a well-formed header and set the length to max
	// numbers in order to exhaust the machine's memory.
	mpl := msg.MaxPayloadLength(pver)
	if hdr.length > mpl {
		discardInput(r, hdr.length)
		err := fmt.Errorf("ReadMessage:payload exceeds max length - header "+
			"indicates %v bytes, but max payload size for "+
			"messages of type [%v] is %v.", hdr.length, command, mpl)
		return totalBytes, nil, nil, err
	}

	// Read payload.
	payload := make([]byte, hdr.length)
	n, err = io.ReadFull(r, payload)
	totalBytes += n
	if err != nil {
		err = fmt.Errorf("ReadMessage:unable to read full payload (%d): %v", hdr.length, err)
		return totalBytes, nil, nil, err
	}

	// Test checksum.
	checksum := chainhash.DoubleHashB(payload)[0:4]
	if !bytes.Equal(checksum, hdr.checksum[:]) {
		err := fmt.Errorf("ReadMessage:payload checksum failed - header "+
			"indicates %v, but actual checksum is %v.",
			hdr.checksum, checksum)
		return totalBytes, nil, nil, err
	}

	// Unmarshal message.  NOTE: This must be a *bytes.Buffer since the
	// MsgVersion BtcDecode function requires it.
	pr := bytes.NewBuffer(payload)
	err = msg.BtcDecode(pr, pver)
	if err != nil {
		err = fmt.Errorf("ReadMessage:BtcDecode failed: %v", err)
		return totalBytes, nil, nil, err
	}

	return totalBytes, msg, payload, nil
}
