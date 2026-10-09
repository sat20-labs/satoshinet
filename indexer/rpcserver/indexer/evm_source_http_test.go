package indexer

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	contract "github.com/sat20-labs/satoshinet/contract/engine"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type sourceHTTPBackend struct {
	shareIndexer.Indexer
	store *dkvs.Indexer
}

func (b *sourceHTTPBackend) PutDKVSRecordCAS(record *wire.DKVSRecord, precondition dkvs.WritePrecondition) (bool, error) {
	return b.store.PutLocalCAS(record, precondition)
}

func (b *sourceHTTPBackend) GetEVMSourceMetadata(address string) (contract.EVMSourceMetadata, bool) {
	record, err := b.store.Get("/contract/evm/source/" + address)
	if err != nil {
		return contract.EVMSourceMetadata{}, false
	}
	var metadata contract.EVMSourceMetadata
	err = json.Unmarshal(record.Value, &metadata)
	return metadata, err == nil
}

// This checks HTTP admission and atomic DKVS writes. Compiler and canonical
// deployment verification have their own tests in the evmsource package.
func TestEVMSourceHTTPRequiresSignedImmutableRecord(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	t.Cleanup(func() { _ = database.Close() })
	store := dkvs.New(database, dkvs.Config{EVMSourceVerifier: func(record *wire.DKVSRecord) error {
		if string(record.Value) != `{"version":1,"source":"checked"}` {
			return dkvs.ErrInvalidRecord
		}
		return nil
	}})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewService(&sourceHTTPBackend{store: store}).InitRouter(router, "")
	url := "/v3/contracts/testcontract/evm/source"
	post := func(body any) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	require.Equal(t, http.StatusBadRequest, post(map[string]any{"source": "unsigned", "verified": true}).Code)
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	sign := func(key, value string) *wire.DKVSRecord {
		record, err := dkvs.NewRecord(key, []byte(value), priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 1})
		require.NoError(t, err)
		hash := dkvs.SigningHash(record)
		record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
		return record
	}
	key := "/contract/evm/source/testcontract"
	bad := sign(key, `{"source":"unchecked"}`)
	require.Equal(t, http.StatusBadRequest, post(map[string]any{"record": bad}).Code)
	_, err = store.Get(key)
	require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	record := sign(key, `{"version":1,"source":"checked"}`)
	wrong := sign("/contract/evm/source/other", string(record.Value))
	require.Equal(t, http.StatusBadRequest, post(map[string]any{"record": wrong}).Code)
	unsigned := *record
	unsigned.Signature = nil
	require.Equal(t, http.StatusBadRequest, post(map[string]any{"record": &unsigned}).Code)
	for i := 0; i < 2; i++ {
		response := post(map[string]any{"record": record})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	require.Equal(t, http.StatusConflict, post(map[string]any{"record": bad}).Code)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "checked")
	got, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(got))
}
