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
	"github.com/sat20-labs/satoshinet/btcutil"
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
	r, err := f.names.Lookup(q)
	if err == nil {
		r.SourceConfigured = true
	}
	return r, err
}

func TestRGB11NamingHTTPRegistryE2E(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	params := &chaincfg.TestNetParams
	address, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{1}, 20), params)
	if err != nil {
		t.Fatal(err)
	}
	a := address.EncodeAddress()
	id := func(n int) string { return fmt.Sprintf("%064x", n) }
	names, err := rgb11names.Open(db, params, rgb11names.Cursor{Height: -1})
	if err != nil {
		t.Fatal(err)
	}
	block := &common.Block{Height: 0, Hash: id(100), Transactions: []*common.Transaction{{Txid: id(101)}, {Txid: id(102)}}}
	// The fixed fixture stands for already authenticated upstream effects.
	// This test covers the real index/store/router, not STP proof verification.
	events := []rgb11names.Event{
		{TxIndex: 1, EventIndex: 0, TxID: id(102), Ownership: &rgb11names.Ownership{DID: "alice", Address: a, Sat: 42, Revision: 1, L1Height: 900000, L1Hash: id(200)}},
		{TxIndex: 1, EventIndex: 1, TxID: id(102), Bind: &rgb11names.Bind{DID: "alice", Address: a}},
		{TxIndex: 1, EventIndex: 2, TxID: id(102), Register: &rgb11names.Register{ContractID: id(1), BaseTicker: "USD", AssetType: "f", GenesisOutpoint: id(300) + ":0", GenesisAddress: a, AuthorizedBy: a}},
	}
	if err := names.ApplyBlock(block, events); err != nil {
		t.Fatal(err)
	}
	batch := db.NewWriteBatch()
	ack, err := names.Stage(batch)
	if err != nil {
		t.Fatal(err)
	}
	// A base-index sync marker shares the exact same atomic batch.
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
	fixture := &namingHTTPIndexer{names: restored}
	router := gin.New()
	NewService(fixture).InitRouter(router, "/testnet")
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/v3/rgb11/naming/status", 200},
		{"/v3/names/primary/" + a, 200},
		{"/v3/rgb11/contract/" + id(1), 200},
		{"/v3/rgb11/name/" + url.PathEscape("rgb11:f:usd@alice"), 200},
		{"/v3/rgb11/ordinal?provider=alice&ticker=USD", 200},
		{"/v3/rgb11/contract/" + id(99), 404},
		{"/v3/rgb11/contract/not-a-contract", 400},
		{"/v3/rgb11/ordinal?provider=abcdefghijk&ticker=USD", 400},
		{"/v3/names/primary/not-an-address", 400},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/testnet"+test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if test.status == http.StatusOK {
				var body struct {
					Code int               `json:"code"`
					Data rgb11names.Result `json:"data"`
				}
				if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Code != 0 || body.Data.Cursor.Hash != block.Hash {
					t.Fatalf("bad snapshot response: %s", response.Body.String())
				}
				if body.Data.Registration != nil && (body.Data.Registration.ContractID != id(1) || body.Data.Registration.AssetName != "rgb11:f:usd@alice") {
					t.Fatalf("wrong mapping: %+v", body.Data)
				}
			}
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, path := range []string{"/v3/names/primary/" + a, "/v3/rgb11/contract/" + id(1), "/v3/rgb11/ordinal"} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(method, "/testnet"+path, bytes.NewBufferString(`{"provider":"attacker"}`)))
			if response.Code != 404 && response.Code != 405 {
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
