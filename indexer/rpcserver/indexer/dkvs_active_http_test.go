package indexer

import (
 "context"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
)

type activeHTTPBackend struct {
	shareIndexer.Indexer
	store *dkvs.Indexer
}

func (b *activeHTTPBackend) GetDKVSActivePage(ctx context.Context, req dkvs.ActiveSyncRequest) (*dkvs.ActivePage,error) {return b.store.ActiveSyncPage(ctx,req)}
func (b *activeHTTPBackend) WatchDKVSActive(ctx context.Context, req dkvs.ActiveWatchRequest) (*dkvs.ActiveWatchResult,error) {return b.store.WatchActive(ctx,req)}
func (b *activeHTTPBackend) ReadDKVSPrefix(prefix string) (*dkvs.PrefixReadResult, error) {
	return b.store.ReadPrefix(prefix)
}

func TestDKVSActiveIncrementalHTTPWithLiveIndexer(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	height := uint64(100)
	store := dkvs.New(database, dkvs.Config{
		EndpointID: "http-test-node", AllowFreeLocal: true,
		FeeVerifier:   dkvs.JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return height },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + dkvs.AccountID(priv.PubKey().SerializeCompressed()) + "/wallet/"
	sign := func(key, value string) *dkvs.Record {
		record, buildErr := dkvs.NewAccountRecord(key, []byte(value), dkvs.RecordOptions{
			Seq: 1, IssueHeight: 100, TTL: 1000,
		})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		hash := dkvs.SigningHash(record)
		signature, signErr := schnorr.Sign(priv, hash[:])
		if signErr != nil {
			t.Fatal(signErr)
		}
		record.Signature = signature.Serialize()
		return record
	}
	if _, err := store.PutLocal(sign(base+"first", "first")); err != nil {
		t.Fatal(err)
	}
	prefix, err := dkvs.CollectionPathForKey(base + "first")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.ActiveSyncPage(context.Background(), dkvs.ActiveSyncRequest{Scope: dkvs.ActiveScope{Prefix: prefix}, EndpointID: store.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	height = 105
	if _, err := store.PutLocal(sign(base+"late", "late")); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewService(&activeHTTPBackend{store: store}).InitRouter(router, "")
	body, err := json.Marshal(map[string]interface{}{
		"scope": dkvs.ActiveScope{Prefix: prefix}, "endpoint_id": initial.Meta.EndpointID,
		"after": initial.Meta.Generation,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v3/dkvs/active/sync", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data *dkvs.ActivePage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Data == nil || len(decoded.Data.Records) != 1 || decoded.Data.Records[0].Key != base+"late" {
		t.Fatalf("HTTP delta=%+v", decoded.Data)
	}
}
