package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// TopicCatalogValueVersion versions DKVS catalog values independently of the
// HTTP/P2P topic messages and their signing encodings.
const TopicCatalogValueVersion byte = 1

// SerializeTopicMeta encodes a versioned DKVS catalog value.
func SerializeTopicMeta(value *TopicMeta) ([]byte, error) {
	if value == nil {
		return nil, fmt.Errorf("nil topic catalog value")
	}
	var buf bytes.Buffer
	buf.WriteByte(TopicCatalogValueVersion)
	topicWriteString(&buf, value.TopicName)
	topicWriteString(&buf, value.DisplayName)
	topicWriteString(&buf, value.OwnerAccount)
	topicWriteString(&buf, value.ServiceCoreNode)
	topicWriteBytes(&buf, value.ServicePubKey)
	topicWriteString(&buf, value.JoinPolicy)
	topicWriteString(&buf, value.MessagePolicy)
	var scratch [4]byte
	binary.BigEndian.PutUint32(scratch[:], value.MaxMembers)
	buf.Write(scratch[:])
	topicWriteU64(&buf, value.CreatedAtHeight)
	if buf.Len() > MaxDKVSValueSize {
		return nil, fmt.Errorf("topic catalog value too large")
	}
	return buf.Bytes(), nil
}

// DeserializeTopicMeta rejects unsupported versions and trailing data.
func DeserializeTopicMeta(encoded []byte) (*TopicMeta, error) {
	if len(encoded) == 0 || len(encoded) > MaxDKVSValueSize || encoded[0] != TopicCatalogValueVersion {
		return nil, fmt.Errorf("invalid topic catalog value")
	}
	r := bytes.NewReader(encoded[1:])
	var value TopicMeta
	var err error
	value.TopicName, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.DisplayName, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.OwnerAccount, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.ServiceCoreNode, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.ServicePubKey, err = topicReadBytes(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.JoinPolicy, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.MessagePolicy, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	err = binary.Read(r, binary.BigEndian, &value.MaxMembers)
	if err != nil {
		return nil, err
	}
	value.CreatedAtHeight, err = topicReadU64(r)
	if err != nil {
		return nil, err
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trailing topic catalog data")
	}
	return &value, nil
}

// SerializeTopicState encodes a versioned DKVS catalog value.
func SerializeTopicState(value *TopicState) ([]byte, error) {
	if value == nil {
		return nil, fmt.Errorf("nil topic catalog value")
	}
	var buf bytes.Buffer
	buf.WriteByte(TopicCatalogValueVersion)
	topicWriteString(&buf, value.Status)
	topicWriteU64(&buf, value.KeySeq)
	var scratch [4]byte
	binary.BigEndian.PutUint32(scratch[:], value.MemberCount)
	buf.Write(scratch[:])
	topicWriteString(&buf, value.LastCommitHash)
	if buf.Len() > MaxDKVSValueSize {
		return nil, fmt.Errorf("topic catalog value too large")
	}
	return buf.Bytes(), nil
}

// DeserializeTopicState rejects unsupported versions and trailing data.
func DeserializeTopicState(encoded []byte) (*TopicState, error) {
	if len(encoded) == 0 || len(encoded) > MaxDKVSValueSize || encoded[0] != TopicCatalogValueVersion {
		return nil, fmt.Errorf("invalid topic catalog value")
	}
	r := bytes.NewReader(encoded[1:])
	var value TopicState
	var err error
	value.Status, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.KeySeq, err = topicReadU64(r)
	if err != nil {
		return nil, err
	}
	err = binary.Read(r, binary.BigEndian, &value.MemberCount)
	if err != nil {
		return nil, err
	}
	value.LastCommitHash, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trailing topic catalog data")
	}
	return &value, nil
}

// SerializeTopicMember encodes a versioned DKVS catalog value.
func SerializeTopicMember(value *TopicMember) ([]byte, error) {
	if value == nil {
		return nil, fmt.Errorf("nil topic catalog value")
	}
	var buf bytes.Buffer
	buf.WriteByte(TopicCatalogValueVersion)
	topicWriteString(&buf, value.AccountID)
	topicWriteString(&buf, value.Status)
	topicWriteString(&buf, value.Role)
	topicWriteU64(&buf, value.JoinedSeq)
	topicWriteU64(&buf, value.LeftSeq)
	if buf.Len() > MaxDKVSValueSize {
		return nil, fmt.Errorf("topic catalog value too large")
	}
	return buf.Bytes(), nil
}

// DeserializeTopicMember rejects unsupported versions and trailing data.
func DeserializeTopicMember(encoded []byte) (*TopicMember, error) {
	if len(encoded) == 0 || len(encoded) > MaxDKVSValueSize || encoded[0] != TopicCatalogValueVersion {
		return nil, fmt.Errorf("invalid topic catalog value")
	}
	r := bytes.NewReader(encoded[1:])
	var value TopicMember
	var err error
	value.AccountID, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.Status, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.Role, err = topicReadString(r, MaxDKVSValueSize)
	if err != nil {
		return nil, err
	}
	value.JoinedSeq, err = topicReadU64(r)
	if err != nil {
		return nil, err
	}
	value.LeftSeq, err = topicReadU64(r)
	if err != nil {
		return nil, err
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trailing topic catalog data")
	}
	return &value, nil
}
