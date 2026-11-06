package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/websocket"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/base"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Use the real WebSocket serviceRequestSem (one slot), real DKVS paid record,
// and cold height cache. Only the getblock chain-lock wait is controlled by
// the fixture; the AUTOPAY lookup must read committed state without that queue.
func TestDKVSAutopayCallbackDoesNotWaitForRPCSlot(t *testing.T) {
	oldCfg, oldChain := cfg, indexercommon.CHAIN
	cfg = &config{RPCMaxConcurrentReqs: 1, RPCMaxWebsockets: 4}
	indexercommon.CHAIN = "testnet"
	t.Cleanup(func() { cfg = oldCfg; indexercommon.CHAIN = oldChain })
	params := chaincfg.TestNetParams
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer db.Close()
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	payer, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &params)
	require.NoError(t, err)
	defaults := dkvs.NetworkDefaultsForParams(&params)
	ap := template.NewAutopayContract("dkvs", defaults.AutopayRecipient, defaults.AutopayFeeAssetName, defaults.FullRecordFeePerBlock)
	addr, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{9}, 32))
	require.NoError(t, err)
	content, err := ap.Encode()
	require.NoError(t, err)
	runtime, err := template.NewRuntimeWithDeployer(addr, contractcommon.DeployPayload{Type: contractcommon.ContractTypeTemplate, SubType: ap.TemplateName(), Version: ap.Version(), ContractContent: content}, nil, payer)
	require.NoError(t, err)
	amount, err := indexercommon.NewDecimalFromString(defaults.FullRecordFeePerBlock, 8)
	require.NoError(t, err)
	// Build committed state through real invokes and settlement so the fixture
	// uses the runtime's current persistence codec.
	gas := framework.DefaultGasConfig()
	gas.GasAssetName = defaults.AutopayFeeAssetName
	fee2, err := gas.ContractFundingFee(framework.ExecutionKindTrigger, gas.TriggerBaseGas, true, 2)
	require.NoError(t, err)
	fee3, err := gas.ContractFundingFee(framework.ExecutionKindTrigger, gas.TriggerBaseGas, true, 3)
	require.NoError(t, err)
	gasFunding := fee2.AddAlignPrecision(fee3)
	param, err := (&template.AutopayConfigInvokeParam{AmountPerBlock: defaults.FullRecordFeePerBlock,
		GasFundingAmount: gasFunding.String(), BlobKeyLimit: 1}).Encode()
	require.NoError(t, err)
	funding := gasFunding.AddAlignPrecision(amount).AddAlignPrecision(amount)
	_, err = runtime.ApplyInvoke(template.ApplyInvokeRequest{
		Action: template.InvokeAPIConfig, Param: param, CallID: "paid-fixture", Invoker: payer,
		GasAssetName: gas.GasAssetName, Height: 1, Timestamp: 1,
		AssetPrecision: func(string) (int, bool) { return 8, true },
		FundingOutput: framework.ContractOutputFromFunding(contractcommon.FundingOutput{
			OutPoint: contractcommon.TxOutPoint{TxID: "paid-fixture"}, Contract: addr,
			Assets: wire.TxAssets{{Name: *wire.NewAssetNameFromString(gas.GasAssetName), Amount: *funding}},
		}),
	})
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(1, gas)
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(2, gas)
	require.NoError(t, err)
	runtime.SetCurrentBlock(2)
	store := template.NewRuntimeStore()
	store.Add(runtime)
	require.NoError(t, node.NewTemplateStateStore(db).StoreBlockState(&chainhash.Hash{2}, store))
	stateRaw := json.RawMessage(nil)
	view, err := runtime.StateView(framework.StateViewContext{ContractAddress: addr.String()})
	require.NoError(t, err)
	stateRaw, err = json.Marshal(view)
	require.NoError(t, err)

	var chainLock sync.RWMutex
	waiting, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var stateRPCs atomic.Int32
	oldGetBlock, oldGetCount, oldGetState := rpcHandlers["getblock"], rpcHandlers["getblockcount"], rpcHandlers["getcontractstate"]
	t.Cleanup(func() {
		rpcHandlers["getblock"] = oldGetBlock
		rpcHandlers["getblockcount"] = oldGetCount
		rpcHandlers["getcontractstate"] = oldGetState
	})
	block := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: params.GenesisBlock.BlockHash(), Nonce: 1}}
	var serialized bytes.Buffer
	require.NoError(t, block.Serialize(&serialized))
	rpcHandlers["getblockcount"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) { return int32(0), nil }
	rpcHandlers["getblock"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) {
		close(waiting)
		<-release
		chainLock.RLock()
		defer chainLock.RUnlock()
		return hex.EncodeToString(serialized.Bytes()), nil
	}
	rpcHandlers["getcontractstate"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) {
		stateRPCs.Add(1)
		return stateRaw, nil
	}
	rpc := &rpcServer{}
	rpc.ntfnMgr = newWsNotificationManager(rpc)
	rpc.ntfnMgr.Start()
	defer func() { rpc.ntfnMgr.Shutdown(); rpc.ntfnMgr.WaitForShutdown() }()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			rpc.WebsocketHandler(conn, r.RemoteAddr, true, true)
		}
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	_, err = satsnet_rpc.InitSatsNetClient(host, port, "test", "test", "", false)
	require.NoError(t, err)
	defer satsnet_rpc.ShutdownSatsNetClient()

	baseDB := indexerdb.NewKVDB(t.TempDir())
	defer baseDB.Close()
	idxDB := indexerdb.NewKVDB(t.TempDir())
	defer idxDB.Close()
	compiling := base.NewBaseIndexer(baseDB, &params, 0, 30)
	compiling.Init()
	compiling.SetBlockCallback(func(*common.Block) {})
	require.NoError(t, compiling.SyncBlock(params.GenesisBlock, 0, 2, false))
	require.NoError(t, compiling.SyncBlock(block, 1, 2, false))
	// Use exactly the node's default provider injection and the same height cache
	// and paid-retention callback used by IndexerMgr.processBlock.
	nodeConfig := nodeDKVSConfig(db, "http://127.0.0.1:18084")
	require.Equal(t, "http://127.0.0.1:18084", nodeConfig.ResolverL1NSBaseURL)
	provider := nodeConfig.AutopayStateProvider
	cached := &dkvs.HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return uint64(compiling.GetHeight()) }}
	verifier := dkvs.LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: dkvs.AutopayFeeVerifier{StateProvider: cached, Contract: addr.String(), ServiceName: "dkvs", Recipient: defaults.AutopayRecipient, FeeAssetName: defaults.AutopayFeeAssetName, FullRecordFeePerBlock: defaults.FullRecordFeePerBlock, AddressParams: &params}}
	idx := dkvs.New(idxDB, dkvs.Config{CurrentHeight: func() uint64 { return uint64(compiling.GetHeight()) }, FeeVerifier: verifier})
	key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "lock-test")
	require.NoError(t, err)
	record, err := dkvs.NewAccountRecord(key, []byte("paid"), dkvs.RecordOptions{Seq: 1})
	require.NoError(t, err)
	proof, err := dkvs.NewAutopayFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), 0, addr.String(), "")
	require.NoError(t, err)
	record.FeeProof, err = dkvs.EncodeFeeProof(proof)
	require.NoError(t, err)
	hash := dkvs.SigningHash(record)
	sig, err := schnorr.Sign(priv, hash[:])
	require.NoError(t, err)
	record.Signature = sig.Serialize()
	_, err = idx.PutLocal(record)
	require.NoError(t, err)
	before := stateRPCs.Load()
	compiling.SetBlockCallback(func(b *common.Block) { idx.RefreshPaidRetentionAt(uint64(b.Height)) })
	block2 := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: block.BlockHash(), Nonce: 2}}
	chainLock.Lock()
	fetched := make(chan error, 1)
	go func() { hash := block.BlockHash(); _, err := satsnet_rpc.GetRawBlock(&hash); fetched <- err }()
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		chainLock.Unlock()
		unblock()
		t.Fatal("getblock did not acquire RPC slot")
	}
	connected := make(chan error, 1)
	go func() { connected <- compiling.SyncBlock(block2, 2, 2, false) }()
	blocked := false
	var connectErr error
	select {
	case connectErr = <-connected:
	case <-time.After(time.Second):
		blocked = true
		t.Error("real AUTOPAY callback waits for the sole RPC slot while holding the chain lock")
	}
	chainLock.Unlock()
	unblock()
	require.NoError(t, <-fetched)
	if blocked {
		connectErr = <-connected
	}
	require.NoError(t, connectErr)
	require.Equal(t, before, stateRPCs.Load(), "cold callback must use committed local state")
	_, err = idx.Get(record.Key)
	require.NoError(t, err, "valid AUTOPAY record must remain active")
	require.Zero(t, stateRPCs.Load(), "node default provider must never use the local RPC queue")
	state, err := provider.GetAutopayState(" " + addr.String() + " ")
	require.NoError(t, err)
	require.Equal(t, addr.String(), state.Contract)
	require.Equal(t, int64(2), state.CurrentBlock)
	require.Equal(t, int64(2), state.Delegates[payer].LastPayHeight)
	_, err = provider.GetAutopayState("invalid-address")
	require.Error(t, err)
	missing, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{10}, 32))
	require.NoError(t, err)
	state, err = provider.GetAutopayState(missing.String())
	require.NoError(t, err)
	require.Nil(t, state, "missing committed runtime means no subscription")
	_, err = (localAutopayStateProvider{}).GetAutopayState(addr.String())
	require.Error(t, err, "unavailable committed state must fail without RPC fallback")
	require.Zero(t, stateRPCs.Load())
}

