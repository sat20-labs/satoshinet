// Copyright (c) 2013-2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
)

// TestCheckBlockScripts ensures that validating the all of the scripts in a
// known-good block doesn't return an error.
func TestCheckBlockScripts(t *testing.T) {
	chain, teardownFunc, err := chainSetup("checkblockscripts",
		&chaincfg.RegressionNetParams)
	if err != nil {
		t.Errorf("Failed to setup chain instance: %v", err)
		return
	}
	defer teardownFunc()

	block, _, err := newBlock(chain, btcutil.NewBlock(chain.chainParams.GenesisBlock), nil)
	if err != nil {
		t.Errorf("Error creating test block: %v\n", err)
		return
	}
	view := NewUtxoViewpoint()

	scriptFlags := txscript.ScriptBip16
	err = checkBlockScripts(block, view, scriptFlags, nil, nil, chain.chainParams)
	if err != nil {
		t.Errorf("Transaction script validation failed: %v\n", err)
		return
	}
}
