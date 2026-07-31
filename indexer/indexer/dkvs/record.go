package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

var signatureDomain = []byte("satoshinet-dkvs-record-v1")

const notifyEventMagic = "DKNE"

func cloneRecord(record *wire.DKVSRecord) *wire.DKVSRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Value = append([]byte(nil), record.Value...)
	cloned.PubKey = append([]byte(nil), record.PubKey...)
	cloned.Signature = append([]byte(nil), record.Signature...)
	cloned.FeeProof = append([]byte(nil), record.FeeProof...)
	return &cloned
}

func RecordSize(record *wire.DKVSRecord) int {
	return wire.DKVSRecordSerializeSize(record)
}

func RecordHash(record *wire.DKVSRecord) chainhash.Hash {
	b := canonicalRecordBytes(record, true)
	return chainhash.DoubleHashH(b)
}

func KeyHash(key string) chainhash.Hash {
	return chainhash.DoubleHashH([]byte(key))
}

func SigningHash(record *wire.DKVSRecord) [32]byte {
	return sha256.Sum256(canonicalRecordBytes(record, false))
}

func SigningMessage(record *wire.DKVSRecord) []byte {
	return canonicalRecordBytes(record, false)
}

func VerifySignature(record *wire.DKVSRecord) error {
	if record == nil || record.Version != Version || len(record.Signature) == 0 {
		return ErrInvalidSignature
	}
	if len(record.PubKey) == 0 {
		return verifyAccountSignature(record)
	}
	pubKey, err := btcec.ParsePubKey(record.PubKey)
	if err != nil {
		return err
	}
	sig, err := ecdsa.ParseSignature(record.Signature)
	if err != nil {
		return err
	}
	hash := SigningHash(record)
	if !sig.Verify(hash[:], pubKey) {
		return ErrInvalidSignature
	}
	return nil
}

// CompareRecords selects the deterministic winner for two records of the same
// key. Sequence is the primary order. At the same sequence, retention may only
// choose a winner when the signed business state is identical; otherwise
// IssueTime and finally RecordHash decide the winner.
func CompareRecords(a, b *wire.DKVSRecord) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if a.Seq > b.Seq {
		return 1
	}
	if a.Seq < b.Seq {
		return -1
	}
	if sameBusinessContent(a, b) {
		if retention := compareRetention(a, b); retention != 0 {
			return retention
		}
	}
	if a.IssueTime > b.IssueTime {
		return 1
	}
	if a.IssueTime < b.IssueTime {
		return -1
	}
	ah := RecordHash(a)
	bh := RecordHash(b)
	return bytes.Compare(ah[:], bh[:])
}

func sameBusinessContent(a, b *wire.DKVSRecord) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Version == b.Version &&
		a.Key == b.Key &&
		a.Seq == b.Seq &&
		a.Flags == b.Flags &&
		bytes.Equal(a.Value, b.Value) &&
		bytes.Equal(a.PubKey, b.PubKey)
}

// compareRetention compares only retention fields. Zero means no protocol
// expiry and is therefore longer than any finite retention.
func compareRetention(a, b *wire.DKVSRecord) int {
	if a == nil || b == nil {
		return 0
	}
	if result := compareOptionalUpperBound(a.ExpiryHeight, b.ExpiryHeight); result != 0 {
		return result
	}
	aExpiry := recordExpiryTime(a)
	bExpiry := recordExpiryTime(b)
	return compareOptionalUpperBound(aExpiry, bExpiry)
}

func compareOptionalUpperBound(a, b uint64) int {
	if a == b {
		return 0
	}
	if a == 0 {
		return 1
	}
	if b == 0 {
		return -1
	}
	if a > b {
		return 1
	}
	return -1
}

func IsExpired(record *wire.DKVSRecord, height uint64, now uint64) bool {
	if record == nil {
		return true
	}
	if record.ExpiryHeight != 0 && height != 0 && record.ExpiryHeight <= height {
		return true
	}
	if record.TTL != 0 && record.IssueTime != 0 && now > record.IssueTime+record.TTL {
		return true
	}
	return false
}

func MarshalRecord(record *wire.DKVSRecord) ([]byte, error) {
	return wire.SerializeDKVSRecord(record)
}

func UnmarshalRecord(data []byte) (*wire.DKVSRecord, error) {
	return wire.DeserializeDKVSRecord(data)
}

func NewNotifyEvent(eventType uint8, record *wire.DKVSRecord) (*NotifyEvent, error) {
	if record == nil {
		return nil, ErrInvalidRecord
	}
	if err := ValidateNotifyEventRecord(eventType, record); err != nil {
		return nil, err
	}
	data, err := MarshalRecord(record)
	if err != nil {
		return nil, err
	}
	return &NotifyEvent{
		EventType: eventType,
		Data:      data,
		Relay:     true,
	}, nil
}

