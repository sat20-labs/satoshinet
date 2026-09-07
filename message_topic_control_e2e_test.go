package main

import (
	"errors"
	"strconv"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedTopicMembershipRequestForTest(t *testing.T, priv *btcec.PrivateKey, account, topic, serviceCore, requestType string) *TopicMembershipRequest {
	t.Helper()
	request := &TopicMembershipRequest{
		TopicName: topic, AccountID: account, ServiceCoreNode: serviceCore, RequestType: requestType,
	}
	hash, err := wire.TopicMembershipRequestSigningHash(request)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	request.Signature = sig.Serialize()
	return request
}

func signedTopicJoinRejectionForTest(t *testing.T, ownerPriv *btcec.PrivateKey, owner, topic, serviceCore, target string) *TopicJoinRejection {
	t.Helper()
	rejection := &TopicJoinRejection{
		TopicName: topic, ServiceCoreNode: serviceCore, OwnerAccount: owner, TargetAccount: target,
	}
	hash, err := wire.TopicJoinRejectionSigningHash(rejection)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(ownerPriv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	rejection.OwnerSignature = sig.Serialize()
	return rejection
}

func requireTopicKeyPackageRecord(t *testing.T, idx *dkvs.Indexer, recipient, topic string, keySeq uint64, issuer string) {
	t.Helper()
	key, err := dkvs.MailTopicKeyKey(recipient, topic, strconv.FormatUint(keySeq, 10))
	if err != nil {
		t.Fatal(err)
	}
	record, err := idx.Get(key)
	if err != nil {
		t.Fatalf("topic key package %s seq=%d missing: %v", recipient, keySeq, err)
	}
	fanout, err := wire.DeserializeTopicKeyFanoutMessage(record.Value)
	if err != nil || fanout.TopicName != topic || fanout.KeySeq != keySeq || fanout.IssuerAccount != issuer ||
		len(fanout.Recipients) != 1 || fanout.Recipients[0].Recipient != recipient {
		t.Fatalf("topic key package=%#v err=%v", fanout, err)
	}
}

func TestDKVSMessageE2ETopicOwnerApprovalLeaveWithoutOwnerApproval(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	memberBPriv, memberB := testAccount(t)
	memberCPriv, memberC := testAccount(t)
	memberDPriv, memberD := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{
		owner: "host", memberB: "core-b", memberC: "core-c", memberD: "core-d",
	}}
	network := newMessageE2ENetwork("bootstrap", "host", "bootstrap", "core-b", "core-c", "core-d")
	for _, core := range []string{"host", "core-b", "core-c", "core-d"} {
		if core != "bootstrap" {
			network.connect(core, "bootstrap")
		}
	}
	hostIdx := newMessageE2EIndexer(t)
	idxB := newMessageE2EIndexer(t)
	idxC := newMessageE2EIndexer(t)
	idxD := newMessageE2EIndexer(t)
	host := attachMessageE2EManager(network.nodes["host"], bindings, e2eMailbox(hostIdx), nil)
	managerB := attachMessageE2EManager(network.nodes["core-b"], bindings, e2eMailbox(idxB), nil)
	managerC := attachMessageE2EManager(network.nodes["core-c"], bindings, e2eMailbox(idxC), nil)
	managerD := attachMessageE2EManager(network.nodes["core-d"], bindings, e2eMailbox(idxD), nil)
	catalog := NewDKVSTopicCatalogStore(e2eTopicBackend{idx: hostIdx})
	if err := host.topics.SetCatalogStore(catalog); err != nil {
		t.Fatal(err)
	}
	if err := host.topics.CreateTopic(TopicMeta{
		TopicName: "developers", DisplayName: "Developers", OwnerAccount: owner,
		ServiceCoreNode: "host", MaxMembers: 16,
	}); err != nil {
		t.Fatal(err)
	}

	// B requests to join through its own CoreNode. The Service CoreNode records
	// only PENDING_JOIN; KeySeq and effective membership remain unchanged until
	// the Topic Owner approves with a key-rotation commit.
	if err := managerB.SubmitTopicMembershipRequest(signedTopicMembershipRequestForTest(
		t, memberBPriv, memberB, "developers", "host", wire.TopicMembershipJoin)); err != nil {
		t.Fatal(err)
	}
	assertTopicMemberStatusForTest(t, host.topics, "developers", memberB, "PENDING_JOIN")
	state, _, _ := host.topics.TopicState("developers")
	if state.KeySeq != 1 || state.MemberCount != 1 {
		t.Fatalf("join request changed effective state=%+v", state)
	}
	joinB := signedTopicMembershipCommitForTest(t, ownerPriv, owner, "developers", "host", 1,
		wire.TopicMembershipAdd, memberB, []string{owner, memberB})
	if err := host.SubmitTopicMembershipCommit(joinB); err != nil {
		t.Fatal(err)
	}
	assertTopicMemberStatusForTest(t, host.topics, "developers", memberB, "ACTIVE")
	requireTopicKeyPackageRecord(t, idxB, memberB, "developers", 2, owner)

	// C joins the same way, producing a new epoch delivered to all three key
	// holders.
	if err := managerC.SubmitTopicMembershipRequest(signedTopicMembershipRequestForTest(
		t, memberCPriv, memberC, "developers", "host", wire.TopicMembershipJoin)); err != nil {
		t.Fatal(err)
	}
	joinC := signedTopicMembershipCommitForTest(t, ownerPriv, owner, "developers", "host", 2,
		wire.TopicMembershipAdd, memberC, []string{owner, memberB, memberC})
	if err := host.SubmitTopicMembershipCommit(joinC); err != nil {
		t.Fatal(err)
	}
	requireTopicKeyPackageRecord(t, idxB, memberB, "developers", 3, owner)
	requireTopicKeyPackageRecord(t, idxC, memberC, "developers", 3, owner)

	// B leaves unilaterally. The request immediately revokes publish permission,
	// but B remains a current-key holder until a remaining member completes the
	// cryptographic rotation.
	if err := managerB.SubmitTopicMembershipRequest(signedTopicMembershipRequestForTest(
		t, memberBPriv, memberB, "developers", "host", wire.TopicMembershipLeave)); err != nil {
		t.Fatal(err)
	}
	assertTopicMemberStatusForTest(t, host.topics, "developers", memberB, "PENDING_LEAVE")
	state, _, _ = host.topics.TopicState("developers")
	if state.KeySeq != 3 || state.MemberCount != 3 {
		t.Fatalf("leave request changed key-holder state=%+v", state)
	}

	// A PENDING_LEAVE member cannot publish. Host rejection must ACK the sender
	// CoreNode so its accepted-message queue does not remain stuck forever.
	stalePublish := signedTestTopic(t, memberBPriv, memberB, "developers", 0, 3, "should-be-rejected")
	if err := managerB.topics.SendTopicMessageToHost(stalePublish, "host"); err != nil {
		t.Fatal(err)
	}
	pendingB, err := managerB.accepted.(pendingMessageAcceptanceStore).Pending()
	if err != nil || len(pendingB) != 0 || managerB.GetNextSenderMsgID(memberB) != 1 {
		t.Fatalf("rejected publish pending=%#v next=%d err=%v", pendingB, managerB.GetNextSenderMsgID(memberB), err)
	}

	// Leaving also removes B from new fan-out immediately, even though B still
	// counts as a holder of the old key until the REMOVE rotation commits.
	duringLeave := signedTestTopic(t, ownerPriv, owner, "developers", 0, 3, "during-pending-leave")
	if err := host.topics.SendTopicMessage(duringLeave); err != nil {
		t.Fatal(err)
	}
	bDuringLeave, err := dkvs.MailTopicMessageKey(memberB, "developers", owner, duringLeave.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idxB.Get(bDuringLeave); !errors.Is(err, dkvs.ErrRecordNotFound) {
		t.Fatalf("PENDING_LEAVE member received new message: %v", err)
	}
	cDuringLeave, _ := dkvs.MailTopicMessageKey(memberC, "developers", owner, duringLeave.MessageID)
	if _, err := idxC.Get(cDuringLeave); err != nil {
		t.Fatalf("ACTIVE member missed pending-leave fan-out: %v", err)
	}

	// C, not the Topic Owner, is allowed to finish B's self-requested leave.
	// This is cryptographic coordination, not membership approval.
	leaveB := signedTopicMembershipCommitForTest(t, memberCPriv, memberC, "developers", "host", 3,
		wire.TopicMembershipRemove, memberB, []string{owner, memberC})
	if err := managerC.SubmitTopicMembershipCommit(leaveB); err != nil {
		t.Fatal(err)
	}
	state, members, err := host.topics.TopicState("developers")
	if err != nil || state.KeySeq != 4 || state.MemberCount != 2 {
		t.Fatalf("post-leave state=%+v members=%d err=%v", state, len(members), err)
	}
	assertTopicMemberStatusForTest(t, host.topics, "developers", memberB, "LEFT")
	requireTopicKeyPackageRecord(t, idxC, memberC, "developers", 4, memberC)
	if key, _ := dkvs.MailTopicKeyKey(memberB, "developers", "4"); key != "" {
		if _, err := idxB.Get(key); !errors.Is(err, dkvs.ErrRecordNotFound) {
			t.Fatalf("left member received new key: %v", err)
		}
	}

	// C can publish under the new epoch; B is absent from fanout.
	publishC := signedTestTopic(t, memberCPriv, memberC, "developers", 0, 4, "post-leave-message")
	if err := managerC.topics.SendTopicMessageToHost(publishC, "host"); err != nil {
		t.Fatal(err)
	}
	ownerMsgKey, err := dkvs.MailTopicMessageKey(owner, "developers", memberC, publishC.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostIdx.Get(ownerMsgKey); err != nil {
		t.Fatalf("owner topic mailbox missing post-leave message: %v", err)
	}
	leftMsgKey, _ := dkvs.MailTopicMessageKey(memberB, "developers", memberC, publishC.MessageID)
	if _, err := idxB.Get(leftMsgKey); !errors.Is(err, dkvs.ErrRecordNotFound) {
		t.Fatalf("left member received post-leave message: %v", err)
	}

	// D's join can be explicitly rejected by the Owner without rotating the
	// current TopicKey because D never possessed it.
	if err := managerD.SubmitTopicMembershipRequest(signedTopicMembershipRequestForTest(
		t, memberDPriv, memberD, "developers", "host", wire.TopicMembershipJoin)); err != nil {
		t.Fatal(err)
	}
	assertTopicMemberStatusForTest(t, host.topics, "developers", memberD, "PENDING_JOIN")
	if err := host.SubmitTopicJoinRejection(signedTopicJoinRejectionForTest(
		t, ownerPriv, owner, "developers", "host", memberD)); err != nil {
		t.Fatal(err)
	}
	assertTopicMemberStatusForTest(t, host.topics, "developers", memberD, "LEFT")
	state, _, _ = host.topics.TopicState("developers")
	if state.KeySeq != 4 || state.MemberCount != 2 {
		t.Fatalf("join rejection rotated key unexpectedly: %+v", state)
	}

	// Host restart preserves pending/terminal membership states and the current
	// key epoch in the host-local Topic catalog.
	restarted := newTestMessageManager("host", bindings, e2eMailbox(hostIdx), messageE2ERouter{node: network.nodes["host"]}, nil, nil)
	if err := restarted.topics.SetCatalogStore(catalog); err != nil {
		t.Fatal(err)
	}
	restartedState, restartedMembers, err := restarted.topics.TopicState("developers")
	if err != nil || restartedState.KeySeq != 4 || restartedState.MemberCount != 2 || len(restartedMembers) != 4 {
		t.Fatalf("restart state=%+v members=%d err=%v", restartedState, len(restartedMembers), err)
	}
}
