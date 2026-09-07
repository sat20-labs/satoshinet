package indexer

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDKVSWalletRoutesExposeOnlyFinalApplicationProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	service := &Service{handle: &Handle{}}
	service.InitRouter(router, "")

	got := make(map[string]struct{})
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/v3/dkvs/") {
			got[route.Method+" "+route.Path] = struct{}{}
		}
	}
	for _, route := range []string{
		"GET /v3/dkvs/config",
		"GET /v3/dkvs/record",
		"GET /v3/dkvs/key-state",
		"POST /v3/dkvs/records/batch-cas",
		"POST /v3/dkvs/prefixes/status",
		"POST /v3/dkvs/prefixes/snapshot",
		"POST /v3/dkvs/prefixes/read",
	} {
		if _, ok := got[route]; !ok {
			t.Fatalf("missing final DKVS application route %s", route)
		}
	}

	for _, forbidden := range []string{
		"/v3/dkvs/pathmeta",
		"/v3/dkvs/path-meta",
		"/v3/dkvs/sync/path",
		"/v3/dkvs/watch/path",
		"/v3/dkvs/records/cas",
		"/v3/dkvs/tombstone",
		"/v3/dkvs/sync",
		"/v3/dkvs/watch",
		"/v3/dkvs/sync/directory",
		"/v3/dkvs/watch/directory",
		"/v3/dkvs/subscriptions/snapshot",
		"/v3/dkvs/subscriptions/watch",
	} {
		for route := range got {
			if strings.HasSuffix(route, " "+forbidden) {
				t.Fatalf("legacy DKVS application route remains exposed: %s", route)
			}
		}
	}
}
