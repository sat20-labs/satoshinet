//go:build !rpctest

package framework

// ResultOutputLimit returns the physical output limit for a Result tx.
func ResultOutputLimit() int { return MaxContractResultOutputs }
