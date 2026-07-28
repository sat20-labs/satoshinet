rpctest
=======

[![Build Status](https://github.com/sat20-labs/satoshinet/workflows/Build%20and%20Test/badge.svg)](https://github.com/sat20-labs/satoshinet/actions)
[![ISC License](http://img.shields.io/badge/license-ISC-blue.svg)](http://copyfree.org)
[![GoDoc](https://img.shields.io/badge/godoc-reference-blue.svg)](https://pkg.go.dev/github.com/sat20-labs/satoshinet/integration/rpctest)

Package rpctest provides the low-level process and RPC harness used by
SatoshiNet integration tests. Callers must explicitly provide the runtime
required by their scenario, including indexer configuration and, when blocks
need signing, a wallet-enabled executable.

The harness retains some btcd-derived helpers for compatibility, but its old
standalone self-test suite is not a valid SatoshiNet consensus test. In
particular, SatoshiNet does not support bootstrapping tests by mining empty
blocks into a fixed 50 BTC in-memory wallet.

## Installation and Updating

The maintained end-to-end use of this package is in
`integration/contract_e2e`.

## License

Package rpctest is licensed under the [copyfree](http://copyfree.org) ISC
License.
