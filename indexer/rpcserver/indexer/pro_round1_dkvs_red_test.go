package indexer

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Only transport adapters are supplied here. All storage, authorization,
// expiry, CAS, and Active decisions use the actual public DKVS implementation.
type proRedHTTPBackend struct {
	*activeHTTPBackend
	admission *dkvs.WalletRPCAdmission
}

func (b *proRedHTTPBackend) GetDKVSRecord(key string) (*wire.DKVSRecord, error) {
	return b.store.Get(key)
}
func (b *proRedHTTPBackend) GetDKVSKeyState(key string) (dkvs.DKVSKeyState, error) {
	return b.store.GetKeyState(key)
}
func (b *proRedHTTPBackend) PutDKVSRecordBatchCASAuthorized(m []dkvs.CASMutation, o dkvs.BatchCASOptions, a *dkvs.WalletWriteAuthorization) (*dkvs.WriteResult, error) {
	return b.admission.PutRecords(m, o, a)
}

func proRedRouter(store *dkvs.Indexer, admission *dkvs.WalletRPCAdmission) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewService(&proRedHTTPBackend{activeHTTPBackend: &activeHTTPBackend{store: store}, admission: admission}).InitRouter(router, "")
	return router
}

func proRedRequest(t *testing.T, router http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func proRedSignRecord(t *testing.T, key *btcec.PrivateKey, record *wire.DKVSRecord) {
	t.Helper()
	hash := dkvs.SigningHash(record)
	sig, err := schnorr.Sign(key, hash[:])
	require.NoError(t, err)
	record.Signature = sig.Serialize()
}

func proRedFreeRecord(t *testing.T, key *btcec.PrivateKey, name string, seq, issue, ttl uint64) *wire.DKVSRecord {
	t.Helper()
	path, err := dkvs.PersonalKey(key.PubKey().SerializeCompressed(), name)
	require.NoError(t, err)
	record, err := dkvs.NewAccountRecord(path, []byte("test-value"), dkvs.RecordOptions{Seq: seq, IssueHeight: issue, TTL: ttl})
	require.NoError(t, err)
	proof, err := dkvs.NewFreeLocalFeeProof(path, "personal", uint32(dkvs.RecordSize(record)), dkvs.RecordExpiryHeight(record))
	require.NoError(t, err)
	record.FeeProof, err = dkvs.EncodeFeeProof(proof)
	require.NoError(t, err)
	proRedSignRecord(t, key, record)
	return record
}

func proRedCASHTTP(t *testing.T, router http.Handler, store *dkvs.Indexer, key *btcec.PrivateKey, record *wire.DKVSRecord, condition dkvs.WritePrecondition) *httptest.ResponseRecorder {
	t.Helper()
	mutations := []dkvs.CASMutation{{Record: record, Precondition: condition}}
	options := dkvs.BatchCASOptions{EndpointID: store.EndpointID(), RequestID: dkvs.RecordHash(record).String()}
	prefixes, err := dkvs.WriteMutationPrefixes(mutations)
	require.NoError(t, err)
	ctx := dkvs.WalletWriteContext{EndpointID: store.EndpointID()}
	for _, prefix := range prefixes {
		meta, err := store.ActiveMetadata(context.Background(), dkvs.ActiveScope{Prefix: prefix})
		require.NoError(t, err)
		ctx.Prefixes = append(ctx.Prefixes, dkvs.PrefixGeneration{Prefix: prefix, Generation: meta.Generation})
	}
	digest, err := dkvs.WalletWriteDigest(mutations, options, ctx)
	require.NoError(t, err)
	sig, err := schnorr.Sign(key, digest[:])
	require.NoError(t, err)
	request := dkvsBatchCASReq{EndpointID: store.EndpointID(), RequestID: options.RequestID, Authorization: &dkvs.WalletWriteAuthorization{Context: ctx, Signature: sig.Serialize()}, Mutations: []dkvsCASMutationReq{{Record: record, ExpectAbsent: condition.ExpectAbsent}}}
	if condition.ExpectedHash != nil {
		request.Mutations[0].ExpectedETag = condition.ExpectedHash.String()
	}
	return proRedRequest(t, router, http.MethodPost, "/v3/dkvs/records/batch-cas", request)
}

func TestProRound1R4CurrentReadTTLView(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ttl           uint64
		tipConfigured bool
	}{{"expired_at_101", 100, true}, {"valid_until_103", 102, true}, {"indexed_fallback", 100, false}} {
		t.Run(tc.name, func(t *testing.T) {
			database := dbpkg.NewKVDB(t.TempDir())
			require.NotNil(t, database)
			t.Cleanup(func() { _ = database.Close() })
			key, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			core, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			indexed, tip := uint64(1), uint64(1)
			cfg := dkvs.Config{EndpointID: hex.EncodeToString(core.PubKey().SerializeCompressed()), AllowFreeLocal: true, FreeLocalCache: dkvs.FreeLocalCachePolicy{Enabled: true, MaxTTL: 1000, MaxRecordsPerSigner: 16, MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100, MaxTotalBytes: 1 << 20}, FeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, CurrentHeight: func() uint64 { return indexed }}
			if tc.tipConfigured {
				cfg.TipHeight = func() uint64 { return tip }
			}
			store := dkvs.New(database, cfg)
			old := proRedFreeRecord(t, key, "pro-review/item", 1, 1, tc.ttl)
			_, err = store.PutLocal(old)
			require.NoError(t, err)
			// Establish the real binding required by WalletRPCAdmission.
			indexed, tip = 100, 100
			payer, err := dkvs.P2TRAddressFromPubKeyBytes(key.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
			require.NoError(t, err)
			bindingKey, err := dkvs.AccountMappingKey("testnet4", payer)
			require.NoError(t, err)
			value, err := dkvs.EncodeAccountServiceDescriptor(dkvs.AccountServiceDescriptor{AccountID: dkvs.AccountID(key.PubKey().SerializeCompressed()), CoreNodeID: cfg.EndpointID, Capabilities: 1})
			require.NoError(t, err)
			binding, err := dkvs.NewAccountRecord(bindingKey, value, dkvs.RecordOptions{Seq: 1, IssueHeight: 100})
			require.NoError(t, err)
			proRedSignRecord(t, key, binding)
			_, err = store.PutLocal(binding)
			require.NoError(t, err)
			admission := &dkvs.WalletRPCAdmission{Indexer: store, IsCoreNode: func() bool { return true }, CurrentBinding: func(string) (*wire.DKVSRecord, error) { return store.Get(binding.Key) }}
			router := proRedRouter(store, admission)
			tip = 102
			prefix, err := dkvs.CollectionPathForKey(old.Key)
			require.NoError(t, err)
			recordResp := proRedRequest(t, router, http.MethodGet, "/v3/dkvs/record?key="+url.QueryEscape(old.Key), nil)
			stateResp := proRedRequest(t, router, http.MethodGet, "/v3/dkvs/key-state?key="+url.QueryEscape(old.Key), nil)
			prefixResp := proRedRequest(t, router, http.MethodPost, "/v3/dkvs/prefixes/read", map[string]string{"prefix": prefix})
			var point dkvsApplicationRecordResp
			require.NoError(t, json.Unmarshal(recordResp.Body.Bytes(), &point))
			var state dkvsKeyStateResp
			require.NoError(t, json.Unmarshal(stateResp.Body.Bytes(), &state))
			require.NotNil(t, state.Data)
			var read dkvsPrefixReadResp
			require.NoError(t, json.Unmarshal(prefixResp.Body.Bytes(), &read))
			require.NotNil(t, read.Data)
			hash := dkvs.RecordHash(old)
			byHash, byHashErr := store.GetByHash(hash)
			listed, total, listErr := store.ListPrefix(prefix, 0, 100)
			newIssue := tip
			if !tc.tipConfigured {
				newIssue = indexed
			}
			update := proRedFreeRecord(t, key, "pro-review/item", 2, newIssue, 100)
			casResp := proRedCASHTTP(t, router, store, key, update, dkvs.WritePrecondition{ExpectedHash: &hash})
			t.Logf("indexed=%d tip=%d configured=%v expiry=%d GET=%d key-state=%s seq=%d ETag=%s Prefix=%d records=%d view=%d GetByHash=%v/%v List=%d/%d/%v CAS=%d %s", indexed, tip, tc.tipConfigured, dkvs.RecordExpiryHeight(old), recordResp.Code, state.Data.Status, state.Data.Seq, state.Data.ETag, prefixResp.Code, len(read.Data.Records), read.Data.ViewHeight, byHash != nil, byHashErr, len(listed), total, listErr, casResp.Code, casResp.Body.String())
			if tc.name == "expired_at_101" {
				recreate := proRedFreeRecord(t, key, "pro-review/item", 1, tip, 100)
				recreateResp := proRedCASHTTP(t, router, store, key, recreate, dkvs.WritePrecondition{ExpectAbsent: true})
				t.Logf("ExpectAbsent Seq=1 HTTP=%d %s", recreateResp.Code, recreateResp.Body.String())
				require.Equal(t, http.StatusConflict, casResp.Code, "old ETag rejected by tip-view CAS")
				require.Equal(t, http.StatusOK, recreateResp.Code, "tip-view absence recreates key")
				require.Empty(t, read.Data.Records)
				// Keep all observable contradictions in one failing leaf.
				if recordResp.Code != http.StatusNotFound || point.Data != nil || state.Data.Status != dkvs.KeyStateNeverSeen || byHashErr == nil || len(listed) != 0 {
					t.Errorf("expired current read must agree with Prefix/CAS: HTTP_GET=%d key-state=%s byHash_found=%v listed=%d", recordResp.Code, state.Data.Status, byHash != nil, len(listed))
				}
			} else {
				require.Equal(t, http.StatusOK, recordResp.Code)
				require.NotNil(t, point.Data)
				require.Equal(t, dkvs.KeyStateActive, state.Data.Status)
				require.Len(t, read.Data.Records, 1)
				require.NoError(t, byHashErr)
				require.Len(t, listed, 1)
				require.Equal(t, http.StatusOK, casResp.Code)
			}
		})
	}
}

type proRedPaidProvider struct {
	state *dkvs.AutopayContractState
	err   error
	calls int
}

func (p *proRedPaidProvider) GetAutopayState(string) (*dkvs.AutopayContractState, error) {
	p.calls++
	return p.state, p.err
}

func TestProRound1R5ColdActiveNetworkHTTP(t *testing.T) {
	for _, mode := range []string{"paid_current", "provider_unavailable", "payment_behind_tip"} {
		t.Run(mode, func(t *testing.T) {
			database := dbpkg.NewKVDB(t.TempDir())
			require.NotNil(t, database)
			t.Cleanup(func() { _ = database.Close() })
			key, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			payer, err := dkvs.P2TRAddressFromPubKeyBytes(key.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
			require.NoError(t, err)
			height, tip := uint64(10), uint64(10)
			provider := &proRedPaidProvider{state: &dkvs.AutopayContractState{Contract: "autopay", TemplateName: "autopay.tc", CurrentBlock: 10, ServiceName: "dkvs", Recipient: "recipient", FeeAssetName: "sgas", Status: "active", Delegates: map[string]dkvs.AutopayDelegateState{payer: {AmountPerBlock: "1", Balance: "100", LastPayHeight: 10, Status: "active"}}}}
			verifier := dkvs.AutopayFeeVerifier{StateProvider: provider, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient", FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams}
			cfg := dkvs.Config{EndpointID: "before-restart", FeeVerifier: verifier, CurrentHeight: func() uint64 { return height }, TipHeight: func() uint64 { return tip }}
			original := dkvs.New(database, cfg)
			path, err := dkvs.PersonalKey(key.PubKey().SerializeCompressed(), "paid-retention")
			require.NoError(t, err)
			record, err := dkvs.NewAccountRecord(path, []byte("value"), dkvs.RecordOptions{Seq: 1})
			require.NoError(t, err)
			proof, err := dkvs.NewAutopayFeeProof(path, "personal", uint32(dkvs.RecordSize(record)), 0, "autopay", "")
			require.NoError(t, err)
			record.FeeProof, err = dkvs.EncodeFeeProof(proof)
			require.NoError(t, err)
			proRedSignRecord(t, key, record)
			_, err = original.PutLocal(record)
			require.NoError(t, err)
			prefix, err := dkvs.CollectionPathForKey(record.Key)
			require.NoError(t, err)
			warm, err := original.ActiveSyncPage(context.Background(), dkvs.ActiveSyncRequest{Scope: dkvs.ActiveScope{Prefix: prefix, Network: true}, EndpointID: original.EndpointID(), Full: true})
			require.NoError(t, err)
			require.Len(t, warm.Records, 1, "warm control")
			if mode == "provider_unavailable" {
				provider.err = errors.New("review: contract state unavailable")
			}
			if mode == "payment_behind_tip" {
				tip = 12
			}
			// A new indexer over the same persisted database, without clearing
			// caches or editing stored records, is the normal restart fixture.
			cfg.EndpointID = "after-restart"
			source := dkvs.New(database, cfg)
			stored, err := source.Get(record.Key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(stored))
			router := proRedRouter(source, nil)
			before := provider.calls
			response := proRedRequest(t, router, http.MethodPost, "/v3/dkvs/active/sync", dkvs.ActiveSyncRequest{Scope: dkvs.ActiveScope{Prefix: prefix, Network: true}, EndpointID: source.EndpointID(), Full: true})
			var decoded struct {
				Data *dkvs.ActivePage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
			t.Logf("normal_restart same_DB=true mode=%s indexed=%d tip=%d HTTP=%d provider_queries=%d body=%s", mode, height, tip, response.Code, provider.calls-before, response.Body.String())
			// Protected snapshot is an independent public control. Run it only
			// after recording the cold HTTP observation, so it cannot warm it.
			snapshot, snapshotErr := source.GetPathSnapshot(prefix)
			size := 0
			if snapshot != nil {
				size = len(snapshot.Records)
			}
			t.Logf("protected GetPathSnapshot records=%d error=%v", size, snapshotErr)
			if mode == "paid_current" {
				require.NoError(t, snapshotErr)
				require.Equal(t, 1, size)
			} else {
				require.Error(t, snapshotErr)
			}
			if response.Code == http.StatusOK && decoded.Data != nil && decoded.Data.Complete && len(decoded.Data.Records) == 0 {
				t.Fatalf("unknown payment became an authoritative complete empty Network Full page: mode=%s root=%s", mode, decoded.Data.Meta.Root)
			}
			if mode == "paid_current" {
				require.Equal(t, http.StatusOK, response.Code)
				require.NotNil(t, decoded.Data)
				require.Len(t, decoded.Data.Records, 1)
			}
		})
	}
}
