package dkvs

import "github.com/sat20-labs/satoshinet/wire"

// EventMessage is deliberately outside the record-notify event family. Its
// payload is an opaque MessageManager envelope and must never be decoded as a
// DKVSRecord by the ordinary P2P handler.
const EventMessage = wire.DKVSNotifyEventMessage

type ReplicationMode uint8

const (
	ReplicationNetwork ReplicationMode = iota
	ReplicationAccountBound
	ReplicationLocalOnly
)

func (mode ReplicationMode) String() string {
	switch mode {
	case ReplicationNetwork:
		return "network"
	case ReplicationAccountBound:
		return "account_bound"
	case ReplicationLocalOnly:
		return "local_only"
	default:
		return "unknown"
	}
}

func replicationMode(parsed ParsedKey, record *wire.DKVSRecord) ReplicationMode {
	if parsed.Namespace == "mail" {
		return ReplicationAccountBound
	}
	// Topic service state is authoritative on its Host CoreNode. TopicManager
	// persists it in DKVS for crash recovery, but it is not ordinary globally
	// replicated DKVS state and never contains chat history or TopicKey plaintext.
	if parsed.Namespace == "topic" {
		return ReplicationLocalOnly
	}
	if record != nil && isFreeLocalRecord(record) {
		return ReplicationLocalOnly
	}
	if parsed.Namespace == "tmp" {
		return ReplicationLocalOnly
	}
	return ReplicationNetwork
}

func ReplicationModeForKey(key string) (ReplicationMode, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return ReplicationNetwork, err
	}
	return replicationMode(parsed, nil), nil
}

func isAccountBoundRecord(record *wire.DKVSRecord) bool {
	if record == nil {
		return false
	}
	parsed, err := ParseKey(record.Key)
	return err == nil && parsed.Namespace == "mail"
}

func isHostLocalTopicRecord(record *wire.DKVSRecord) bool {
	return record != nil && isInternalTopicRecord(record)
}

func isPeerRelayableRecord(record *wire.DKVSRecord) bool {
	return record != nil && !isAccountBoundRecord(record) && !isHostLocalTopicRecord(record) && !isFreeLocalRecord(record)
}
