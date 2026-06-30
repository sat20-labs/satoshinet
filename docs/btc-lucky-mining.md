# BTC Lucky Mining

BTC Lucky Mining is an optional Bitcoin-native CPU mining module. SatoshiNet
nodes and wallets both fetch compact BTC mining jobs from the L1 indexer, mine
locally, and submit solutions back to the L1 indexer.

## Scope

- BTC Lucky Mining is controlled by `btcluckymining`, not by SatoshiNet PoS
  mining or `generate`.
- The reward address is always the channel address between the local participant
  and the serving core node. A wallet uses the L1 indexer provider's pubkey as
  the core node pubkey.
- The Bitcoin coinbase input script only includes the `satoshinet` tag and the
  extra nonce. Winner identity is determined by the coinbase reward address.
- Bitcoin block, transaction, header, getblocktemplate, and submitblock handling
  use `github.com/btcsuite/btcd/...` types, not SatoshiNet's extended wire
  types.
- The L1 indexer rebuilds the block from the cached job and submitted solution,
  verifies the hash target and reward address, then calls Bitcoin Core
  `submitblock`.
- Found block metadata is recorded in the L1 indexer's local DB under the
  `btclucky:found:` prefix.

## Flow

```text
Bitcoin Core / bitcoind
        ^
        | getblocktemplate / submitblock
        |
L1 indexer BTC lucky service
        ^
        | HTTP compact job / solution
        |
SatoshiNet node or wallet local miner
```

## SatoshiNet Configuration

```ini
btcluckymining=0
btcluckyminingjobs=1
btcluckymininglowpriority=1
btcluckymininglowprioritysleep=1ms
```

SatoshiNet nodes use the configured L1 indexer endpoint:

```ini
indexerscheme=http
indexerhost=127.0.0.1:8009
indexerproxy=testnet
```

The template service runs in the L1 indexer. SatoshiNet nodes do not expose
local BTC lucky mining RPC methods.
