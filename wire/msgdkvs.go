package wire

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

const (
	MaxDKVSKeySize       = 256
	MaxDKVSValueSize     = 16 * 1024
	MaxDKVSBlobValueSize = 1024 * 1024
	MaxDKVSFeeProofSize  = 2 * 1024
	MaxDKVSSignatureSize = 256
	MaxDKVSPubKeySize    = 128

	// MaxDKVSRecordSize remains the ordinary-record wire bound. Blob records use
	// MaxDKVSBlobRecordSize and are still bounded by MaxProtocolMessageLength.
	MaxDKVSRecordSize     = 16 * 1024
	MaxDKVSBlobRecordSize = MaxDKVSBlobValueSize + 4*1024

	MaxDKVSRecordsPerMsg  = 200
	MaxDKVSItemsPerMsg    = 1024
	MaxDKVSCursorSize     = 512
	MaxDKVSSyncFilters    = 256
	MaxDKVSFilterTypeSize = 16
	MaxDKVSNotifyDataSize = MaxDKVSBlobRecordSize

	// Leave room for message framing fields, cursors, signatures and not-found
	// hashes. Record-bearing DKVS messages must fit below this aggregate budget.
	MaxDKVSRecordsPayloadSize = MaxProtocolMessageLength - 64*1024
)

type DKVSRecord struct {
	Version      uint32
	Key          string
	Value        []byte
	PubKey       []byte
	Signature    []byte
	Seq          uint64
	IssueTime    uint64
	TTL          uint64
	ExpiryHeight uint64
	FeeProof     []byte
	Flags        uint32
}

type DKVSInvItem struct {
	Key        string
	RecordHash chainhash.Hash
	Seq        uint64
}

type MsgDKVSNotify struct {
	EventType uint8
	Data      []byte
}

type MsgDKVSInv struct {
	Items []DKVSInvItem
}

type MsgDKVSGet struct {
	Keys         []string
	RecordHashes []chainhash.Hash
}

type MsgDKVSData struct {
	Records  []*DKVSRecord
	NotFound []chainhash.Hash
}

type MsgDKVSSyncRequest struct {
	SessionID uint64
	Cursor    []byte
	Limit     uint32
	Filters   []DKVSSyncFilter
}

type MsgDKVSSyncResponse struct {
	SessionID       uint64
	Records         []*DKVSRecord
	NextCursor      []byte
	Done            bool
	CheckpointRoot  chainhash.Hash
	SourceSignature []byte
}

type DKVSSyncFilter struct {
	Type   string
	Target string
}

// DKVSValueSizeLimit returns the wire-level value limit for a key. Namespace
// shape and ownership are validated by the DKVS indexer after decoding.
func DKVSValueSizeLimit(key string) uint32 {
	if strings.HasPrefix(key, "/blob/") {
		return MaxDKVSBlobValueSize
	}
	return MaxDKVSValueSize
}

// DKVSRecordSizeLimit returns the wire-level encoded record limit for a key.
func DKVSRecordSizeLimit(key string) int {
	if strings.HasPrefix(key, "/blob/") {
		return MaxDKVSBlobRecordSize
	}
	return MaxDKVSRecordSize
}

func readDKVSRecord(r io.Reader, pver uint32, buf []byte) (*DKVSRecord, error) {
	rec := &DKVSRecord{}
	if err := readElements(r, &rec.Version); err != nil {
		return nil, err
	}
	key, err := readVarStringBuf(r, pver, buf)
	if err != nil {
		return nil, err
	}
	if len(key) > MaxDKVSKeySize {
		return nil, messageError("readDKVSRecord", "dkvs key too large")
	}
	rec.Key = key
	if rec.Value, err = ReadVarBytesBuf(r, pver, buf, DKVSValueSizeLimit(key), "dkvs value"); err != nil {
		return nil, err
	}
	if rec.PubKey, err = ReadVarBytesBuf(r, pver, buf, MaxDKVSPubKeySize, "dkvs pubkey"); err != nil {
		return nil, err
	}
	if rec.Signature, err = ReadVarBytesBuf(r, pver, buf, MaxDKVSSignatureSize, "dkvs signature"); err != nil {
		return nil, err
	}
	if err := readElements(r, &rec.Seq, &rec.IssueTime, &rec.TTL, &rec.ExpiryHeight); err != nil {
		return nil, err
	}
	if rec.FeeProof, err = ReadVarBytesBuf(r, pver, buf, MaxDKVSFeeProofSize, "dkvs fee proof"); err != nil {
		return nil, err
	}
	if err := readElements(r, &rec.Flags); err != nil {
		return nil, err
	}
	if dkvsRecordSerializeSize(rec) > DKVSRecordSizeLimit(key) {
		return nil, messageError("readDKVSRecord", "dkvs record too large")
	}
	return rec, nil
}

