//go:build !rpctest

package main

import "github.com/sat20-labs/satoshinet/chaincfg"

func configurePOSV2TestNetwork(*chaincfg.Params) error { return nil }

func unlockRPCTestWallet() (bool, error) { return false, nil }