// Exercise the node's committed provider through the real retention selector,
// receiving manager and DKVS mailbox, rather than a fixed subscription fixture.
func TestDKVSAutopayMissingRuntimeDirectMailbox(t *testing.T) {
	for _, initial := range []string{"no bucket", "no tip", "committed empty store"} {
		t.Run(initial, func(t *testing.T) {
			db := testMessageDatabase(t)
			if initial == "no tip" {
				require.NoError(t, db.Update(func(tx database.Tx) error {
					_, err := tx.Metadata().CreateBucketIfNotExists([]byte("templatestate"))
					return err
				}))
			} else if initial == "committed empty store" {
				require.NoError(t, node.NewTemplateStateStore(db).StoreBlockState(&chainhash.Hash{1}, template.NewRuntimeStore()))
			}
			addr, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1,
				contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{11}, 32))
			require.NoError(t, err)
			height := uint64(100)
			provider := nodeDKVSConfig(db, "").AutopayStateProvider
			cached := &dkvs.HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return height }}
			state, err := cached.GetAutopayState(addr.String())
			require.NoError(t, err)
			require.Nil(t, state)
			authorizer := AutopaySubscriptionMessageAuthorizer{
				StateProvider: cached, ContractForAccount: func(string) (string, error) { return addr.String(), nil },
				ServiceName: "dkvs", AddressParams: &chaincfg.TestNetParams,
			}
			senderPriv, sender := testAccount(t)
			_, recipient := testAccount(t)
			paid, err := authorizer.HasActiveSubscription(recipient)
			require.NoError(t, err)
			require.False(t, paid)
			mailDB := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, mailDB)
			t.Cleanup(func() { _ = mailDB.Close() })
			idx := dkvs.New(mailDB, dkvs.Config{CurrentHeight: func() uint64 { return height }})
			mailbox := NewDKVSMessageMailboxStoreWithRetention(e2eMailboxWriter{idx: idx},
				func() uint64 { return height }, func(string) uint64 { return 25 }, nil, authorizer)
			message := signedTestDirect(t, senderPriv, sender, recipient, 0, "test-direct")
			key, err := dkvs.MailMsgKey(recipient, sender, message.MessageID)
			require.NoError(t, err)
			acks := 0
			router := &testMessageRouter{}
			router.hook = func(notify *wire.MsgDKVSNotify) error {
				envelope, err := UnmarshalMessageEnvelope(notify.Data)
				require.NoError(t, err)
				ack, err := UnmarshalMessageAck(envelope.Payload)
				require.NoError(t, err)
				require.Equal(t, MessageAckOK, ack.Status)
				_, err = idx.Get(key)
				require.NoError(t, err, "successful ACK must follow mailbox commit")
				acks++
				return nil
			}
			bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
			receiver := newTestMessageManager("core-b", bindings, mailbox, router, nil, nil)
			payload, err := MarshalDirectMessage(message, true)
			require.NoError(t, err)
			data, err := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeDirect, SourceCoreNode: "core-a", Payload: payload})
			require.NoError(t, err)
			notify := &wire.MsgDKVSNotify{Target: "core-b", EventType: wire.DKVSNotifyEventMessage, Data: data}
			require.NoError(t, receiver.HandleMessageNotify(notify))
			stored, err := idx.Get(key)
			require.NoError(t, err)
			require.Equal(t, uint64(25), stored.TTL)
			require.Equal(t, uint64(100), stored.IssueHeight)
			proof, err := dkvs.ParseFeeProof(stored.FeeProof)
			require.NoError(t, err)
			require.Equal(t, dkvs.FeeModeFreeLocal, proof.Mode)
			height++
			require.NoError(t, receiver.HandleMessageNotify(notify))
			require.Equal(t, 2, acks)
			updated, err := mailbox.AppendDirectMessage(message)
			require.NoError(t, err)
			require.False(t, updated, "mailbox-level retry must also be idempotent")
			retried, err := idx.Get(key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(stored), dkvs.RecordHash(retried), "retry must not renew retention")
			records, total, err := idx.ListPrefix("/mail/"+recipient, 0, 100)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, records, 1)

			// A well-formed but absent configured contract grants no paid rights.
			require.ErrorIs(t, authorizer.AuthorizeMessageSend(sender, message.MessageID), ErrMessageTopicPermission)
			topic := AutopayTopicServiceAuthorizer{StateProvider: cached,
				ContractForTopic: func(TopicMeta) (string, error) { return addr.String(), nil },
				ServiceName:      "dkvs", AddressParams: &chaincfg.TestNetParams}
			require.ErrorIs(t, topic.AuthorizeTopicService(TopicMeta{TopicName: "test", OwnerAccount: sender}), ErrMessageTopicPermission)
			verifier := dkvs.AutopayFeeVerifier{StateProvider: cached, Contract: addr.String(), AddressParams: &chaincfg.TestNetParams}
			paidKey, err := dkvs.PersonalKey(senderPriv.PubKey().SerializeCompressed(), "missing-autopay")
			require.NoError(t, err)
			record, err := dkvs.NewAccountRecord(paidKey, []byte("test"), dkvs.RecordOptions{Seq: 1})
			require.NoError(t, err)
			paidProof, err := dkvs.NewAutopayFeeProof(paidKey, "personal", uint32(dkvs.RecordSize(record)), 0, addr.String(), "")
			require.NoError(t, err)
			record.FeeProof, err = dkvs.EncodeFeeProof(paidProof)
			require.NoError(t, err)
			hash := dkvs.SigningHash(record)
			signature, err := schnorr.Sign(senderPriv, hash[:])
			require.NoError(t, err)
			record.Signature = signature.Serialize()
			paidDB := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, paidDB)
			t.Cleanup(func() { _ = paidDB.Close() })
			paidIdx := dkvs.New(paidDB, dkvs.Config{CurrentHeight: func() uint64 { return height }, FeeVerifier: verifier})
			_, err = paidIdx.PutLocal(record)
			require.ErrorIs(t, err, dkvs.ErrInvalidFeeProof)
			_, err = paidIdx.Get(paidKey)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
			parsed, err := dkvs.ParseKey(paidKey)
			require.NoError(t, err)
			_, err = verifier.PaidRecordRetention(record, parsed)
			require.ErrorIs(t, err, dkvs.ErrInvalidFeeProof)

			// Keep the existing height-scoped negative cache: commit visibility
			// changes on the next indexed height, not during the current height.
			defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
			ap := template.NewAutopayContract("dkvs", defaults.AutopayRecipient, defaults.AutopayFeeAssetName, defaults.FullRecordFeePerBlock)
			content, err := ap.Encode()
			require.NoError(t, err)
			runtime, err := template.NewRuntimeWithDeployer(addr, contractcommon.DeployPayload{
				Type: contractcommon.ContractTypeTemplate, SubType: ap.TemplateName(), Version: ap.Version(), ContractContent: content}, nil, "deployer")
			require.NoError(t, err)
			store := template.NewRuntimeStore()
			store.Add(runtime)
			require.NoError(t, node.NewTemplateStateStore(db).StoreBlockState(&chainhash.Hash{2}, store))
			state, err = provider.GetAutopayState(addr.String())
			require.NoError(t, err)
			require.NotNil(t, state)
			state, err = cached.GetAutopayState(addr.String())
			require.NoError(t, err)
			require.Nil(t, state)
			height++
			state, err = cached.GetAutopayState(addr.String())
			require.NoError(t, err)
			require.NotNil(t, state)
		})
	}
}