func writeDKVSRecord(w io.Writer, pver uint32, rec *DKVSRecord, buf []byte) error {
	if rec == nil {
		return messageError("writeDKVSRecord", "nil dkvs record")
	}
	if len(rec.Key) > MaxDKVSKeySize {
		return messageError("writeDKVSRecord", "dkvs key too large")
	}
	if len(rec.Value) > int(DKVSValueSizeLimit(rec.Key)) ||
		len(rec.PubKey) > MaxDKVSPubKeySize || len(rec.Signature) > MaxDKVSSignatureSize ||
		len(rec.FeeProof) > MaxDKVSFeeProofSize {
		return messageError("writeDKVSRecord", "dkvs record field too large")
	}
	if dkvsRecordSerializeSize(rec) > DKVSRecordSizeLimit(rec.Key) {
		return messageError("writeDKVSRecord", "dkvs record too large")
	}
	if err := writeElements(w, rec.Version); err != nil {
		return err
	}
	if err := writeVarStringBuf(w, pver, rec.Key, buf); err != nil {
		return err
	}
	for _, field := range [][]byte{rec.Value, rec.PubKey, rec.Signature} {
		if err := WriteVarBytesBuf(w, pver, field, buf); err != nil {
			return err
		}
	}
	if err := writeElements(w, rec.Seq, rec.IssueTime, rec.TTL, rec.ExpiryHeight); err != nil {
		return err
	}
	if err := WriteVarBytesBuf(w, pver, rec.FeeProof, buf); err != nil {
		return err
	}
	return writeElements(w, rec.Flags)
}

func dkvsRecordSerializeSize(rec *DKVSRecord) int {
	if rec == nil {
		return 0
	}
	return 4 +
		VarIntSerializeSize(uint64(len(rec.Key))) + len(rec.Key) +
		VarIntSerializeSize(uint64(len(rec.Value))) + len(rec.Value) +
		VarIntSerializeSize(uint64(len(rec.PubKey))) + len(rec.PubKey) +
		VarIntSerializeSize(uint64(len(rec.Signature))) + len(rec.Signature) +
		32 +
		VarIntSerializeSize(uint64(len(rec.FeeProof))) + len(rec.FeeProof) +
		4
}

// DKVSRecordSerializeSize returns the exact encoded size of a DKVS record.
func DKVSRecordSerializeSize(rec *DKVSRecord) int {
	return dkvsRecordSerializeSize(rec)
}

func validateDKVSRecordsPayload(records []*DKVSRecord, fixedSize int) error {
	total := fixedSize + VarIntSerializeSize(uint64(len(records)))
	for _, record := range records {
		if record == nil {
			return messageError("validateDKVSRecordsPayload", "nil dkvs record")
		}
		total += dkvsRecordSerializeSize(record)
		if total > MaxDKVSRecordsPayloadSize {
			return messageError("validateDKVSRecordsPayload", "dkvs records payload too large")
		}
	}
	return nil
}

