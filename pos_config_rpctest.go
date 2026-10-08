//go:build rpctest

package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/stp"
)

// The local harness can exercise activation without assigning a production
// height or exposing a per-node consensus override in release binaries.
func configurePOSV2TestNetwork(params *chaincfg.Params) error {
	raw := os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT")
	if raw == "" {
		return nil
	}
	if params.Name != "testnet" {
		return fmt.Errorf("POS v2 harness requires testnet")
	}
	height, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || height <= 0 {
		return fmt.Errorf("invalid POS v2 harness activation height %q", raw)
	}
	params.POSV2Height = int32(height)
	chaincfg.TestNetParams.POSV2Height = int32(height) // shared L2 indexer parameters
	return nil
}

// Restarted local harness nodes have a wallet already; no terminal is attached.
// This hook exists only in rpctest binaries and never in release builds.
func unlockRPCTestWallet() (bool, error) {
	password := os.Getenv("SATOSHINET_RPCTEST_STP_PASSWORD")
	if password == "" {
		return false, nil
	}
	if activeNetParams.Name != "testnet" || os.Getenv("SATOSHINET_RPCTEST_STP_MNEMONIC") == "" {
		return true, fmt.Errorf("test wallet unlock requires local testnet harness")
	}
	return true, stp.UnlockWallet(password)
}
