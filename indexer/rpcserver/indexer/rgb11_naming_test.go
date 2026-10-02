package indexer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	share "github.com/sat20-labs/satoshinet/indexer/share/indexer"
)

type namingHTTPIndexer struct {
	share.Indexer
	byContract map[string]*dkvsindexer.RGB11Registration
	byName     map[string]*dkvsindexer.RGB11Registration
	count      uint64
}

func (f *namingHTTPIndexer) GetRGB11RegistrationByContract(contractID string) (*dkvsindexer.RGB11Registration, error) {
	if value := f.byContract[contractID]; value != nil {
		copyValue := *value
		return &copyValue, nil
	}
	return nil, dkvsindexer.ErrRecordNotFound
}

func (f *namingHTTPIndexer) GetRGB11RegistrationByName(name string) (*dkvsindexer.RGB11Registration, error) {
	if value := f.byName[name]; value != nil {
		copyValue := *value
		return &copyValue, nil
	}
	return nil, dkvsindexer.ErrRecordNotFound
}

func (f *namingHTTPIndexer) GetRGB11RegistryCount(provider, ticker string) (uint64, error) {
	if provider != "alice" || ticker == "" {
		return 0, dkvsindexer.ErrInvalidRecord
	}
	return f.count, nil
}

func TestRGB11NamingHTTPUsesDKVSRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := "0000000000000000000000000000000000000000000000000000000000000001"
	reg := &dkvsindexer.RGB11Registration{
		ContractID: id, AssetName: "rgb11:f:usd@alice",
		ProviderDID: "alice", BaseTicker: "usd", Ordinal: 1,
	}
	fixture := &namingHTTPIndexer{
		byContract: map[string]*dkvsindexer.RGB11Registration{id: reg},
		byName: map[string]*dkvsindexer.RGB11Registration{reg.AssetName: reg},
		count: 1,
	}
	router := gin.New()
	NewService(fixture).InitRouter(router, "/testnet")

	for _, test := range []struct {
		path   string
		status int
	}{
		{"/v3/rgb11/naming/status", http.StatusOK},
		{"/v3/rgb11/contract/" + id, http.StatusOK},
		{"/v3/rgb11/name/" + url.PathEscape(reg.AssetName), http.StatusOK},
		{"/v3/rgb11/ordinal?provider=alice&ticker=USD", http.StatusOK},
		{"/v3/rgb11/contract/" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/testnet"+test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s status=%d want=%d body=%s", test.path, response.Code, test.status, response.Body.String())
		}
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(
			method, "/testnet/v3/rgb11/contract/"+id, bytes.NewBufferString("{}"),
		))
		if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("mutation route exposed: %s -> %d", method, response.Code)
		}
	}
}