// SerializeDKVSRecord encodes a standalone DKVS record using the wire codec.
func SerializeDKVSRecord(rec *DKVSRecord) ([]byte, error) {
	var encoded bytes.Buffer
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := writeDKVSRecord(&encoded, ProtocolVersion, rec, buf); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

// DeserializeDKVSRecord decodes one standalone DKVS record.
func DeserializeDKVSRecord(encoded []byte) (*DKVSRecord, error) {
	reader := bytes.NewReader(encoded)
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	record, err := readDKVSRecord(reader, ProtocolVersion, buf)
	if err != nil {
		return nil, err
	}
	if reader.Len() != 0 {
		return nil, messageError("DeserializeDKVSRecord", "trailing dkvs record bytes")
	}
	return record, nil
}

func readDKVSInvItem(r io.Reader, pver uint32, buf []byte) (DKVSInvItem, error) {
	var item DKVSInvItem
	key, err := readVarStringBuf(r, pver, buf)
	if err != nil {
		return item, err
	}
	if len(key) > MaxDKVSKeySize {
		return item, messageError("readDKVSInvItem", "dkvs key too large")
	}
	item.Key = key
	if _, err := io.ReadFull(r, item.RecordHash[:]); err != nil {
		return item, err
	}
	err = readElements(r, &item.Seq)
	return item, err
}

func writeDKVSInvItem(w io.Writer, pver uint32, item DKVSInvItem, buf []byte) error {
	if len(item.Key) > MaxDKVSKeySize {
		return messageError("writeDKVSInvItem", "dkvs key too large")
	}
	if err := writeVarStringBuf(w, pver, item.Key, buf); err != nil {
		return err
	}
	if _, err := w.Write(item.RecordHash[:]); err != nil {
		return err
	}
	return writeElements(w, item.Seq)
}

func readHashList(r io.Reader, pver uint32, buf []byte, max uint32, field string) ([]chainhash.Hash, error) {
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return nil, err
	}
	if count > uint64(max) {
		return nil, messageError(field, fmt.Sprintf("too many hashes %d", count))
	}
	hashes := make([]chainhash.Hash, 0, count)
	for i := uint64(0); i < count; i++ {
		var h chainhash.Hash
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return nil, err
		}
		hashes = append(hashes, h)
	}
	return hashes, nil
}

func writeHashList(w io.Writer, pver uint32, hashes []chainhash.Hash, max uint32, field string, buf []byte) error {
	if len(hashes) > int(max) {
		return messageError(field, fmt.Sprintf("too many hashes %d", len(hashes)))
	}
	if err := WriteVarIntBuf(w, pver, uint64(len(hashes)), buf); err != nil {
		return err
	}
	for i := range hashes {
		if _, err := w.Write(hashes[i][:]); err != nil {
			return err
		}
	}
	return nil
}

func readerLen(r io.Reader) int {
	type lenReader interface{ Len() int }
	if lr, ok := r.(lenReader); ok {
		return lr.Len()
	}
	return -1
}

func (msg *MsgDKVSNotify) BtcDecode(r io.Reader, pver uint32, _ MessageEncoding) error {
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := readElements(r, &msg.EventType); err != nil {
		return err
	}
	if msg.EventType == 0 {
		return messageError("MsgDKVSNotify.BtcDecode", "missing dkvs notify event type")
	}
	data, err := ReadVarBytesBuf(r, pver, buf, MaxDKVSNotifyDataSize, "dkvs notify data")
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return messageError("MsgDKVSNotify.BtcDecode", "missing dkvs notify data")
	}
	msg.Data = data
	return nil
}

func (msg *MsgDKVSNotify) BtcEncode(w io.Writer, pver uint32, _ MessageEncoding) error {
	if msg.EventType == 0 {
		return messageError("MsgDKVSNotify.BtcEncode", "missing dkvs notify event type")
	}
	if len(msg.Data) == 0 {
		return messageError("MsgDKVSNotify.BtcEncode", "missing dkvs notify data")
	}
	if len(msg.Data) > MaxDKVSNotifyDataSize {
		return messageError("MsgDKVSNotify.BtcEncode", "dkvs notify data too large")
	}
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := writeElements(w, msg.EventType); err != nil {
		return err
	}
	return WriteVarBytesBuf(w, pver, msg.Data, buf)
}

func (msg *MsgDKVSNotify) Command() string { return CmdDKVSNotify }
func (msg *MsgDKVSNotify) MaxPayloadLength(uint32) uint32 {
	return 1 + MaxVarIntPayload + MaxDKVSNotifyDataSize
}

