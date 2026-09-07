package posminer

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestSubmitNewBlockReportsAcceptance(t *testing.T) {
	if hash, height, err := submitNewBlock(nil, func(*btcutil.Block) bool { return true }); err == nil || hash != nil || height != 0 {
		t.Fatalf("nil block result hash=%v height=%d err=%v", hash, height, err)
	}

	block := &wire.MsgBlock{}
	if hash, height, err := submitNewBlock(block, func(*btcutil.Block) bool { return false }); err == nil || hash != nil || height != 0 {
		t.Fatalf("rejected block result hash=%v height=%d err=%v", hash, height, err)
	}

	hash, height, err := submitNewBlock(block, func(block *btcutil.Block) bool {
		block.SetHeight(42)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if hash == nil || *hash != block.BlockHash() || height != 42 {
		t.Fatalf("accepted block result hash=%v height=%d", hash, height)
	}
}
