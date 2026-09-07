package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/wire"
)

const (
	messageServiceAuthMaxFuture = 2 * time.Minute
	messageServiceQueryWindow   = time.Minute
	messageServiceQueryLimit    = uint64(120)
)

type messageServiceQueryUsage struct {
	windowStart time.Time
	count       uint64
}

type MessageServiceQueryRateLimitError struct{ RetryAfter time.Duration }

func (e *MessageServiceQueryRateLimitError) Error() string {
	return fmt.Sprintf("%s: retry after %s", ErrMessageRateLimited, e.RetryAfter)
}

func (e *MessageServiceQueryRateLimitError) Unwrap() error { return ErrMessageRateLimited }

type messageServiceQueryGuard struct {
	mu     sync.Mutex
	now    func() time.Time
	nonces map[string]time.Time
	usage  map[string]messageServiceQueryUsage
}

func newMessageServiceQueryGuard() *messageServiceQueryGuard {
	return &messageServiceQueryGuard{
		now: time.Now, nonces: make(map[string]time.Time),
		usage: make(map[string]messageServiceQueryUsage),
	}
}

func (g *messageServiceQueryGuard) Authorize(request *wire.MessageServiceRequest) error {
	if g == nil || request == nil || request.Auth == nil {
		return ErrMessageInvalidSignature
	}
	nonce, err := hex.DecodeString(request.Auth.Nonce)
	if err != nil || len(nonce) != 16 || request.Auth.Nonce != hex.EncodeToString(nonce) {
		return ErrMessageInvalidSignature
	}
	now := g.now()
	expiresAt := time.UnixMilli(int64(request.Auth.ExpiresAtMS))
	if !expiresAt.After(now) || expiresAt.After(now.Add(messageServiceAuthMaxFuture)) {
		return ErrMessageInvalidSignature
	}
	hash, err := wire.MessageServiceQuerySigningHash(request)
	if err != nil || verifyAccountSchnorr(request.AccountID, request.Auth.Signature, hash) != nil {
		return ErrMessageInvalidSignature
	}

	action := strings.ToUpper(strings.TrimSpace(request.Action))
	nonceKey := request.AccountID + ":" + request.Auth.Nonce
	usageKey := request.AccountID + ":" + action
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, expiry := range g.nonces {
		if !expiry.After(now) {
			delete(g.nonces, key)
		}
	}
	if _, replayed := g.nonces[nonceKey]; replayed {
		return ErrMessageInvalidSignature
	}
	usage := g.usage[usageKey]
	if usage.windowStart.IsZero() || now.Before(usage.windowStart) || now.Sub(usage.windowStart) >= messageServiceQueryWindow {
		usage = messageServiceQueryUsage{windowStart: now}
	}
	if usage.count >= messageServiceQueryLimit {
		retry := messageServiceQueryWindow - now.Sub(usage.windowStart)
		if retry <= 0 {
			retry = time.Millisecond
		}
		return &MessageServiceQueryRateLimitError{RetryAfter: retry}
	}
	g.nonces[nonceKey] = expiresAt
	usage.count++
	g.usage[usageKey] = usage
	return nil
}