func (msg *MsgDKVSInv) BtcDecode(r io.Reader, pver uint32, _ MessageEncoding) error {
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	if count > MaxDKVSItemsPerMsg {
		return messageError("MsgDKVSInv.BtcDecode", "too many dkvs inv items")
	}
	msg.Items = make([]DKVSInvItem, 0, count)
	for i := uint64(0); i < count; i++ {
		item, err := readDKVSInvItem(r, pver, buf)
		if err != nil {
			return err
		}
		msg.Items = append(msg.Items, item)
	}
	return nil
}

func (msg *MsgDKVSInv) BtcEncode(w io.Writer, pver uint32, _ MessageEncoding) error {
	if len(msg.Items) > MaxDKVSItemsPerMsg {
		return messageError("MsgDKVSInv.BtcEncode", "too many dkvs inv items")
	}
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := WriteVarIntBuf(w, pver, uint64(len(msg.Items)), buf); err != nil {
		return err
	}
	for _, item := range msg.Items {
		if err := writeDKVSInvItem(w, pver, item, buf); err != nil {
			return err
		}
	}
	return nil
}

func (msg *MsgDKVSInv) Command() string { return CmdDKVSInv }
func (msg *MsgDKVSInv) MaxPayloadLength(uint32) uint32 {
	return MaxVarIntPayload + MaxDKVSItemsPerMsg*(MaxVarIntPayload+MaxDKVSKeySize+chainhash.HashSize+8)
}

func (msg *MsgDKVSGet) BtcDecode(r io.Reader, pver uint32, _ MessageEncoding) error {
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	if count > MaxDKVSItemsPerMsg {
		return messageError("MsgDKVSGet.BtcDecode", "too many dkvs keys")
	}
	msg.Keys = make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		key, err := readVarStringBuf(r, pver, buf)
		if err != nil {
			return err
		}
		if len(key) > MaxDKVSKeySize {
			return messageError("MsgDKVSGet.BtcDecode", "dkvs key too large")
		}
		msg.Keys = append(msg.Keys, key)
	}
	msg.RecordHashes, err = readHashList(r, pver, buf, MaxDKVSItemsPerMsg, "MsgDKVSGet.BtcDecode")
	return err
}

func (msg *MsgDKVSGet) BtcEncode(w io.Writer, pver uint32, _ MessageEncoding) error {
	if len(msg.Keys) > MaxDKVSItemsPerMsg {
		return messageError("MsgDKVSGet.BtcEncode", "too many dkvs keys")
	}
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := WriteVarIntBuf(w, pver, uint64(len(msg.Keys)), buf); err != nil {
		return err
	}
	for _, key := range msg.Keys {
		if len(key) > MaxDKVSKeySize {
			return messageError("MsgDKVSGet.BtcEncode", "dkvs key too large")
		}
		if err := writeVarStringBuf(w, pver, key, buf); err != nil {
			return err
		}
	}
	return writeHashList(w, pver, msg.RecordHashes, MaxDKVSItemsPerMsg, "MsgDKVSGet.BtcEncode", buf)
}

func (msg *MsgDKVSGet) Command() string { return CmdDKVSGet }
func (msg *MsgDKVSGet) MaxPayloadLength(uint32) uint32 {
	return MaxVarIntPayload + MaxDKVSItemsPerMsg*(MaxVarIntPayload+MaxDKVSKeySize) +
		MaxVarIntPayload + MaxDKVSItemsPerMsg*chainhash.HashSize
}

func (msg *MsgDKVSData) BtcDecode(r io.Reader, pver uint32, _ MessageEncoding) error {
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	if count > MaxDKVSRecordsPerMsg {
		return messageError("MsgDKVSData.BtcDecode", "too many dkvs records")
	}
	msg.Records = make([]*DKVSRecord, 0, count)
	payloadSize := VarIntSerializeSize(count)
	for i := uint64(0); i < count; i++ {
		rec, err := readDKVSRecord(r, pver, buf)
		if err != nil {
			return err
		}
		payloadSize += dkvsRecordSerializeSize(rec)
		if payloadSize > MaxDKVSRecordsPayloadSize {
			return messageError("MsgDKVSData.BtcDecode", "dkvs records payload too large")
		}
		msg.Records = append(msg.Records, rec)
	}
	msg.NotFound, err = readHashList(r, pver, buf, MaxDKVSItemsPerMsg, "MsgDKVSData.BtcDecode")
	return err
}

