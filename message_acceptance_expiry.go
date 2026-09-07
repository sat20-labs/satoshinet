package main

import (
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// Sender-side Direct acceptance is itself a free cache. It must not outlive
// the standard FREE_LOCAL retention window even though the recipient may have
// purchased durable mailbox storage.
const defaultDirectAcceptanceTTL = time.Duration(dkvs.DefaultFreeLocalMaxTTLBlocks) * 12 * time.Second

type directAcceptancePruner interface {
	PruneExpiredDirect(now time.Time) error
}

func acceptedDirectGeneratedAt(message acceptedMessage) (time.Time, bool) {
	if !wire.ValidMessageID(message.MessageID) || len(message.Data) == 0 {
		return time.Time{}, false
	}
	envelope, err := UnmarshalMessageEnvelope(message.Data)
	if err != nil || envelope.MessageType != MessageTypeDirect {
		return time.Time{}, false
	}
	raw, err := hex.DecodeString(message.MessageID)
	if err != nil || len(raw) != wire.MessageIDHexSize/2 {
		return time.Time{}, false
	}
	micros := binary.BigEndian.Uint64(raw[:8])
	if micros == 0 || micros > uint64(^uint64(0)>>1) {
		return time.Time{}, false
	}
	return time.UnixMicro(int64(micros)), true
}

func acceptedDirectExpired(message acceptedMessage, now time.Time) bool {
	generatedAt, ok := acceptedDirectGeneratedAt(message)
	return ok && !generatedAt.After(now) && now.Sub(generatedAt) >= defaultDirectAcceptanceTTL
}

func (m *MessageManager) pruneExpiredDirectAcceptances() error {
	if m == nil || m.accepted == nil {
		return nil
	}
	pruner, ok := m.accepted.(directAcceptancePruner)
	if !ok {
		return nil
	}
	return pruner.PruneExpiredDirect(time.Now())
}
