package indexer

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sat20-labs/satoshinet/indexer/common"
	share "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/stretchr/testify/require"
)

type luckyListIndexer struct {
	share.Indexer
	pubkey string
	err    error
}

func (i *luckyListIndexer) GetBTCLuckyBlocks(pubkey string, start, limit int) (*common.BTCLuckyBlocks, error) {
	i.pubkey = pubkey
	return &common.BTCLuckyBlocks{Start: start, Limit: limit, Items: []common.BTCLuckyBlock{}}, i.err
}
func TestBTCLuckyBlocksHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	index := &luckyListIndexer{}
	router := gin.New()
	NewService(index).InitRouter(router, "/mainnet")
	pubkey := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"", 200}, {"?pubkey=" + strings.ToUpper(pubkey) + "&start=2&limit=1", 200},
		{"?start=-1", 400}, {"?start=no", 400}, {"?limit=0", 400}, {"?limit=1001", 400}, {"?limit=no", 400},
		{"?pubkey=abcd", 400}, {"?pubkey=" + strings.Repeat("ff", 33), 400},
	} {
		t.Run(tc.query, func(t *testing.T) {
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, httptest.NewRequest("GET", "/mainnet/v3/btc/lucky/blocks"+tc.query, nil))
			require.Equal(t, tc.status, resp.Code)
			if tc.status == 200 {
				var result struct {
					Code int
					Data common.BTCLuckyBlocks
				}
				require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &result))
				require.Zero(t, result.Code)
				require.NotNil(t, result.Data.Items)
			}
		})
	}
	require.Equal(t, pubkey, index.pubkey)
	index.err = fmt.Errorf("database unavailable")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest("GET", "/mainnet/v3/btc/lucky/blocks", nil))
	require.Equal(t, 500, resp.Code)
}
