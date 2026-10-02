package indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
	share "github.com/sat20-labs/satoshinet/indexer/share/indexer"
)

type namingHTTPIndexer struct {
	share.Indexer
	names *rgb11names.Index
}

func (f *namingHTTPIndexer) GetRGB11Naming(q rgb11names.Query) (*rgb11names.Result, error) {
	return f.names.Lookup(q)
}

func TestRGB11NamingHTTPRegistryE2E(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()

	params := &chaincfg.TestNetParams
	id := func(n int) string { return fmt.Sprintf("%064x", n) }
	names, err := rgb11names.Open(db, params, rgb11names.Cursor{Height: -1})
	if err != nil {
		t.Fatal(err)
	}
	block := &common.Block{
		Height: 0, Hash: id(100),
		Transactions: []*common.Transaction{{Txid: id(101)}, {Txid: id(102)}},
	}
	events := []rgb11names.Event{{
		TxIndex: 1, EventIndex: 0, TxID: id(102),
		Register: &rgb11names.Register{
			ContractID: id(1), BaseTicker: "USD", AssetType: "f",
			GenesisOutpoint: id(300) + ":0", ProviderDID: "alice",
		},
	}}
	if err := names.ApplyBlock(block, events); err != nil {
		t.Fatal(err)
	}
	batch := db.NewWriteBatch()
	ack, err := names.Stage(batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Put([]byte("naming-e2e-base-checkpoint"), []byte(block.Hash)); err != nil {
		t.Fatal(err)
	}
	if err := batch.Flush(); err != nil {
		t.Fatal(err)
	}
	ack()
	batch.Close()

	restored, err := rgb11names.Open(db, params, rgb11names.Cursor{Height: 0, Hash: block.Hash})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	NewService(&namingHTTPIndexer{names: restored}).InitRouter(router, "/testnet")

	for _, test := range []struct {
		path   string
		status int
	}{
		{"/v3/rgb11/naming/status", http.StatusOK},
		{"/v3/rgb11/contract/" + id(1), http.StatusOK},
		{"/v3/rgb11/name/" + url.PathEscape("rgb11:f:usd@alice"), http.StatusOK},
		{"/v3/rgb11/ordinal?provider=alice&ticker=USD", http.StatusOK},
		{"/v3/rgb11/contract/" + id(99), http.StatusNotFound},
		{"/v3/rgb11/contract/not-a-contract", http.StatusBadRequest},
		{"/v3/rgb11/ordinal?provider=abcdefghijk&ticker=USD", http.StatusBadRequest},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/testnet"+test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if test.status == http.StatusOK {
				var body map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				var code int
				var data rgb11names.Result
				if json.Unmarshal(body["code"], &code) != nil ||
					json.Unmarshal(body["data"], &data) != nil ||
					code != 0 || data.Cursor.Hash != block.Hash {
					t.Fatalf("bad snapshot response: %s", response.Body.String())
				}
				if data.Registration != nil &&
					(data.Registration.ContractID != id(1) ||
						data.Registration.AssetName != "rgb11:f:usd@alice") {
					t.Fatalf("wrong mapping: %+v", data)
				}
			}
		})
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, path := range []string{
			"/v3/rgb11/contract/" + id(1),
			"/v3/rgb11/ordinal",
		} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(
				method, "/testnet"+path, bytes.NewBufferString("{\"provider\":\"attacker\"}"),
			))
			if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("mutation route exposed: %s %s -> %d", method, path, response.Code)
			}
		}
	}
	if err := restored.CheckSelf(); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11NamingHTTPMissingCapabilityIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewService(nil).InitRouter(router, "")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v3/rgb11/naming/status", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
