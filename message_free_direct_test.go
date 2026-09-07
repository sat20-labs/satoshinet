package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type captureMailboxWriter struct{ records []*wire.DKVSRecord }

func (w *captureMailboxWriter) PutDKVSInternalMailbox(record *wire.DKVSRecord) (bool, error) {
	copyRecord := *record
	copyRecord.Value = append([]byte(nil), record.Value...)
	copyRecord.FeeProof = append([]byte(nil), record.FeeProof...)
	w.records = append(w.records, &copyRecord)
	return true, nil
}

type fixedMessageRetention struct {
	paid bool
	err  error
}

func (r fixedMessageRetention) HasActiveSubscription(string) (bool, error) {
	return r.paid, r.err
}

func TestDirectMailboxUsesFreeLocalOrPaidRetention(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")

	freeWriter := &captureMailboxWriter{}
	freeStore := NewDKVSMessageMailboxStoreWithRetention(freeWriter, func() uint64 { return 100 },
		func(string) uint64 { return 25 }, nil, fixedMessageRetention{})
	if _, err := freeStore.AppendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	if len(freeWriter.records) != 1 || freeWriter.records[0].TTL != 25 {
		t.Fatalf("free record=%+v", freeWriter.records)
	}
	proof, err := dkvs.ParseFeeProof(freeWriter.records[0].FeeProof)
	if err != nil || proof.Mode != dkvs.FeeModeFreeLocal {
		t.Fatalf("free proof=%+v err=%v", proof, err)
	}

	paidWriter := &captureMailboxWriter{}
	paidStore := NewDKVSMessageMailboxStoreWithRetention(paidWriter, func() uint64 { return 100 },
		func(string) uint64 { return 25 }, nil, fixedMessageRetention{paid: true})
	if _, err := paidStore.AppendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	if len(paidWriter.records) != 1 || paidWriter.records[0].TTL != 0 || len(paidWriter.records[0].FeeProof) != 0 {
		t.Fatalf("paid record=%+v", paidWriter.records)
	}
}

func TestDirectMailboxRetentionLookupFailureDoesNotWrite(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")
	want := errors.New("autopay state unavailable")
	writer := &captureMailboxWriter{}
	store := NewDKVSMessageMailboxStoreWithRetention(writer, func() uint64 { return 100 },
		func(string) uint64 { return 25 }, nil, fixedMessageRetention{err: want})
	if _, err := store.AppendDirectMessage(message); !errors.Is(err, want) {
		t.Fatalf("retention error=%v", err)
	}
	if len(writer.records) != 0 {
		t.Fatalf("retention failure wrote %d records", len(writer.records))
	}
}

func TestDirectRateLimitRejectsBeforeSenderSequenceAcceptance(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	manager := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, &testMessageRouter{}, &testMessageCharger{}, nil)
	policy := DefaultDirectAdmissionPolicy()
	policy.Window = time.Minute
	policy.MaxMessagesPerSender = 1
	limiter := newDirectAdmissionLimiter(policy)
	now := time.Unix(1_000, 0)
	limiter.now = func() time.Time { return now }
	manager.directLimiter = limiter

	if err := manager.SendDirectMessage(signedTestDirect(t, senderPriv, sender, recipient, 0, "first")); err != nil {
		t.Fatal(err)
	}
	second := signedTestDirect(t, senderPriv, sender, recipient, 1, "second")
	err := manager.SendDirectMessage(second)
	if !errors.Is(err, ErrMessageRateLimited) || directRetryAfterMS(err) == 0 {
		t.Fatalf("rate-limit err=%v retry=%d", err, directRetryAfterMS(err))
	}
	if got := manager.GetNextSenderMsgID(sender); got != 1 {
		t.Fatalf("rate-limit consumed SenderMsgID next=%d", got)
	}
	now = now.Add(time.Minute)
	if err := manager.SendDirectMessage(second); err != nil {
		t.Fatal(err)
	}
	if got := manager.GetNextSenderMsgID(sender); got != 2 {
		t.Fatalf("post-window next=%d", got)
	}
}

func TestDirectRetryAdviceDefersPendingRoute(t *testing.T) {
	_, sender := testAccount(t)
	manager := newTestMessageManager("core-a", &testMessageBindings{}, &testMessageMailbox{}, &testMessageRouter{}, nil, nil)
	messageID := testStableMessageID("retry-advice")
	accepted := acceptedMessage{SenderAccount: sender, MessageID: messageID}
	manager.recordRetryAdvice(&MessageAck{
		OriginalType: MessageTypeDirect, SenderAccount: sender, MessageID: messageID,
		Status: MessageAckRetryable, RetryAfterMS: 60_000,
	})
	if manager.retryReady(accepted) {
		t.Fatal("retry_after advice was ignored")
	}
	key := acceptedMessageKey(sender, messageID)
	manager.retryMu.Lock()
	manager.retryNotBefore[key] = time.Now().Add(-time.Millisecond)
	manager.retryMu.Unlock()
	if !manager.retryReady(accepted) {
		t.Fatal("expired retry_after still blocked pending route")
	}
}

func TestDirectAcceptanceCacheExpiresWithFreeLocalTTL(t *testing.T) {
	_, sender := testAccount(t)
	_, recipient := testAccount(t)
	generatedAt := time.Now().Add(-defaultDirectAcceptanceTTL - time.Minute)
	var raw [wire.MessageIDHexSize / 2]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(generatedAt.UnixMicro()))
	copy(raw[8:], []byte("entropy!"))
	messageID := hex.EncodeToString(raw[:])
	direct := &DirectMessage{
		SenderAccount: sender, RecipientAccount: recipient, MessageID: messageID,
		Ciphertext: []byte("ciphertext"), SenderSignature: []byte{1},
	}
	payload, err := MarshalDirectMessage(direct, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeDirect, SourceCoreNode: "core-a", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryMessageAcceptanceStore()
	store.accepted[acceptedMessageKey(sender, messageID)] = acceptedMessage{
		SenderAccount: sender, MessageID: messageID, Target: "core-b", Data: data,
	}
	if err := store.PruneExpiredDirect(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(store.accepted) != 0 {
		t.Fatalf("expired Direct acceptances=%d", len(store.accepted))
	}
}
