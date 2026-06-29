# BTC Lucky Mining

BTC Lucky Mining is an optional Bitcoin-native CPU mining module for SatoshiNet
nodes. It is disabled by default and does not participate in SatoshiNet PoS
mining, consensus, mempool policy, or validator scheduling.

## Confirmed V1 Scope

- BTC Lucky Mining is controlled by `btcluckymining`, not by `generate`.
- BTC template service is controlled by `btcluckytemplateservice`.
- Bootstrap nodes must not start BTC Lucky Mining or BTC template service.
- The reward address is the existing SatoshiNet mining/channel address. Except
  contract addresses, SatoshiNet addresses are compatible with Bitcoin
  addresses, so the same address can be used as the Bitcoin coinbase output.
- No development address override is added. The module must resolve the reward
  address from the node's configured mining/channel identity.
- If no configured SatoshiNet mining address exists, BTC Lucky Mining requires
  `serverpubkey` so the node can derive the channel reward address.
- Bitcoin block, transaction, header, getblocktemplate, and submitblock handling
  use `github.com/btcsuite/btcd/...` types. They must not use SatoshiNet's
  extended `wire` package because SatoshiNet transaction outputs include asset
  serialization that is not valid Bitcoin block serialization.
- BTC lucky jobs run in cooperative low-priority mode by default. They yield
  and briefly sleep during the hash loop so other runnable processes and the
  SatoshiNet node's own critical goroutines can take CPU first. This does not
  lower the priority of the whole node process.
- The Bitcoin coinbase input script includes a `satoshinet` tag and the
  SatoshiNet node `miningpubkey` as miner metadata. If `miningpubkey` is empty,
  the miner falls back to the reward address as metadata. The coinbase output
  pays the configured mining/channel reward address.
- V1 records found block metadata in memory and appends it to
  `btc_lucky_found_blocks.jsonl` under the node data directory. Coinbase
  maturity tracking and channel credit are follow-up work.

## Current V1 Flow

```text
Bitcoin Core / bitcoind
        ^
        | getblocktemplate / submitblock
        |
SatoshiNet core node BTC template service
        ^
        | peer-template compact job / solution
        |
BTC lucky CPU miner on miner or ordinary node
```

The miner requests a compact job from a connected core peer by default. The
core node builds the Bitcoin coinbase transaction paying the configured
mining/channel reward address, returns only compact header work to the miner,
and reconstructs/submits the solved Bitcoin block through Bitcoin Core RPC.

A core node can also set `btcluckyminingbackend=local-template` to mine against
its in-process template service without a P2P round trip. In both modes the
reward address remains the node's SatoshiNet mining/channel address.

## Configuration

```ini
btcluckymining=0
btcluckyminingbackend=peer-template
btcluckyminingjobs=1
btcluckyminingreservecores=0
btcluckymininglowpriority=1
btcluckyminingnetwork=mainnet

btcluckytemplateservice=0
btcluckytemplatebackend=bitcoin-core
btcluckytemplaterpcconnect=127.0.0.1:8332
btcluckytemplaterpcuser=
btcluckytemplaterpcpass=
btcluckytemplaterpcdisabletls=1
btcluckytemplatenetwork=mainnet
btcluckytemplaterefreshinterval=60s
btcluckytemplatejobttl=120s
btcluckytemplatecachelimit=16
```

## RPC

- `getbtcluckymininginfo`
- `getbtcluckyhashrate`
- `getbtctemplateserviceinfo`

Existing PoS mining RPCs such as `getgenerate`, `setgenerate`, and
`gethashespersec` continue to report SatoshiNet PoS miner state only.