func (msg *MsgDKVSData) BtcEncode(w io.Writer, pver uint32, _ MessageEncoding) error {
	if len(msg.Records) > MaxDKVSRecordsPerMsg {
		return messageError("MsgDKVSData.BtcEncode", "too many dkvs records")
	}
	if err := validateDKVSRecordsPayload(msg.Records, 0); err != nil {
		return err
	}
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := WriteVarIntBuf(w, pver, uint64(len(msg.Records)), buf); err != nil {
		return err
	}
	for _, rec := range msg.Records {
		if err := writeDKVSRecord(w, pver, rec, buf); err != nil {
			return err
		}
	}
	return writeHashList(w, pver, msg.NotFound, MaxDKVSItemsPerMsg, "MsgDKVSData.BtcEncode", buf)
}

func (msg *MsgDKVSData) Command() string                { return CmdDKVSData }
func (msg *MsgDKVSData) MaxPayloadLength(uint32) uint32 { return MaxProtocolMessageLength }

func (msg *MsgDKVSSyncRequest) BtcDecode(r io.Reader, pver uint32, _ MessageEncoding) error {
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := readElements(r, &msg.SessionID); err != nil {
		return err
	}
	cursor, err := ReadVarBytesBuf(r, pver, buf, MaxDKVSCursorSize, "dkvs cursor")
	if err != nil {
		return err
	}
	msg.Cursor = cursor
	if err := readElements(r, &msg.Limit); err != nil {
		return err
	}
	if readerLen(r) == 0 {
		return nil
	}
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	if count > MaxDKVSSyncFilters {
		return messageError("MsgDKVSSyncRequest.BtcDecode", "too many dkvs sync filters")
	}
	msg.Filters = make([]DKVSSyncFilter, 0, count)
	for i := uint64(0); i < count; i++ {
		filterType, err := readVarStringBuf(r, pver, buf)
		if err != nil {
			return err
		}
		if len(filterType) > MaxDKVSFilterTypeSize {
			return messageError("MsgDKVSSyncRequest.BtcDecode", "dkvs sync filter type too large")
		}
		target, err := readVarStringBuf(r, pver, buf)
		if err != nil {
			return err
		}
		if len(target) > MaxDKVSKeySize {
			return messageError("MsgDKVSSyncRequest.BtcDecode", "dkvs sync filter target too large")
		}
		msg.Filters = append(msg.Filters, DKVSSyncFilter{Type: filterType, Target: target})
	}
	return nil
}

func (msg *MsgDKVSSyncRequest) BtcEncode(w io.Writer, pver uint32, _ MessageEncoding) error {
	if len(msg.Cursor) > MaxDKVSCursorSize {
		return messageError("MsgDKVSSyncRequest.BtcEncode", "dkvs cursor too large")
	}
	if len(msg.Filters) > MaxDKVSSyncFilters {
		return messageError("MsgDKVSSyncRequest.BtcEncode", "too many dkvs sync filters")
	}
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := writeElements(w, msg.SessionID); err != nil {
		return err
	}
	if err := WriteVarBytesBuf(w, pver, msg.Cursor, buf); err != nil {
		return err
	}
	if err := writeElements(w, msg.Limit); err != nil {
		return err
	}
	if len(msg.Filters) == 0 {
		return nil
	}
	if err := WriteVarIntBuf(w, pver, uint64(len(msg.Filters)), buf); err != nil {
		return err
	}
	for _, filter := range msg.Filters {
		if len(filter.Type) > MaxDKVSFilterTypeSize {
			return messageError("MsgDKVSSyncRequest.BtcEncode", "dkvs sync filter type too large")
		}
		if len(filter.Target) > MaxDKVSKeySize {
			return messageError("MsgDKVSSyncRequest.BtcEncode", "dkvs sync filter target too large")
		}
		if err := writeVarStringBuf(w, pver, filter.Type, buf); err != nil {
			return err
		}
		if err := writeVarStringBuf(w, pver, filter.Target, buf); err != nil {
			return err
		}
	}
	return nil
}

