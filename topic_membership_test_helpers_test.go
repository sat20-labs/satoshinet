package main

import (
	"fmt"
	"sort"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

// forceTopicActiveMembersForTest prepares a post-rotation stable snapshot for
// tests whose subject is fan-out/retry rather than membership authorization.
// It persists the snapshot when a catalog is configured.
func forceTopicActiveMembersForTest(t *testing.T, manager *TopicManager, topicName string, keySeq uint64, accounts ...string) {
	t.Helper()
	if manager == nil || keySeq == 0 {
		t.Fatal("invalid topic test setup")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	topic := manager.topics[topicName]
	if topic == nil {
		t.Fatalf("topic %s not found", topicName)
	}
	for _, account := range accounts {
		if account == topic.meta.OwnerAccount {
			continue
		}
		topic.members[account] = TopicMember{
			AccountID: account,
			Status:    "ACTIVE",
			Role:      "MEMBER",
			JoinedSeq: keySeq,
		}
	}
	count := uint32(0)
	for _, member := range topic.members {
		if topicMemberHoldsCurrentKey(member) {
			count++
		}
	}
	topic.state.KeySeq = keySeq
	topic.state.MemberCount = count
	if err := manager.persistTopicLocked(topic); err != nil {
		t.Fatal(err)
	}
}

func signedTopicPackageForTest(t *testing.T, ownerPriv *btcec.PrivateKey, owner, topic string, keySeq uint64, recipient string) TopicKeyPackageRecipient {
	t.Helper()
	item := TopicKeyPackageRecipient{
		Recipient:         recipient,
		EncryptedTopicKey: []byte("encrypted-topic-key-for-" + recipient),
	}
	hash, err := wire.TopicKeyPackageSigningHash(topic, keySeq, owner, item)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(ownerPriv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	item.IssuerSignature = sig.Serialize()
	return item
}

func signedTopicMembershipCommitForTest(t *testing.T, ownerPriv *btcec.PrivateKey, owner, topic, serviceCore string,
	baseKeySeq uint64, changeType, target string, postChangeKeyHolders []string) *TopicMembershipCommit {
	t.Helper()
	newKeySeq := baseKeySeq + 1
	holders := append([]string(nil), postChangeKeyHolders...)
	sort.Strings(holders)
	packages := make([]TopicKeyPackageRecipient, 0, len(holders))
	for _, account := range holders {
		packages = append(packages, signedTopicPackageForTest(t, ownerPriv, owner, topic, newKeySeq, account))
	}
	commit := &TopicMembershipCommit{
		TopicName:       topic,
		ServiceCoreNode: serviceCore,
		IssuerAccount:   owner,
		BaseKeySeq:      baseKeySeq,
		NewKeySeq:       newKeySeq,
		ChangeType:      changeType,
		TargetAccount:   target,
		KeyPackages:     packages,
	}
	hash, err := wire.TopicMembershipCommitSigningHash(commit)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(ownerPriv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	commit.IssuerSignature = sig.Serialize()
	return commit
}

func topicKeyHoldersForTest(t *testing.T, manager *TopicManager, topicName string) []string {
	t.Helper()
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	topic := manager.topics[topicName]
	if topic == nil {
		t.Fatalf("topic %s not found", topicName)
	}
	return topicKeyHolderAccounts(topic)
}

func commitTopicJoinForTest(t *testing.T, manager *TopicManager, ownerPriv *btcec.PrivateKey, owner, topic, account string) uint64 {
	t.Helper()
	base, err := manager.RequestJoin(topic, account)
	if err != nil {
		t.Fatal(err)
	}
	holders := topicKeyHoldersForTest(t, manager, topic)
	holders = append(holders, account)
	commit := signedTopicMembershipCommitForTest(t, ownerPriv, owner, topic, manager.manager.localCore, base, wire.TopicMembershipAdd, account, holders)
	if err := manager.CommitMembershipChange(commit); err != nil {
		t.Fatalf("commit join %s: %v", account, err)
	}
	return commit.NewKeySeq
}

func commitTopicLeaveForTest(t *testing.T, manager *TopicManager, ownerPriv *btcec.PrivateKey, owner, topic, account string) uint64 {
	t.Helper()
	base, err := manager.RequestLeave(topic, account)
	if err != nil {
		t.Fatal(err)
	}
	holders := topicKeyHoldersForTest(t, manager, topic)
	filtered := holders[:0]
	for _, holder := range holders {
		if holder != account {
			filtered = append(filtered, holder)
		}
	}
	commit := signedTopicMembershipCommitForTest(t, ownerPriv, owner, topic, manager.manager.localCore, base, wire.TopicMembershipRemove, account, filtered)
	if err := manager.CommitMembershipChange(commit); err != nil {
		t.Fatalf("commit leave %s: %v", account, err)
	}
	return commit.NewKeySeq
}

func assertTopicMemberStatusForTest(t *testing.T, manager *TopicManager, topicName, account, want string) {
	t.Helper()
	_, members, err := manager.TopicState(topicName)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.AccountID == account {
			if member.Status != want {
				t.Fatalf("topic %s member %s status=%s want=%s", topicName, account, member.Status, want)
			}
			return
		}
	}
	t.Fatalf("topic %s member %s missing; want status %s", topicName, account, want)
}

func describeTopicStateForTest(state TopicState, members []TopicMember) string {
	return fmt.Sprintf("state=%+v members=%d", state, len(members))
}
