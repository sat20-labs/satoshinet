package dkvs

import (
	"sort"
	"strings"
	"sync"
)

type subscriptionSet struct {
	mutex sync.RWMutex
	items map[Subscription]struct{}
}

func newSubscriptionSet() *subscriptionSet {
	return &subscriptionSet{items: make(map[Subscription]struct{})}
}

func (s *subscriptionSet) add(sub Subscription) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.items[sub]; ok {
		return false
	}
	s.items[sub] = struct{}{}
	return true
}

func (s *subscriptionSet) remove(sub Subscription) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.items, sub)
}

func (s *subscriptionSet) list() []Subscription {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	subs := make([]Subscription, 0, len(s.items))
	for sub := range s.items {
		subs = append(subs, sub)
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].Type == subs[j].Type {
			return subs[i].Target < subs[j].Target
		}
		return subs[i].Type < subs[j].Type
	})
	return subs
}

func (s *subscriptionSet) matchKey(key string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for sub := range s.items {
		if subscriptionMatchesKey(sub, key) {
			return true
		}
	}
	return false
}

func validateSubscription(sub Subscription) (Subscription, error) {
	sub.Type = SubscriptionType(strings.ToLower(strings.TrimSpace(string(sub.Type))))
	sub.Target = strings.TrimSpace(sub.Target)
	if sub.Target == "" {
		return sub, ErrInvalidKey
	}
	switch sub.Type {
	case SubscriptionKey:
		if _, err := ParseKey(sub.Target); err != nil {
			return sub, err
		}
	case SubscriptionPrefix:
		if _, err := ParsePrefix(sub.Target); err != nil {
			return sub, err
		}
		sub.Target = strings.TrimSuffix(sub.Target, "/")
	case SubscriptionMailbox:
		mailboxID := strings.TrimPrefix(strings.TrimSuffix(sub.Target, "/"), "/mail/")
		if mailboxID == "" || strings.Contains(mailboxID, "/") || !validSegment(mailboxID) {
			return sub, ErrInvalidKey
		}
		sub.Target = "/mail/" + mailboxID
	case SubscriptionService:
		serviceName := strings.TrimPrefix(strings.TrimSuffix(sub.Target, "/"), "/svc/")
		if serviceName == "" || strings.Contains(serviceName, "/") || !validSegment(serviceName) {
			return sub, ErrInvalidKey
		}
		sub.Target = "/svc/" + serviceName
	default:
		return sub, ErrInvalidRecord
	}
	return sub, nil
}

func subscriptionMatchesKey(sub Subscription, key string) bool {
	switch sub.Type {
	case SubscriptionKey:
		return key == sub.Target
	case SubscriptionPrefix, SubscriptionMailbox, SubscriptionService:
		return key == sub.Target || strings.HasPrefix(key, sub.Target+"/")
	default:
		return false
	}
}
