//go:build rpctest

package main

import (
	"os"
	"time"
)

// Keep live reports fresh while exercising real declaration expiry.
func nodeAccessTiming(defaults accessTiming) accessTiming {
	if os.Getenv("SATOSHINET_RPCTEST_FAST_ACCESS") == "1" {
		return accessTiming{
			ping: time.Second, idlePing: 2 * time.Second,
			expiry: 8 * time.Second, selectionWait: 3 * time.Second,
		}
	}
	return defaults
}
