// Copyright (c) 2016-2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"github.com/sat20-labs/satoshinet/chaincfg"
)

const (
	// vbLegacyBlockVersion is the highest legacy block version before the
	// version bits scheme became active.
	vbLegacyBlockVersion = 4

	// vbTopBits defines the bits to set in the version to signal that the
	// version bits scheme is being used.
	vbTopBits = 0x20000000

	// vbTopMask is the bitmask to use to determine whether or not the
	// version bits scheme is in use.
	vbTopMask = 0xe0000000
)

// deploymentChecker provides a thresholdConditionChecker which can be used to
// test a specific deployment rule.  This is required for properly detecting
// and activating consensus rule changes.
type deploymentChecker struct {
	deployment *chaincfg.ConsensusDeployment
	chain      *BlockChain
}

// Ensure the deploymentChecker type implements the thresholdConditionChecker
// interface.
var _ thresholdConditionChecker = deploymentChecker{}

// HasEnded returns true if the target consensus rule change has expired
// or timed out (at the next window).
//
// This implementation returns the value defined by the specific deployment the
// checker is associated with.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) HasStarted(blkNode *blockNode) bool {
	// Can't fail as we make sure to set the clock above when we
	// instantiate *BlockChain.
	header := blkNode.Header()
	started, _ := c.deployment.DeploymentStarter.HasStarted(&header)

	return started
}

// HasEnded returns true if the target consensus rule change has expired
// or timed out.
//
// This implementation returns the value defined by the specific deployment the
// checker is associated with.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) HasEnded(blkNode *blockNode) bool {
	// Can't fail as we make sure to set the clock above when we
	// instantiate *BlockChain.
	header := blkNode.Header()
	ended, _ := c.deployment.DeploymentEnder.HasEnded(&header)

	return ended
}

// RuleChangeActivationThreshold is the number of blocks for which the condition
// must be true in order to lock in a rule change.
//
// This implementation returns the value defined by the chain params the checker
// is associated with.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) RuleChangeActivationThreshold() uint32 {
	// Some deployments like taproot used a custom activation threshold
	// that overrides the network level threshold.
	if c.deployment.CustomActivationThreshold != 0 {
		return c.deployment.CustomActivationThreshold
	}

	return c.chain.chainParams.RuleChangeActivationThreshold
}

// MinerConfirmationWindow is the number of blocks in each threshold state
// retarget window.
//
// This implementation returns the value defined by the chain params the checker
// is associated with.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) MinerConfirmationWindow() uint32 {
	return c.chain.chainParams.MinerConfirmationWindow
}

// EligibleToActivate returns true if a custom deployment can transition from
// the LockedIn to the Active state. For normal deployments, this always
// returns true. However, some deployments add extra rules like a minimum
// activation height, which can be abstracted into a generic arbitrary check at
// the final state via this method.
//
// This implementation always returns true, unless a minimum activation height
// is specified.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) EligibleToActivate(blkNode *blockNode) bool {
	// No activation height, so it's always ready to go.
	if c.deployment.MinActivationHeight == 0 {
		return true
	}

	// If the _next_ block (as this is the prior block to the one being
	// connected is the min height or beyond, then this can activate.
	return uint32(blkNode.height)+1 >= c.deployment.MinActivationHeight
}

// IsSpeedy returns true if this is to be a "speedy" deployment. A speedy
// deployment differs from a regular one in that only after a miner block
// confirmation window can the deployment expire.  This implementation returns
// true if a min activation height is set.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) IsSpeedy() bool {
	return (c.deployment.MinActivationHeight != 0 ||
		c.deployment.CustomActivationThreshold != 0)
}

// Condition returns true when the specific bit defined by the deployment
// associated with the checker is set.
//
// This is part of the thresholdConditionChecker interface implementation.
func (c deploymentChecker) Condition(node *blockNode) (bool, error) {
	conditionMask := uint32(1) << c.deployment.BitNumber
	version := uint32(node.version)
	return (version&vbTopMask == vbTopBits) && (version&conditionMask != 0),
		nil
}

// calcNextBlockVersion calculates the expected version of the block after the
// passed previous block node based on the state of started and locked in
// rule change deployments.
//
// This function differs from the exported CalcNextBlockVersion in that the
// exported version uses the current best chain as the previous block node
// while this function accepts any block node.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) calcNextBlockVersion(prevNode *blockNode) (int32, error) {
	// Set the appropriate bits for each actively defined rule deployment
	// that is either in the process of being voted on, or locked in for the
	// activation at the next threshold window change.
	expectedVersion := uint32(vbTopBits)
	for id := 0; id < len(b.chainParams.Deployments); id++ {
		deployment := &b.chainParams.Deployments[id]
		cache := &b.deploymentCaches[id]
		checker := deploymentChecker{deployment: deployment, chain: b}
		state, err := b.thresholdState(prevNode, checker, cache)
		if err != nil {
			return 0, err
		}
		if state == ThresholdStarted || state == ThresholdLockedIn {
			expectedVersion |= uint32(1) << deployment.BitNumber
		}
	}
	return int32(expectedVersion), nil
}

// CalcNextBlockVersion calculates the expected version of the block after the
// end of the current best chain based on the state of started and locked in
// rule change deployments.
//
// This function is safe for concurrent access.
func (b *BlockChain) CalcNextBlockVersion() (int32, error) {
	b.chainLock.Lock()
	version, err := b.calcNextBlockVersion(b.bestChain.Tip())
	b.chainLock.Unlock()
	return version, err
}
