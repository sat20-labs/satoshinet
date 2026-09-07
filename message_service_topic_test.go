package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func authorizedMessageQuery(t *testing.T, priv *btcec.PrivateKey, request *wire.MessageServiceRequest) *wire.MessageServiceRequest {
	t.Helper()
	request.Auth = &wire.MessageServiceAuth{
		Nonce:       testStableMessageID("query", request.Action, request.AccountID, request.TopicName, fmt.Sprint(time.Now().UnixNano())),
		ExpiresAtMS: uint64(time.Now().Add(time.Minute).UnixMilli()),
	}
	hash, err := wire.MessageServiceQuerySigningHash(request)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	request.Auth.Signature = signature.Serialize()
	return request
}

func TestDKVSMessageServiceQueryAuthRejectsReplayAndTampering(t *testing.T) {
	priv, account := testAccount(t)
	guard := newMessageServiceQueryGuard()
	request := authorizedMessageQuery(t, priv, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionNextMessage, AccountID: account,
	})
	if err := guard.Authorize(request); err != nil {
		t.Fatalf("valid query rejected: %v", err)
	}
	if err := guard.Authorize(request); !errors.Is(err, ErrMessageInvalidSignature) {
		t.Fatalf("replayed query err=%v", err)
	}
	tampered := authorizedMessageQuery(t, priv, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionTopicState, AccountID: account, TopicName: "topic-a",
	})
	tampered.TopicName = "topic-b"
	if err := guard.Authorize(tampered); !errors.Is(err, ErrMessageInvalidSignature) {
		t.Fatalf("tampered query err=%v", err)
	}
}

func callMessageServiceJSONForTest(t *testing.T, server *server, request *wire.MessageServiceRequest) *wire.MessageServiceResponse {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := server.handleMessageServiceJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	var response wire.MessageServiceResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func TestDKVSMessageServiceTopicActionsViaBootstrap(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	memberPriv, member := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{owner: "host", member: "core-b"}}
	network := newMessageE2ENetwork("bootstrap", "host", "bootstrap", "core-b")
	network.connect("host", "bootstrap")
	network.connect("core-b", "bootstrap")
	hostIdx := newMessageE2EIndexer(t)
	memberIdx := newMessageE2EIndexer(t)
	hostManager := attachMessageE2EManager(network.nodes["host"], bindings, e2eMailbox(hostIdx), nil)
	memberManager := attachMessageE2EManager(network.nodes["core-b"], bindings, e2eMailbox(memberIdx), nil)
	catalog := NewDKVSTopicCatalogStore(e2eTopicBackend{idx: hostIdx})
	if err := hostManager.topics.SetCatalogStore(catalog); err != nil {
		t.Fatal(err)
	}

	hostServer := &server{miningPubKey: "host", messageBindingResolver: bindings}
	memberServer := &server{miningPubKey: "core-b", messageBindingResolver: bindings}
	serverMessageManagers.Store(hostServer, hostManager)
	serverMessageManagers.Store(memberServer, memberManager)
	t.Cleanup(func() {
		serverMessageManagers.Delete(hostServer)
		serverMessageManagers.Delete(memberServer)
	})

	create := &TopicCreateRequest{Meta: TopicMeta{
		TopicName: "developers", DisplayName: "Developers", OwnerAccount: owner,
		ServiceCoreNode: "host", MaxMembers: 16,
	}}
	createHash, err := wire.TopicCreateRequestSigningHash(create)
	if err != nil {
		t.Fatal(err)
	}
	createSig, err := schnorr.Sign(ownerPriv, createHash[:])
	if err != nil {
		t.Fatal(err)
	}
	create.OwnerSignature = createSig.Serialize()
	createPayload, err := wire.EncodeTopicJSON(create)
	if err != nil {
		t.Fatal(err)
	}
	if response := callMessageServiceJSONForTest(t, hostServer, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionCreateTopic, Payload: createPayload,
	}); response.Code != 0 {
		t.Fatalf("create topic response=%+v", response)
	}

	stateResponse := callMessageServiceJSONForTest(t, hostServer, authorizedMessageQuery(t, ownerPriv, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionTopicState, AccountID: owner, TopicName: "developers",
	}))
	if stateResponse.Code != 0 {
		t.Fatalf("topic state response=%+v", stateResponse)
	}
	var initial TopicServiceSnapshot
	if wire.DecodeTopicJSON(stateResponse.Payload, &initial) != nil || initial.State.KeySeq != 1 || initial.State.MemberCount != 1 {
		t.Fatalf("initial state=%+v", initial)
	}

	join := signedTopicMembershipRequestForTest(t, memberPriv, member, "developers", "host", wire.TopicMembershipJoin)
	joinPayload, _ := wire.EncodeTopicJSON(join)
	if response := callMessageServiceJSONForTest(t, memberServer, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionTopicJoin, Payload: joinPayload,
	}); response.Code != 0 {
		t.Fatalf("join response=%+v", response)
	}
	assertTopicMemberStatusForTest(t, hostManager.topics, "developers", member, "PENDING_JOIN")

	commit := signedTopicMembershipCommitForTest(t, ownerPriv, owner, "developers", "host", 1,
		wire.TopicMembershipAdd, member, []string{owner, member})
	commitPayload, _ := wire.EncodeTopicJSON(commit)
	if response := callMessageServiceJSONForTest(t, hostServer, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionTopicCommit, Payload: commitPayload,
	}); response.Code != 0 {
		t.Fatalf("join commit response=%+v", response)
	}
	assertTopicMemberStatusForTest(t, hostManager.topics, "developers", member, "ACTIVE")
	requireTopicKeyPackageRecord(t, memberIdx, member, "developers", 2, owner)

	// The SDK's NEXT_MESSAGE_ID + TOPIC_PUBLISH sequence uses the same sender
	// authority as Direct messages.
	nextResponse := callMessageServiceJSONForTest(t, memberServer, authorizedMessageQuery(t, memberPriv, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionNextMessage, AccountID: member,
	}))
	if nextResponse.Code != 0 || nextResponse.NextSenderMsgID != 0 {
		t.Fatalf("next response=%+v", nextResponse)
	}
	publish := signedTestTopic(t, memberPriv, member, "developers", nextResponse.NextSenderMsgID, 2, "encrypted-topic")
	publishPayload, err := wire.SerializeTopicPublishMessage(publish, true)
	if err != nil {
		t.Fatal(err)
	}
	if response := callMessageServiceJSONForTest(t, memberServer, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionTopicPublish, TargetCoreNode: "host", Payload: publishPayload,
	}); response.Code != 0 {
		t.Fatalf("publish response=%+v", response)
	}
	ownerKey, err := dkvs.MailTopicMessageKey(owner, "developers", member, publish.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostIdx.Get(ownerKey); err != nil {
		t.Fatalf("owner topic mailbox missing: %v", err)
	}
	if memberManager.GetNextSenderMsgID(member) != 1 {
		t.Fatalf("topic publish did not consume sender sequence")
	}

	// A create request routed to a CoreNode different from the signed service
	// location is rejected instead of being silently redirected.
	if response := callMessageServiceJSONForTest(t, memberServer, &wire.MessageServiceRequest{
		Action: wire.MessageServiceActionCreateTopic, Payload: createPayload,
	}); response.Code == 0 {
		t.Fatal("topic create was accepted on the wrong service CoreNode")
	}
}
