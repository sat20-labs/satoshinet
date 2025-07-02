package main

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/btcwallet"
)

func TestGenerateGenesisBlock(t *testing.T) {
	btcwallet.InitWalletManager(btcctlHomeDir, &chaincfg.MainNetParams)
	GenerateGenesisBlock(&chaincfg.MainNetParams)
	fmt.Printf("ok\n")
}