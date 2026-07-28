integration
===========

[![Build Status](https://github.com/sat20-labs/satoshinet/workflows/Build%20and%20Test/badge.svg)](https://github.com/sat20-labs/satoshinet/actions)
[![ISC License](http://img.shields.io/badge/license-ISC-blue.svg)](http://copyfree.org)

This directory contains SatoshiNet integration tests which use the
[rpctest](https://github.com/sat20-labs/satoshinet/tree/main/integration/rpctest)
package to drive nodes via RPC.

The supported network-level suite lives in `contract_e2e` and models the
current SatoshiNet runtime: a configured L1 indexer, wallet-backed POS signing,
and transaction-driven block production. Run it with:

```bash
go test -tags=rpctest ./integration/contract_e2e
```

The upstream btcd tests which depended on PoW hash-rate RPCs, empty-block chain
bootstrapping, fixed 50 BTC coinbase outputs, or the legacy in-memory wallet
were removed because those assumptions do not match SatoshiNet consensus.
The old prune RPC test was also removed because SatoshiNet requires its default
transaction index, while the node intentionally rejects enabling `--prune` and
`--txindex` together.

## License

This code is licensed under the [copyfree](http://copyfree.org) ISC License.
