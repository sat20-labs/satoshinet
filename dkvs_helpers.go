package main

import "github.com/sat20-labs/satoshinet/chaincfg/chainhash"

func dkvsKeyHash(key string) chainhash.Hash {
	return chainhash.DoubleHashH([]byte(key))
}
