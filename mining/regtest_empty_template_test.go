package mining

import (
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/txscript"
)

func TestRegtestEmptyTemplateKeepsPublicPolicy(t *testing.T) {
	for _, original := range []*chaincfg.Params{&chaincfg.RegressionNetParams, &chaincfg.TestNetParams, &chaincfg.MainNetParams} {
		t.Run(original.Name, func(t *testing.T) {
			params := *original
			db, err := database.Create("ffldb", t.TempDir(), params.Net)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			clock := blockchain.NewMedianTime()
			chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: clock})
			if err != nil {
				t.Fatal(err)
			}
			generator := NewBlkTmplGenerator(&Policy{BlockMaxWeight: blockchain.MaxBlockWeight}, &params, newAnchorTemplateTxSource(), chain, clock, txscript.NewSigCache(10), txscript.NewHashCache(10))
			// Reuse the existing template tests' isolated signer. The live-node
			// gate separately verifies the actual operator's coinbase signature.
			generator.coinbaseSigner = func([]byte) ([]byte, error) { return []byte("test-signature"), nil }
			_, err = generator.NewBlockTemplate(nil)
			if err == nil || !strings.Contains(err.Error(), "no any new tx in mempool") {
				t.Fatalf("ordinary mining empty-block policy changed: %v", err)
			}
		})
	}
}