func TestDKVSAutopayProviderErrorsDoNotWriteMailbox(t *testing.T) {
	for _, failure := range []string{"invalid address", "missing database", "closed database", "corrupt tip", "missing snapshot", "corrupt snapshot", "wrong template"} {
		t.Run(failure, func(t *testing.T) {
			db := testMessageDatabase(t)
			addr, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1,
				contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{12}, 32))
			require.NoError(t, err)
			address := addr.String()
			switch failure {
			case "invalid address":
				address = "invalid-address"
			case "missing database":
				db = nil
			case "closed database":
				require.NoError(t, db.Close())
			case "wrong template":
				amm := template.NewAMMContract("ordx:f:test", "100", 20, "2000")
				content, err := amm.Encode()
				require.NoError(t, err)
				runtime, err := template.NewRuntimeWithDeployer(addr, contractcommon.DeployPayload{
					Type: contractcommon.ContractTypeTemplate, SubType: amm.TemplateName(), Version: amm.Version(),
					Flags: contractcommon.ContractFlagNonClosable, ContractContent: content}, nil, "deployer")
				require.NoError(t, err)
				store := template.NewRuntimeStore()
				store.Add(runtime)
				require.NoError(t, node.NewTemplateStateStore(db).StoreBlockState(&chainhash.Hash{1}, store))
			default:
				require.NoError(t, db.Update(func(tx database.Tx) error {
					parent, err := tx.Metadata().CreateBucketIfNotExists([]byte("templatestate"))
					if err != nil {
						return err
					}
					if failure == "corrupt tip" {
						return parent.Put([]byte("tip"), []byte{1})
					}
					hash := chainhash.Hash{1}
					if err := parent.Put([]byte("tip"), hash[:]); err != nil {
						return err
					}
					if failure == "corrupt snapshot" {
						byBlock, err := parent.CreateBucketIfNotExists([]byte("byblock"))
						if err != nil {
							return err
						}
						return byBlock.Put(hash[:], []byte("invalid-runtime-codec"))
					}
					return nil
				}))
			}
			provider := nodeDKVSConfig(db, "").AutopayStateProvider
			state, err := provider.GetAutopayState(address)
			require.Error(t, err)
			require.Nil(t, state)
			if failure == "wrong template" {
				require.ErrorIs(t, err, dkvs.ErrInvalidFeeProof)
			}
			authorizer := AutopaySubscriptionMessageAuthorizer{StateProvider: provider,
				ContractForAccount: func(string) (string, error) { return address, nil }, AddressParams: &chaincfg.TestNetParams}
			idx := newMessageE2EIndexer(t)
			mailbox := NewDKVSMessageMailboxStoreWithRetention(e2eMailboxWriter{idx: idx},
				func() uint64 { return 100 }, func(string) uint64 { return 25 }, nil, authorizer)
			priv, sender := testAccount(t)
			_, recipient := testAccount(t)
			message := signedTestDirect(t, priv, sender, recipient, 0, "test-direct")
			updated, err := mailbox.AppendDirectMessage(message)
			require.Error(t, err)
			require.False(t, updated)
			key, err := dkvs.MailMsgKey(recipient, sender, message.MessageID)
			require.NoError(t, err)
			_, err = idx.Get(key)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		})
	}
}
