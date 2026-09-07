package main

import (
	"errors"
	"sync"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	indexerShare "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/stp"
	"github.com/sat20-labs/satoshinet/wire"
)

var errMessageTargetUnavailable = errors.New("message target core node unavailable")

type messageNotifyPeer interface {
	Connected() bool
	QueueMessage(msg wire.Message, doneChan chan<- struct{})
}

type coreMessageRouteTransport interface {
	LocalCoreID() string
	BootstrapCoreID() string
	DirectPeer(coreNodeID string) messageNotifyPeer
	DeliverLocal(message *wire.MsgDKVSNotify) error
}

// routeDirectedMessageNotify implements the only three allowed paths:
// local, direct target peer, or bootstrap fallback for locally-originated
// messages. An inbound message may only be consumed locally or relayed by the
// bootstrap; ordinary CoreNodes never become a second relay hop.
func routeDirectedMessageNotify(transport coreMessageRouteTransport, message *wire.MsgDKVSNotify, inbound bool) error {
	if transport == nil || message == nil || message.Target == "" ||
		message.EventType != wire.DKVSNotifyEventMessage || len(message.Data) == 0 {
		return ErrMessageInvalidEnvelope
	}
	local := transport.LocalCoreID()
	if local == "" {
		return ErrMessageBindingNotFound
	}
	if message.Target == local {
		return transport.DeliverLocal(message)
	}
	bootstrap := transport.BootstrapCoreID()
	isBootstrap := bootstrap != "" && local == bootstrap
	if inbound && !isBootstrap {
		return errMessageTargetUnavailable
	}
	if target := transport.DirectPeer(message.Target); target != nil && target.Connected() {
		target.QueueMessage(message, nil)
		return nil
	}
	if inbound || isBootstrap {
		// Bootstrap has no store-and-forward queue and never broadcasts a
		// missing Target. The sender/Topic Host keeps its durable pending job.
		return errMessageTargetUnavailable
	}
	if bootstrap == "" || bootstrap == message.Target {
		return errMessageTargetUnavailable
	}
	relay := transport.DirectPeer(bootstrap)
	if relay == nil || !relay.Connected() {
		return errMessageTargetUnavailable
	}
	relay.QueueMessage(message, nil)
	return nil
}

type serverMessageRouteTransport struct {
	server *server
}

func (t serverMessageRouteTransport) LocalCoreID() string {
	if t.server == nil {
		return ""
	}
	return t.server.miningPubKey
}

func (t serverMessageRouteTransport) BootstrapCoreID() string {
	return indexercommon.GetBootstrapPubKey()
}

func (t serverMessageRouteTransport) DirectPeer(coreNodeID string) messageNotifyPeer {
	if t.server == nil || coreNodeID == "" {
		return nil
	}
	return t.server.GetPeerByValidatorId(coreNodeID)
}

func (t serverMessageRouteTransport) DeliverLocal(message *wire.MsgDKVSNotify) error {
	if t.server == nil {
		return ErrMessageInvalidEnvelope
	}
	manager := messageManagerForServer(t.server)
	if manager == nil {
		return ErrMessageBindingNotFound
	}
	return manager.HandleMessageNotify(message)
}

type serverMessageRouter struct {
	server *server
}

func (r serverMessageRouter) RouteMessageNotify(message *wire.MsgDKVSNotify) error {
	return routeDirectedMessageNotify(serverMessageRouteTransport{server: r.server}, message, false)
}

var serverMessageManagers sync.Map

func messageManagerForServer(s *server) *MessageManager {
	if s == nil {
		return nil
	}
	value, ok := serverMessageManagers.Load(s)
	if !ok {
		return nil
	}
	return value.(*MessageManager)
}

// ConfigureMessageManager binds the message application to the existing server
// once the account-service binding resolver and payment authorizers are
// available. It deliberately does not invent another account->CoreNode
// registry: callers provide the existing binding mechanism through bindings.
func (s *server) ConfigureMessageManager(bindings MessageBindingResolver, usage MessageUsageCharger,
	topicService TopicServiceAuthorizer, retention MessageRetentionResolver) (*MessageManager, error) {
	if s == nil || s.assetIndexer == nil || s.db == nil || s.miningPubKey == "" || bindings == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	if existing := messageManagerForServer(s); existing != nil {
		return existing, nil
	}
	policy := s.assetIndexer.GetDKVSMailboxPolicy()
	freePolicy := s.assetIndexer.GetDKVSFreeLocalCachePolicy()
	mailbox := NewDKVSMessageMailboxStoreWithRetention(
		s.assetIndexer,
		func() uint64 {
			if s.chain == nil {
				return 0
			}
			best := s.chain.BestSnapshot()
			if best == nil || best.Height < 0 {
				return 0
			}
			return uint64(best.Height)
		},
		func(string) uint64 { return freePolicy.MaxTTL },
		func(string) uint64 { return policy.MaxShareTTL },
		retention,
	)
	manager := NewPersistentMessageManager(
		s.miningPubKey, bindings, mailbox, serverMessageRouter{server: s},
		usage, topicService, s.db,
	)
	manager.SetCoreSigner(stp.SignMsg)
	manager.coreAuthority = func(source string) bool {
		if indexerShare.ShareIndexer == nil {
			return false
		}
		seqMgr := indexerShare.ShareIndexer.GetSeqMgr()
		if seqMgr == nil {
			return false
		}
		typ := seqMgr.GetNodeType(source)
		return typ == indexercommon.NODE_TYPE_CORE || typ == indexercommon.NODE_TYPE_BOOTSTRAP
	}
	if err := manager.topics.SetCatalogStore(NewDKVSTopicCatalogStore(s.assetIndexer)); err != nil {
		return nil, err
	}
	serverMessageManagers.Store(s, manager)
	dkvsp2p.RegisterDirectedNotifyHandler(s.assetIndexer, func(message *wire.MsgDKVSNotify) error {
		return routeDirectedMessageNotify(serverMessageRouteTransport{server: s}, message, true)
	})

	// Durable sender/Topic jobs own their retry responsibility. The loop always
	// re-resolves AccountBound placement before retrying and stops with server
	// shutdown; Bootstrap itself remains stateless.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		manager.RunRetryLoop(s.quit, defaultMessageRetryInterval)
	}()
	return manager, nil
}

func (s *server) RetryPendingMessages() error {
	manager := messageManagerForServer(s)
	if manager == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := manager.RetryPendingResolved(); err != nil {
		return err
	}
	return manager.topics.RetryPendingResolved()
}

func (s *server) UnconfigureMessageManager() {
	if s == nil {
		return
	}
	serverMessageManagers.Delete(s)
	if s.assetIndexer != nil {
		dkvsp2p.RegisterDirectedNotifyHandler(s.assetIndexer, nil)
	}
}

// Ensure *peer.Peer remains a valid direct-target transport as its networking
// implementation evolves.
var _ messageNotifyPeer = (*peer.Peer)(nil)
