package indexer

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestRGB11TechnicalRegistrationHasOnlyBusinessWriteRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Service{handle: &Handle{}}).InitRouter(router, "")
	count := 0
	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "/v3/rgb11/contracts/register") {
			if route.Method != "POST" || route.Path != "/v3/rgb11/contracts/register" {
				t.Fatalf("unexpected RGB11 registration route %s %s", route.Method, route.Path)
			}
			count++
		}
	}
	if count != 1 {
		t.Fatalf("RGB11 registration route count = %d, want 1", count)
	}
}

func TestRGB11TechnicalRegistrationRejectsUnsignedOrWrongKey(t *testing.T) {
	for _, record := range []*swire.DKVSRecord{
		nil,
		{Key: "/contract/rgb11/short"},
		{Key: "/contract/rgb11/" + strings.Repeat("a", 64), Seq: 1, Value: []byte{2, 0}},
		{Key: "/contract/rgb11/" + strings.Repeat("a", 64), Seq: 1, Value: []byte{1, 0}, PubKey: make([]byte, 33)},
	} {
		if _, err := validateRGB11TechnicalRecord(record); err == nil {
			t.Fatalf("accepted malformed technical record: %+v", record)
		}
	}
}
