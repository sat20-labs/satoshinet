package blockchain

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sirupsen/logrus"
)

// A zero network threshold must not create unknown-rule warnings. Known
// deployments must still progress and control the next block's version bits.
func TestKnownDeploymentsWithoutUnknownRuleWarnings(t *testing.T) {
	params := chaincfg.TestNetParams
	params.Checkpoints = nil
	var knownBits uint32
	for id := range params.Deployments {
		deployment := &params.Deployments[id]
		deployment.DeploymentStarter = chaincfg.NewMedianTimeDeploymentStarter(time.Time{})
		deployment.DeploymentEnder = chaincfg.NewMedianTimeDeploymentEnder(time.Time{})
		deployment.CustomActivationThreshold = 1
		deployment.MinActivationHeight = 0
		knownBits |= 1 << deployment.BitNumber
	}
	chain := newFakeChain(&params)
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	oldLog := log
	UseLogger(logrus.NewEntry(logger))
	t.Cleanup(func() { UseLogger(oldLog) })

	for height := int32(1); height <= 35; height++ {
		node := newFakeNode(chain.bestChain.Tip(), int32(vbTopBits|knownBits),
			params.PowLimitBits, time.Unix(1700000000+int64(height), 0))
		chain.index.AddNode(node)
		chain.bestChain.SetTip(node)
		if err := chain.initThresholdCaches(); err != nil {
			t.Fatalf("initialize at height %d: %v", height, err)
		}
		wantState := ThresholdDefined
		switch {
		case height+1 >= 30:
			wantState = ThresholdActive
		case height+1 >= 20:
			wantState = ThresholdLockedIn
		case height+1 >= 10:
			wantState = ThresholdStarted
		}
		for id := range params.Deployments {
			state, err := chain.deploymentState(node, uint32(id))
			if err != nil || state != wantState {
				t.Fatalf("height %d deployment %d: got %v, %v; want %v", height, id, state, err, wantState)
			}
		}
		wantVersion := uint32(vbTopBits)
		if wantState == ThresholdStarted || wantState == ThresholdLockedIn {
			wantVersion |= knownBits
		}
		version, err := chain.CalcNextBlockVersion()
		if err != nil || uint32(version) != wantVersion {
			t.Fatalf("height %d next version: got %x, %v; want %x", height, version, err, wantVersion)
		}
		if strings.Contains(output.String(), "Unknown new rules") {
			t.Fatalf("spurious rule warning at height %d: %s", height, output.String())
		}
	}
}
