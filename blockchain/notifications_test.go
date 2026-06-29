// Copyright (c) 2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

// TestNotifications ensures that notification callbacks are fired on events.
func TestNotifications(t *testing.T) {
	// Create a new database and chain instance to run tests against.
	chain, teardownFunc, err := chainSetup("notifications",
		&chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatalf("Failed to setup chain instance: %v", err)
	}
	defer teardownFunc()

	notificationCount := 0
	callback := func(notification *Notification) {
		if notification.Type == NTBlockAccepted {
			notificationCount++
		}
	}

	// Register callback multiple times then assert it is called that many
	// times.
	const numSubscribers = 3
	for i := 0; i < numSubscribers; i++ {
		chain.Subscribe(callback)
	}

	chain.TstSetCoinbaseMaturity(1)
	_, _, err = addBlock(chain, btcutil.NewBlock(chain.chainParams.GenesisBlock), nil)
	if err != nil {
		t.Fatalf("ProcessBlock fail on block 1: %v\n", err)
	}

	if notificationCount != numSubscribers {
		t.Fatalf("Expected notification callback to be executed %d "+
			"times, found %d", numSubscribers, notificationCount)
	}
}
