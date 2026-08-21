package blockchain

import (
	"sync"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

type captureEnsureReadiness struct {
	mu        sync.Mutex
	height    int
	hash      chainhash.Hash
	ready     bool
	ensureTip int
}

func (r *captureEnsureReadiness) GetInternalTip() (int, chainhash.Hash, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.height, r.hash, true
}

func (r *captureEnsureReadiness) InternalTipReady(height int, hash *chainhash.Hash) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready && hash != nil && r.height == height && r.hash == *hash
}

func (r *captureEnsureReadiness) EnsureInternalTip(height int, hash *chainhash.Hash, tip int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureTip = tip
	r.height = height
	r.hash = *hash
	r.ready = true
	return nil
}

func (r *captureEnsureReadiness) WaitForInternalTip(int, *chainhash.Hash, <-chan struct{}) error {
	return nil
}

func TestDirectReadinessUsesCanonicalTipForUnheightedCandidate(t *testing.T) {
	chain, _, _, teardown := setupReadinessChain(t)
	defer teardown()
	block := readinessTestBlock(t, chain)
	block.SetHeight(-1)

	ready := &captureEnsureReadiness{ensureTip: -99}
	chain.assetIndexReadiness = ready
	if _, _, err := chain.ProcessBlock(block, BFNoPoWCheck); err != nil {
		t.Fatalf("process unheighted candidate: %v", err)
	}

	ready.mu.Lock()
	got := ready.ensureTip
	ready.mu.Unlock()
	if got != 0 {
		t.Fatalf("EnsureInternalTip tip=%d, want canonical genesis height 0", got)
	}
}
