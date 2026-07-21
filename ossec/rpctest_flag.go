//go:build rpctest

package ossec

// skipProcessHardening is restricted to binaries compiled with the rpctest
// build tag. Hosted CI runners cannot grant the process an unlimited memlock
// budget, while production builds continue to execute the full hardening path.
const skipProcessHardening = true
