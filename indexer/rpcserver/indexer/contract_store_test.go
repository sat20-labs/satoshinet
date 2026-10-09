package indexer

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	share "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/wire"
	"net/http"
	"net/http/httptest"
	"testing"
)

type contractHTTPIndexer struct {
	share.Indexer
	corePubKey []byte
	wrote      *wire.DKVSRecord
}

func (f *contractHTTPIndexer) PutDKVSInternalContract(record *wire.DKVSRecord) (bool, error) {
	if !bytes.Equal(record.PubKey, f.corePubKey) {
		return false, dkvsindexer.ErrPermissionDenied
	}
	copied := *record
	f.wrote = &copied
	return true, nil
}
func TestContractStoreHTTPAuthorityAndLocalBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := &contractHTTPIndexer{}
	router := gin.New()
	NewService(fixture).InitRouter(router, "/testnet")
	corePubKey := []byte{2, 3, 4}
	fixture.corePubKey = append([]byte(nil), corePubKey...)
	record := &wire.DKVSRecord{
		Version: dkvsindexer.Version,
		Key:     "/contract/example/opaque",
		Value:   []byte("router delegates content validation to the real indexer"),
		PubKey:  corePubKey,
		Seq:     1,
	}
	body, err := json.Marshal(map[string]any{"record": record})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/testnet/v3/dkvs/internal/contract", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fixture.wrote == nil || fixture.wrote.Key != record.Key {
		t.Fatalf("CoreNode internal register status=%d body=%s wrote=%+v", response.Code, response.Body.String(), fixture.wrote)
	}

	fixture.corePubKey = []byte("different")
	request = httptest.NewRequest(http.MethodPost, "/testnet/v3/dkvs/internal/contract", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-CoreNode register status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/testnet/v3/dkvs/internal/contract", bytes.NewReader(body))
	request.RemoteAddr = "198.51.100.10:12345"
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-local register status=%d body=%s", response.Code, response.Body.String())
	}

}
