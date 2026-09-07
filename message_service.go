package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func (s *server) messageServiceResponse(req *wire.MessageServiceRequest) *wire.MessageServiceResponse {
	response := &wire.MessageServiceResponse{}
	fail := func(err error) *wire.MessageServiceResponse {
		response.Code = -1
		if err != nil {
			response.Msg = err.Error()
			if errors.Is(err, ErrMessageRateLimited) {
				response.ErrorCode = "RATE_LIMITED"
				response.RetryAfterMS = messageServiceRetryAfterMS(err)
			}
		}
		return response
	}
	if s == nil || req == nil {
		return fail(ErrMessageInvalidEnvelope)
	}
	manager := messageManagerForServer(s)
	resolver := s.messageBindingResolver
	if manager == nil || resolver == nil {
		return fail(ErrMessageInvalidEnvelope)
	}
	switch strings.ToUpper(strings.TrimSpace(req.Action)) {
	case wire.MessageServiceActionBindAccount:
		if req.Record == nil {
			return fail(ErrMessageInvalidEnvelope)
		}
		acceptor, ok := resolver.(interface {
			AcceptLocalBinding(*wire.DKVSRecord) error
		})
		if !ok {
			return fail(ErrMessageInvalidEnvelope)
		}
		if err := acceptor.AcceptLocalBinding(req.Record); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionNextMessage:
		if err := manager.queryGuard.Authorize(req); err != nil {
			return fail(err)
		}
		if !resolver.AccountBoundToCore(req.AccountID, s.miningPubKey) {
			return fail(ErrMessageNotBoundHere)
		}
		response.NextSenderMsgID = manager.GetNextSenderMsgID(req.AccountID)
	case wire.MessageServiceActionSendDirect:
		if req.Direct == nil {
			return fail(ErrMessageInvalidEnvelope)
		}
		if err := manager.SendDirectMessage(req.Direct); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionCreateTopic:
		request, err := UnmarshalTopicCreateRequest(req.Payload)
		if err != nil {
			return fail(err)
		}
		if err := verifyTopicCreateRequestSignature(request); err != nil {
			return fail(err)
		}
		if request.Meta.ServiceCoreNode != s.miningPubKey ||
			!resolver.AccountBoundToCore(request.Meta.OwnerAccount, s.miningPubKey) {
			return fail(ErrMessageTopicPermission)
		}
		if err := manager.topics.CreateTopic(request.Meta); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionTopicState:
		if err := manager.queryGuard.Authorize(req); err != nil {
			return fail(err)
		}
		if req.TopicName == "" || !resolver.AccountBoundToCore(req.AccountID, s.miningPubKey) {
			return fail(ErrMessageNotBoundHere)
		}
		snapshot, err := manager.topics.TopicSnapshot(req.TopicName)
		if err != nil {
			return fail(err)
		}
		authorized := false
		for _, member := range snapshot.Members {
			if member.AccountID == req.AccountID && member.Status != "LEFT" && member.Status != "BANNED" {
				authorized = true
				break
			}
		}
		if !authorized {
			return fail(ErrMessageTopicPermission)
		}
		response.Payload, err = wire.EncodeTopicJSON(snapshot)
		if err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionTopicJoin, wire.MessageServiceActionTopicLeave:
		request, err := UnmarshalTopicMembershipRequest(req.Payload)
		if err != nil {
			return fail(err)
		}
		want := wire.TopicMembershipJoin
		if strings.EqualFold(req.Action, wire.MessageServiceActionTopicLeave) {
			want = wire.TopicMembershipLeave
		}
		if request.RequestType != want {
			return fail(ErrMessageInvalidEnvelope)
		}
		if err := manager.SubmitTopicMembershipRequest(request); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionTopicRejectJoin:
		rejection, err := UnmarshalTopicJoinRejection(req.Payload)
		if err != nil {
			return fail(err)
		}
		if err := manager.SubmitTopicJoinRejection(rejection); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionTopicCommit:
		commit, err := UnmarshalTopicMembershipCommit(req.Payload)
		if err != nil {
			return fail(err)
		}
		if err := manager.SubmitTopicMembershipCommit(commit); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionTopicPublish:
		publish, err := UnmarshalTopicPublish(req.Payload)
		if err != nil {
			return fail(err)
		}
		if req.TargetCoreNode == "" {
			return fail(ErrMessageInvalidEnvelope)
		}
		if err := manager.topics.SendTopicMessageToHost(publish, req.TargetCoreNode); err != nil {
			return fail(err)
		}
	case wire.MessageServiceActionDeleteMailbox:
		if req.Record == nil {
			return fail(ErrMessageInvalidEnvelope)
		}
		parsed, err := dkvs.ParseKey(req.Record.Key)
		if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) == 0 ||
			!resolver.AccountBoundToCore(parsed.Segments[0], s.miningPubKey) {
			return fail(ErrMessageNotBoundHere)
		}
		if _, err := s.assetIndexer.DeleteDKVSInternalMailbox(req.Record); err != nil {
			return fail(err)
		}
	default:
		return fail(fmt.Errorf("%w: unknown message service action", ErrMessageUnsupportedType))
	}
	return response
}

func messageServiceRetryAfterMS(err error) uint64 {
	if value := directRetryAfterMS(err); value != 0 {
		return value
	}
	var limited *MessageServiceQueryRateLimitError
	if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		return 0
	}
	if millis := limited.RetryAfter.Milliseconds(); millis > 0 {
		return uint64(millis)
	}
	return 1
}

func (s *server) handleMessageServiceJSON(request []byte) ([]byte, error) {
	var req wire.MessageServiceRequest
	if len(request) == 0 || len(request) > 2*1024*1024 {
		return nil, ErrMessageTooLarge
	}
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return json.Marshal(s.messageServiceResponse(&req))
}

func (s *server) configureProductionMessageService() error {
	if s == nil || s.assetIndexer == nil || s.db == nil || strings.TrimSpace(s.miningPubKey) == "" {
		return nil
	}
	bindings := newServerMessageBindingResolver(s)
	provider, contract, serviceName := s.assetIndexer.GetDKVSAutopayRuntime()
	authorizer := AutopaySubscriptionMessageAuthorizer{
		StateProvider:      provider,
		ContractForAccount: func(string) (string, error) { return contract, nil },
		ServiceName:        serviceName,
		AddressParams:      s.chainParams,
	}
	usage := NewPersistentMessageUsageCharger(s.db, authorizer)
	topicAuthorizer := AutopayTopicServiceAuthorizer{
		StateProvider:    provider,
		ContractForTopic: func(TopicMeta) (string, error) { return contract, nil },
		ServiceName:      serviceName,
		AddressParams:    s.chainParams,
	}
	s.messageBindingResolver = bindings
	if _, err := s.ConfigureMessageManager(bindings, usage, topicAuthorizer, authorizer); err != nil {
		return err
	}
	return nil
}
