package main

import (
	"fmt"
	"sync"
	"time"
)

// DirectAdmissionPolicy bounds free Direct traffic before sender sequence or
// durable acceptance state is consumed. Message and byte limits share one
// fixed window so large application payloads cannot bypass count limits.
type DirectAdmissionPolicy struct {
	Window                  time.Duration
	MaxMessagesPerSender    uint64
	MaxBytesPerSender       uint64
	MaxMessagesPerPair      uint64
	MaxBytesPerPair         uint64
	MaxMessagesPerRecipient uint64
	MaxBytesPerRecipient    uint64
	MaxMessagesTotal        uint64
	MaxBytesTotal           uint64
}

func DefaultDirectAdmissionPolicy() DirectAdmissionPolicy {
	return DirectAdmissionPolicy{
		Window:                  time.Minute,
		MaxMessagesPerSender:    60,
		MaxBytesPerSender:       16 << 20,
		MaxMessagesPerPair:      20,
		MaxBytesPerPair:         8 << 20,
		MaxMessagesPerRecipient: 300,
		MaxBytesPerRecipient:    64 << 20,
		MaxMessagesTotal:        10_000,
		MaxBytesTotal:           1 << 30,
	}
}

type directAdmissionUsage struct {
	windowStart time.Time
	messages    uint64
	bytes       uint64
}

type DirectRateLimitError struct{ RetryAfter time.Duration }

func (e *DirectRateLimitError) Error() string {
	if e == nil {
		return ErrMessageRateLimited.Error()
	}
	return fmt.Sprintf("%s: retry after %s", ErrMessageRateLimited, e.RetryAfter)
}

func (e *DirectRateLimitError) Unwrap() error { return ErrMessageRateLimited }

type directAdmissionLimiter struct {
	mu     sync.Mutex
	policy DirectAdmissionPolicy
	now    func() time.Time
	usage  map[string]directAdmissionUsage
}

func newDirectAdmissionLimiter(policy DirectAdmissionPolicy) *directAdmissionLimiter {
	if policy.Window <= 0 {
		policy = DefaultDirectAdmissionPolicy()
	}
	return &directAdmissionLimiter{policy: policy, now: time.Now, usage: make(map[string]directAdmissionUsage)}
}

func (l *directAdmissionLimiter) currentLocked(key string, now time.Time) directAdmissionUsage {
	usage := l.usage[key]
	if usage.windowStart.IsZero() || now.Sub(usage.windowStart) >= l.policy.Window || now.Before(usage.windowStart) {
		usage = directAdmissionUsage{windowStart: now}
	}
	return usage
}

func admissionExceeded(usage directAdmissionUsage, size, maxMessages, maxBytes uint64) bool {
	return maxMessages == 0 || maxBytes == 0 || usage.messages+1 > maxMessages || usage.bytes+size > maxBytes
}

func (l *directAdmissionLimiter) Admit(direction, sender, recipient string, size int) error {
	if l == nil || direction == "" || !validateAccountID(sender) || !validateAccountID(recipient) || size <= 0 {
		return ErrMessageInvalidEnvelope
	}
	now := l.now()
	amount := uint64(size)
	keys := []string{
		direction + ":sender:" + sender,
		direction + ":pair:" + sender + ":" + recipient,
		direction + ":recipient:" + recipient,
		direction + ":total",
	}
	limits := [][2]uint64{
		{l.policy.MaxMessagesPerSender, l.policy.MaxBytesPerSender},
		{l.policy.MaxMessagesPerPair, l.policy.MaxBytesPerPair},
		{l.policy.MaxMessagesPerRecipient, l.policy.MaxBytesPerRecipient},
		{l.policy.MaxMessagesTotal, l.policy.MaxBytesTotal},
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	current := make([]directAdmissionUsage, len(keys))
	for index, key := range keys {
		current[index] = l.currentLocked(key, now)
		if admissionExceeded(current[index], amount, limits[index][0], limits[index][1]) {
			retry := l.policy.Window - now.Sub(current[index].windowStart)
			if retry <= 0 {
				retry = time.Millisecond
			}
			return &DirectRateLimitError{RetryAfter: retry}
		}
	}
	for index, key := range keys {
		current[index].messages++
		current[index].bytes += amount
		l.usage[key] = current[index]
	}
	return nil
}
