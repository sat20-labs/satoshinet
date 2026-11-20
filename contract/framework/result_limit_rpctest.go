//go:build rpctest

package framework

import "os"

// ResultOutputLimit returns the limit shared by execution and validation.
// A serial in-process test may scope the override with t.Setenv.
func ResultOutputLimit() int {
	if os.Getenv("SATOSHINET_RPCTEST_RESULT_OUTPUT_LIMIT") == "10" {
		return 10
	}
	return MaxContractResultOutputs
}
