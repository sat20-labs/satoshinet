package httpclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

func TestIndexerPublicKeyUsesCurrentReadAPI(t *testing.T) {
	const key = "025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad"
	for _, reject := range []bool{false, true} {
		name := "read_metadata"
		if reject {
			name = "preserve_server_rejection"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/btc/regtest/v3/indexer/pubkey" {
					http.NotFound(w, r)
					return
				}
				response := indexerwire.IndexerPubKeyResp{PubKey: key}
				if reject {
					response.Code = -1
					response.Msg = "indexer unavailable"
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := NewIndexerClient("http", strings.TrimPrefix(server.URL, "http://"), "btc/regtest")
			got, err := client.GetIndexerPubkey(key)
			if reject {
				if err == nil || !strings.Contains(err.Error(), "indexer unavailable") || got != "" {
					t.Fatalf("server rejection lost: key=%s err=%v", got, err)
				}
			} else if err != nil || got != key {
				t.Fatalf("current indexer metadata read failed: key=%s err=%v", got, err)
			}
		})
	}
}
