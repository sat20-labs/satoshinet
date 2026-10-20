package blockchain

import (
	"github.com/sat20-labs/satoshinet/chaincfg"
	"testing"
	"time"
)

// Exercise the unchanged BIP9 state machine with the actual network parameters.
func TestRegressionDeploymentsUseNormalWarmup(t *testing.T) {
	params := chaincfg.RegressionNetParams
	chain := newFakeChain(&params)
	for height := int32(0); height <= 3; height++ {
		if height > 0 {
			node := newFakeNode(chain.bestChain.Tip(), int32(vbTopBits), params.PowLimitBits, params.GenesisBlock.Header.Timestamp.Add(time.Duration(height)*time.Second))
			chain.index.AddNode(node)
			chain.bestChain.SetTip(node)
		}
		want := ThresholdActive
		if height == 0 {
			want = ThresholdStarted
		}
		if height == 1 {
			want = ThresholdLockedIn
		}
		for _, id := range []uint32{chaincfg.DeploymentCSV, chaincfg.DeploymentSegwit, chaincfg.DeploymentTaproot} {
			state, err := chain.deploymentState(chain.bestChain.Tip(), id)
			if err != nil || state != want {
				t.Fatalf("tip=%d next=%d deployment=%d: got %v, %v; want %v", height, height+1, id, state, err, want)
			}
		}
	}
}