func ValidateNotifyEventRecord(eventType uint8, record *wire.DKVSRecord) error {
	if record == nil {
		return ErrInvalidRecord
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return err
	}
	tombstone := IsTombstone(record.Flags)
	switch eventType {
	case EventRecordTombstone:
		if !tombstone || len(record.Value) != 0 {
			return ErrInvalidRecord
		}
	case EventMailboxMessage:
		if tombstone || parsed.Namespace != "mail" || len(parsed.Segments) < 2 || parsed.Segments[1] != "msg" {
			return ErrInvalidRecord
		}
	case EventCheckpointReady:
		if tombstone || parsed.Namespace != "sys" || len(parsed.Segments) < 2 || parsed.Segments[0] != "checkpoint" {
			return ErrInvalidRecord
		}
	case EventSnapshotReady:
		if tombstone || parsed.Namespace != "sys" || len(parsed.Segments) < 2 || parsed.Segments[0] != "snapshot" {
			return ErrInvalidRecord
		}
	case EventRecordPut, EventRecordUpdate, EventRenewal:
		if tombstone {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidRecord
	}
	return nil
}

func RecordFromNotifyEvent(event *NotifyEvent) (*wire.DKVSRecord, error) {
	if event == nil || len(event.Data) == 0 || len(event.Data) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrInvalidRecord
	}
	record, err := UnmarshalRecord(event.Data)
	if err != nil {
		return nil, err
	}
	if err := ValidateNotifyEventRecord(event.EventType, record); err != nil {
		return nil, err
	}
	return record, nil
}

func MarshalNotifyEvent(event *NotifyEvent) ([]byte, error) {
	if event == nil {
		return nil, ErrInvalidRecord
	}
	if _, err := RecordFromNotifyEvent(event); err != nil {
		return nil, err
	}
	if len(event.Data) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrInvalidRecord
	}
	buf := make([]byte, len(notifyEventMagic)+1+4+len(event.Data))
	copy(buf, notifyEventMagic)
	buf[len(notifyEventMagic)] = event.EventType
	binary.BigEndian.PutUint32(buf[len(notifyEventMagic)+1:], uint32(len(event.Data)))
	copy(buf[len(notifyEventMagic)+1+4:], event.Data)
	return buf, nil
}

func UnmarshalNotifyEvent(data []byte) (*NotifyEvent, error) {
	const headerSize = len(notifyEventMagic) + 1 + 4
	if len(data) < headerSize || string(data[:len(notifyEventMagic)]) != notifyEventMagic {
		return nil, ErrInvalidRecord
	}
	dataLen := binary.BigEndian.Uint32(data[len(notifyEventMagic)+1:])
	if int(dataLen) != len(data)-headerSize || dataLen == 0 || dataLen > wire.MaxDKVSNotifyDataSize {
		return nil, ErrInvalidRecord
	}
	event := &NotifyEvent{EventType: data[len(notifyEventMagic)], Data: append([]byte(nil), data[headerSize:]...)}
	if _, err := RecordFromNotifyEvent(event); err != nil {
		return nil, err
	}
	return event, nil
}

func currentUnixMilli() uint64 {
	return uint64(time.Now().UnixMilli())
}

func canonicalRecordBytes(record *wire.DKVSRecord, includeSignature bool) []byte {
	var buf bytes.Buffer
	writeBytes(&buf, signatureDomain)
	if record == nil {
		return buf.Bytes()
	}
	writeUint32(&buf, record.Version)
	writeString(&buf, record.Key)
	writeHash(&buf, sha256.Sum256(record.Value))
	writeBytes(&buf, record.PubKey)
	writeUint64(&buf, record.Seq)
	writeUint64(&buf, record.PathGeneration)
	writeUint64(&buf, record.IssueTime)
	writeUint64(&buf, record.TTL)
	writeUint64(&buf, record.ExpiryHeight)
	writeHash(&buf, sha256.Sum256(record.FeeProof))
	writeUint32(&buf, record.Flags)
	if includeSignature {
		writeBytes(&buf, record.Signature)
	}
	return buf.Bytes()
}

func writeString(buf *bytes.Buffer, s string) {
	writeBytes(buf, []byte(s))
}

func writeBytes(buf *bytes.Buffer, b []byte) {
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], uint64(len(b)))
	buf.Write(scratch[:])
	buf.Write(b)
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[:], v)
	buf.Write(scratch[:])
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], v)
	buf.Write(scratch[:])
}

func writeHash(buf *bytes.Buffer, h [32]byte) {
	buf.Write(h[:])
}

func HexRecordHash(record *wire.DKVSRecord) string {
	h := RecordHash(record)
	return hex.EncodeToString(h[:])
}
