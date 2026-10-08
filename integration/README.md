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

## POS approval upgrade

Run the dedicated POS suite with:

```bash
go test -tags=rpctest ./integration/contract_e2e -run '^TestNetworkPOSV2' -count=1 -timeout=15m -v
```

The suite uses real Bootstrap/Core wallet nodes and a fake L1 indexer.
Anchor invoices switch at the same activation height: below it the historical
message is verified; from it the signature binds every output's wire encoding
(value, assets and script, in transaction order). The Anchor input script format
is unchanged. Fixtures sign complete outputs for their target height, and an
old unconfirmed invoice must be signed again if it crosses activation. Its
original cases activate the upgrade at local height 2 and exercise approval, ordinary P2P
propagation, invalid witness variants, crash recovery, substitute rewards and
rejection of a Bootstrap-signed competing branch. It requires two subsequent
approved blocks (H6 and H7) on the original canonical chain, including a Core
proposal routed to Bootstrap. Indexer reorg, including administrator rollback,
now panics before closing databases and requires manual handling; the manager
regression covers this boundary. The former online Template invalidation/rollback
case was removed with this unsupported behavior; ordinary contract execution
checks remain. A second case pauses Bootstrap approval and verifies that
the producer cannot accept an unsigned candidate and retains it across ACK
timeouts. `SATOSHINET_RPCTEST_POS_V2_HEIGHT` is honored only by rpctest
node builds; production network activation remains unscheduled until its chain
parameters are assigned a common positive height.

The activation/replay case can be run separately:

```bash
go test -tags=rpctest ./integration/contract_e2e -run '^TestNetworkPOSV2ActivationBoundaryAndLegacyReplay$' -count=1 -timeout=8m -v
```

It activates at height 6, produces legacy blocks 1–5, restarts Bootstrap at
height 5, then produces approved blocks 6–9 with Bootstrap/Core rotation.
A fresh isolated node submits the original blocks through ordinary RPC
validation, restarts at height 5, and verifies every stored block, tip and asset
balance. A block without approval at height 6 is rejected before the original
approved block with the same hash is accepted. Restarting the fully replayed
chain also preserves its tip and asset state. This case passed in about 120
seconds including node builds. It uses locally generated legacy-format blocks.
The separate real-history replay on testnet node 104 initially failed at height
1708 on a historical repeated Anchor transaction. The identical Anchor txid is now permitted only in the fixed original
H1708/H1709 block hashes, independently of peer height or sync status. User-approved
exceptions pin the original testnet block 1748 and the specified pizzatest
and rarepizza Anchor txids, including the latter's prior funding record in
block 1747; they must be removed when testnet is rebuilt. Continued P2P replay
now reaches 3451 with the same chain and indexer tip as the source node.
Restart recovery passes, and every stored block 1–3451 matches the source hash
and complete raw bytes. Production POS activation remains unscheduled.
See the [104 replay report](../docs/pos-v2-testnet-replay-104-20261006.md).

The seven-node topology case can be run separately:

```bash
go test -tags=rpctest ./integration/contract_e2e -run '^TestNetworkPOSV2MultiCoreMultiMinerOffline$' -count=1 -timeout=10m -v
```

It registers two Cores and two Miners per Core through L1 anchor transactions,
then checks a full production round, a stopped Miner, a stopped Core with its
children still online, a stopped whole group, and production after restart and
catch-up. Each block checks the actual producer signature, Bootstrap approval,
reward script, original slot progression and a common tip among online nodes.
All four scenarios passed through height 50 (about 471 seconds including node
builds). With its Core offline, Miners retain ordinary P2P sync through the other
Core, but their candidates still require the scheduled parent relay; Bootstrap
therefore substitutes and pays the Core–Bootstrap channel.
The first run exposed a recursive indexer lock while processing an anchor
signed by the second Core at height 2. Block indexing now checks Core membership
against its own locked view; ordinary Anchor validation keeps its existing
global lookup. Targeted regressions cover the lock and historical-view mismatch.

## License

This code is licensed under the [copyfree](http://copyfree.org) ISC License.
