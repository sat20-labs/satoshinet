# SatoshiNet Architecture Notes

## Project Positioning

SatoshiNet is a BTC Layer 2 extension network forked from btcd.

Its high-level goal is:

- Assets originate from the Bitcoin network.
- Assets are locked on Bitcoin through Lightning-style channels / locking flows.
- Locked assets are automatically mapped into SatoshiNet.
- SatoshiNet then acts as the execution and circulation layer for those mapped assets.

In other words, this codebase is not just a renamed btcd node. It is a custom network node built on top of btcd with additional asset, anchor, validator, miner, and indexer-driven behavior.

## Main Differences From btcd

### 1. Custom chain identity

SatoshiNet defines its own network parameters instead of using standard Bitcoin mainnet/testnet directly.

Key observations:

- `chaincfg/params.go` defines `MainNetParams` with `Name: "satsnet"` and `DefaultPort: "9526"`.
- `TestNetParams` is also customized with `DefaultPort: "19526"`.
- The repo keeps Bitcoin-like timing and subsidy defaults in many places, but it is clearly running a distinct network identity.

Relevant files:

- `chaincfg/params.go`

### 2. Native asset support in transaction outputs

Unlike btcd, transaction outputs are extended to carry asset data directly.

Key observations:

- `wire.TxOut` has an added `Assets TxAssets` field.
- Tx output serialization and deserialization were modified to encode and decode `TxAssets`.
- `wire/txassets.go` defines the asset encoding format and aliases asset types from the external indexer model.

This means assets are part of the base transaction model, not just an RPC-side decoration.

Relevant files:

- `wire/msgtx.go`
- `wire/txassets.go`

### 3. Validation and mining understand asset balances

The asset model is propagated into consensus-adjacent logic.

Key observations:

- `blockchain/validate.go` tracks input assets, output assets, and fee assets.
- `mempool` stores `FeeAssets`.
- `mining/mining.go` merges fee assets into the block reward output.

This shows the system treats asset accounting as a first-class rule throughout mempool acceptance and block assembly.

Relevant files:

- `blockchain/validate.go`
- `mempool/mempool.go`
- `mining/mining.go`

### 4. Anchor / de-anchor flow from Bitcoin into SatoshiNet

This appears to be the bridge entry path for bringing BTC-side locked assets into SatoshiNet.

Key observations:

- A dedicated `anchortx` package was added.
- `AnchorInfo` records:
  - locked BTC UTXO
  - witness script
  - value
  - mapped assets
  - signature
- Anchor data is parsed from transaction script data.
- Mempool logic prevents the same locked BTC UTXO from being mapped more than once.

This fits the project goal of locking assets on BTC and mapping them into SatoshiNet.

Relevant files:

- `anchortx/anchortx.go`
- `mempool/anchortx.go`
- `blockchain/anchorcache.go`

### 5. Extended peer protocol with validator identity

The P2P protocol is no longer just standard btcd behavior.

Key observations:

- `wire.MsgVersion` includes a `ValidatorId` field.
- The version message encoder and decoder were extended to carry validator identity.
- `server` tracks `minerPeers` by validator ID.

This means peers are not just anonymous Bitcoin nodes; they can participate in a validator/miner topology.

Relevant files:

- `wire/msgversion.go`
- `peer/peer.go`
- `server.go`

### 6. Custom mining / validator message flow

The wire protocol includes extra messages used for miner coordination.

Key observations:

- Added `wire/msgmineblock.go`
- Added `wire/msgmineact.go`
- These messages can carry subcommands, payloads, and signatures.

This is a strong signal that block production is coordinated through custom validator/miner flows, not plain btcd mining.

Relevant files:

- `wire/msgmineblock.go`
- `wire/msgmineact.go`

### 7. PoS-like validator sequencing instead of plain btcd PoW node behavior

The repository adds a full `mining/posminer` subsystem.

Key observations:

- `ValidatorManager` reads mining order / topology from the shared indexer state.
- It reasons about current miner, next miner, father node, and validator identity.
- Generated block payloads are signed and later verified against validator public keys.
- `server` starts the PoS miner after sync and STP initialization.

This is far beyond the original btcd CPU miner model.

Relevant files:

- `mining/posminer/validatormanager.go`
- `mining/posminer/posminer.go`
- `server.go`

### 8. Hard dependency on external indexer services and shared state

This project is architecturally coupled to an indexer layer.

Key observations:

- `go.mod` replaces `github.com/sat20-labs/indexer` with local `../indexer`.
- The repo includes an internal `indexer/` integration layer.
- Startup code creates and initializes an indexer manager.
- There is also an `httpclient` layer that talks to indexer APIs.

This suggests the node depends on indexed asset / mining-sequence / bridge state that plain btcd does not have.

Relevant files:

- `go.mod`
- `indexer/main.go`
- `httpclient/indexerclient.go`
- `indexer/share/indexer`

### 9. STP plugin wallet integration for signing and staking-like actions

The node depends on an external plugin-backed wallet / signer.

Key observations:

- `stp/stpmgr.go` dynamically loads `./stpd.so`.
- The plugin provides wallet existence checks, unlock, import, create, pubkey retrieval, and message signing.
- Mining startup requires wallet access and signing ability.

This means operational security and validator signing are delegated to a plugin component rather than native btcd wallet logic.

Relevant files:

- `stp/stpmgr.go`
- `btcd.go`

### 10. RPC layer exposes SatoshiNet-specific state

The RPC server was extended for bridge / asset aware behavior.

Key observations:

- Added `getanchortxinfo`.
- `getrawtransaction` can surface parsed anchor info.
- `gettxout` responses include asset information.
- There are also signs of additional VC block RPC command types in `btcjson` and `rpcclient`.

Relevant files:

- `rpcserver.go`
- `btcjson/chainsvrcmds.go`
- `btcjson/chainsvrresults.go`
- `rpcclient/chain.go`

## Practical Mental Model

The easiest way to think about this repository is:

`btcd base node`
`+ custom satsnet chain parameters`
`+ BTC-to-SatoshiNet anchor mapping`
`+ asset-aware transactions`
`+ validator / sequenced mining layer`
`+ indexer-driven network state`
`+ plugin wallet signing`

So the project is best understood as a specialized BTC Layer 2 node implementation, not a light btcd fork.

## Notes About Confidence

These notes were derived by reading the local codebase and comparing the current tree against the locally available `sat20-labs/btcd` module contents.

High-confidence areas:

- chain parameter changes
- asset fields in transaction format
- anchor transaction flow
- validator ID handshake changes
- PoS miner / validator subsystem
- indexer and STP integration

Areas worth deeper follow-up:

- exact consensus-critical rule differences vs upstream btcd
- whether all added RPC command types are fully implemented
- precise interaction between Lightning locking flow and anchor tx generation