func (msg *MsgDKVSSyncRequest) Command() string { return CmdDKVSSyncRequest }
func (msg *MsgDKVSSyncRequest) MaxPayloadLength(uint32) uint32 {
	return 8 + MaxVarIntPayload + MaxDKVSCursorSize + 4 + MaxVarIntPayload +
		MaxDKVSSyncFilters*(MaxVarIntPayload+MaxDKVSFilterTypeSize+MaxVarIntPayload+MaxDKVSKeySize)
}

func (msg *MsgDKVSSyncResponse) BtcDecode(r io.Reader, pver uint32, _ MessageEncoding) error {
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := readElements(r, &msg.SessionID); err != nil {
		return err
	}
	count, err := ReadVarIntBuf(r, pver, buf)
	if err != nil {
		return err
	}
	if count > MaxDKVSRecordsPerMsg {
		return messageError("MsgDKVSSyncResponse.BtcDecode", "too many dkvs records")
	}
	msg.Records = make([]*DKVSRecord, 0, count)
	payloadSize := 8 + VarIntSerializeSize(count)
	for i := uint64(0); i < count; i++ {
		rec, err := readDKVSRecord(r, pver, buf)
		if err != nil {
			return err
		}
		payloadSize += dkvsRecordSerializeSize(rec)
		if payloadSize > MaxDKVSRecordsPayloadSize {
			return messageError("MsgDKVSSyncResponse.BtcDecode", "dkvs records payload too large")
		}
		msg.Records = append(msg.Records, rec)
	}
	msg.NextCursor, err = ReadVarBytesBuf(r, pver, buf, MaxDKVSCursorSize, "dkvs cursor")
	if err != nil {
		return err
	}
	var done uint8
	if err := readElements(r, &done); err != nil {
		return err
	}
	msg.Done = done != 0
	if _, err = io.ReadFull(r, msg.CheckpointRoot[:]); err != nil {
		return err
	}
	msg.SourceSignature, err = ReadVarBytesBuf(r, pver, buf, MaxDKVSSignatureSize, "dkvs sync source signature")
	if len(msg.SourceSignature) == 0 {
		msg.SourceSignature = nil
	}
	return err
}

func (msg *MsgDKVSSyncResponse) BtcEncode(w io.Writer, pver uint32, _ MessageEncoding) error {
	if len(msg.Records) > MaxDKVSRecordsPerMsg {
		return messageError("MsgDKVSSyncResponse.BtcEncode", "too many dkvs records")
	}
	if len(msg.NextCursor) > MaxDKVSCursorSize {
		return messageError("MsgDKVSSyncResponse.BtcEncode", "dkvs cursor too large")
	}
	if err := validateDKVSRecordsPayload(msg.Records, 8); err != nil {
		return err
	}
	buf := binarySerializer.Borrow()
	defer binarySerializer.Return(buf)
	if err := writeElements(w, msg.SessionID); err != nil {
		return err
	}
	if err := WriteVarIntBuf(w, pver, uint64(len(msg.Records)), buf); err != nil {
		return err
	}
	for _, rec := range msg.Records {
		if err := writeDKVSRecord(w, pver, rec, buf); err != nil {
			return err
		}
	}
	if err := WriteVarBytesBuf(w, pver, msg.NextCursor, buf); err != nil {
		return err
	}
	var done uint8
	if msg.Done {
		done = 1
	}
	if err := writeElements(w, done); err != nil {
		return err
	}
	if _, err := w.Write(msg.CheckpointRoot[:]); err != nil {
		return err
	}
	if len(msg.SourceSignature) > MaxDKVSSignatureSize {
		return messageError("MsgDKVSSyncResponse.BtcEncode", "dkvs sync source signature too large")
	}
	return WriteVarBytesBuf(w, pver, msg.SourceSignature, buf)
}

func (msg *MsgDKVSSyncResponse) Command() string { return CmdDKVSSyncResponse }
func (msg *MsgDKVSSyncResponse) MaxPayloadLength(uint32) uint32 {
	return MaxProtocolMessageLength
}
