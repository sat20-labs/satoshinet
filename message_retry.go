package main

import (
	"time"
)

const defaultMessageRetryInterval = 5 * time.Second

// refreshAcceptedTarget re-resolves addressable business targets before a
// durable outgoing job is retried. The MessageID/digest stay unchanged, so the
// acceptance store only updates routing metadata and never charges or advances
// SenderMsgID a second time.
func (m *MessageManager) refreshAcceptedTarget(accepted acceptedMessage) (acceptedMessage, error) {
	if m == nil || m.accepted == nil || m.bindings == nil {
		return accepted, ErrMessageInvalidEnvelope
	}
	envelope, err := UnmarshalMessageEnvelope(accepted.Data)
	if err != nil {
		return accepted, err
	}
	var target string
	switch envelope.MessageType {
	case MessageTypeDirect:
		direct, err := UnmarshalDirectMessage(envelope.Payload)
		if err != nil {
			return accepted, err
		}
		target, err = m.bindings.CoreNodeForAccount(direct.RecipientAccount)
		if err != nil || target == "" {
			return accepted, ErrMessageBindingNotFound
		}
	default:
		// TopicPublish targets a Topic Host identity rather than an account.
		// Its Host migration is a Topic service operation, not account binding
		// resolution, so keep the durable target unchanged here.
		return accepted, nil
	}
	if target == accepted.Target {
		return accepted, nil
	}
	updated, _, err := m.accepted.Accept(
		accepted.SenderAccount, accepted.SenderMsgID, accepted.MessageID, accepted.Digest,
		target, accepted.Data, nil,
	)
	if err != nil {
		return accepted, err
	}
	return updated, nil
}

// RetryPendingResolved retries durable sender jobs after refreshing any target
// whose placement is account-bound. It is safe after restart and preserves the
// original MessageID and billing admission.
func (m *MessageManager) RetryPendingResolved() error {
	if m == nil || m.router == nil || m.accepted == nil {
		return ErrMessageInvalidEnvelope
	}
	store, ok := m.accepted.(pendingMessageAcceptanceStore)
	if !ok {
		return nil
	}
	if err := m.pruneExpiredDirectAcceptances(); err != nil {
		return err
	}
	pending, err := store.Pending()
	if err != nil {
		return err
	}
	var firstErr error
	for _, accepted := range pending {
		if !m.retryReady(accepted) {
			continue
		}
		refreshed, err := m.refreshAcceptedTarget(accepted)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := m.routeAccepted(refreshed, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RunRetryLoop is intentionally an application-layer loop. Bootstrap does not
// become a store-and-forward service; durable retry ownership remains with the
// sender CoreNode and Topic Host.
func (m *MessageManager) RunRetryLoop(stop <-chan struct{}, interval time.Duration) {
	if m == nil {
		return
	}
	if interval <= 0 {
		interval = defaultMessageRetryInterval
	}
	retry := func() {
		_ = m.RetryPendingResolved()
		if m.topics != nil {
			_ = m.topics.RetryPendingResolved()
		}
	}
	// Recover durable jobs promptly after node restart/configuration instead of
	// waiting a full interval for the first attempt.
	retry()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			retry()
		case <-stop:
			return
		}
	}
}
