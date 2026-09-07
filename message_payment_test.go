package main

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type testMessageSendAuthorizer struct {
	calls int
	err   error
}

func (a *testMessageSendAuthorizer) AuthorizeMessageSend(string, string) error {
	a.calls++
	return a.err
}

type testMessageAutopayProvider struct {
	state *dkvs.AutopayContractState
	err   error
}

func (p testMessageAutopayProvider) GetAutopayState(string) (*dkvs.AutopayContractState, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.state == nil {
		return nil, nil
	}
	copyState := *p.state
	copyState.Delegates = make(map[string]dkvs.AutopayDelegateState, len(p.state.Delegates))
	for key, value := range p.state.Delegates {
		copyState.Delegates[key] = value
	}
	return &copyState, nil
}

func TestDKVSMessagePersistentUsageChargerIsIdempotentByMessageID(t *testing.T) {
	db := testMessageDatabase(t)
	_, sender := testAccount(t)
	authorizer := &testMessageSendAuthorizer{}
	charger := NewPersistentMessageUsageCharger(db, authorizer)
	messageID0 := testStableMessageID("payment", sender, 0)
	messageID1 := testStableMessageID("payment", sender, 1)
	if err := charger.ChargeMessageSend(sender, messageID0); err != nil {
		t.Fatal(err)
	}
	if err := charger.ChargeMessageSend(sender, messageID0); err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("authorization calls=%d want=1", authorizer.calls)
	}
	restarted := NewPersistentMessageUsageCharger(db, authorizer)
	if err := restarted.ChargeMessageSend(sender, messageID0); err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("restart authorization calls=%d want=1", authorizer.calls)
	}
	if err := restarted.ChargeMessageSend(sender, messageID1); err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 2 {
		t.Fatalf("second message authorization calls=%d", authorizer.calls)
	}
}

func TestDKVSMessageAutopaySubscriptionAuthorization(t *testing.T) {
	priv, sender := testAccount(t)
	payer, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	state := &dkvs.AutopayContractState{
		TemplateName: "autopay.tc", CurrentBlock: 100, ServiceName: "message", Status: "active",
		Delegates: map[string]dkvs.AutopayDelegateState{
			payer: {Status: "active", LastPayHeight: 100, AmountPerBlock: "1", Balance: "10"},
		},
	}
	authorizer := AutopaySubscriptionMessageAuthorizer{
		StateProvider: testMessageAutopayProvider{state: state},
		ContractForAccount: func(accountID string) (string, error) {
			if accountID != sender {
				return "", ErrMessageBindingNotFound
			}
			return "message-autopay", nil
		},
		ServiceName: "message", AddressParams: &chaincfg.TestNetParams,
	}
	if err := authorizer.AuthorizeMessageSend(sender, testStableMessageID("autopay", sender, 0)); err != nil {
		t.Fatal(err)
	}
	state.Delegates[payer] = dkvs.AutopayDelegateState{Status: "active", LastPayHeight: 99, AmountPerBlock: "1", Balance: "10"}
	authorizer.StateProvider = testMessageAutopayProvider{state: state}
	if err := authorizer.AuthorizeMessageSend(sender, testStableMessageID("autopay", sender, 1)); !errors.Is(err, ErrMessageTopicPermission) {
		t.Fatalf("stale autopay err=%v", err)
	}
}

func TestDKVSMessageTopicServiceAutopayIndependentFromSenderUsage(t *testing.T) {
	priv, owner := testAccount(t)
	payer, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	state := &dkvs.AutopayContractState{
		TemplateName: "autopay.tc", CurrentBlock: 12, ServiceName: "topic", Status: "active",
		Delegates: map[string]dkvs.AutopayDelegateState{
			payer: {Status: "active", LastPayHeight: 12, AmountPerBlock: "1", Balance: "100"},
		},
	}
	authorizer := AutopayTopicServiceAuthorizer{
		StateProvider: testMessageAutopayProvider{state: state},
		ContractForTopic: func(topic TopicMeta) (string, error) {
			if topic.OwnerAccount != owner {
				return "", ErrMessageTopicPermission
			}
			return "topic-autopay", nil
		},
		ServiceName: "topic", AddressParams: &chaincfg.TestNetParams,
	}
	if err := authorizer.AuthorizeTopicService(TopicMeta{TopicName: "developers", OwnerAccount: owner}); err != nil {
		t.Fatal(err)
	}
	state.Status = "funding"
	authorizer.StateProvider = testMessageAutopayProvider{state: state}
	if err := authorizer.AuthorizeTopicService(TopicMeta{TopicName: "developers", OwnerAccount: owner}); !errors.Is(err, ErrMessageTopicPermission) {
		t.Fatalf("inactive topic service err=%v", err)
	}
}
