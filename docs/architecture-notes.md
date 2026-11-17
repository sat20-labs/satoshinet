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

#### 首选 CoreNode 与在线节点观测

节点要求 P2P 版本 `70017`。`ping` 负载由 8 字节扩展为 21 字节：原有 `Nonce uint64`，加上 `NodeID uint64`、`UseAsCore bool`、`ChildCount uint32`。`version`、`addr`、`pong` 格式不变。旧协议节点在握手阶段被拒绝，因此节点部署需要协调升级。

普通节点从已连接、且被本地聪网索引器识别为 core 的出站连接中随机选择一个首选节点；连接及心跳正常时不重新选择。首选公钥及观测到的地址以紧凑字节保存在现有节点数据库中。重启时在正常建立的连接中优先复用该 core；失联后保留 10 分钟选择等待期，期满允许选择其他有新鲜心跳的 core。挖矿节点沿用配置或索引器中的上级，core 的上级为 bootstrap。

首选 core 只影响 ping 中的接入声明，不创建、取消或断开连接，也不暂停地址发现。`--connect`、`--addpeer` 及 RPC 添加的连接都沿用原连接管理器的行为，选择等待期不阻止连接其他节点。当前选中的 core 被索引器移除时清除首选记录；若它已断线，允许等待窗口结束后再重新选择。

每个进程生成一个随机 `NodeID`，所有连接共享，重启后重新生成。节点只向选中的上级发送 `UseAsCore=true`；相关连接每 20 秒发送 ping。core 按最近 60 秒的声明和存活连接统计子节点，并按 `NodeID` 去重，通过给 bootstrap 的 ping 报告 `ChildCount`。bootstrap 按索引器登记的 core 公钥去重后汇总，不累计多个连接上的重复报告。

RPC `getnodestats` 返回 `preferredcore`、`preferredaddr`、`accesscore`、`childcount`、`corecount`、`observednodes` 等字段。`observednodes` 仅在 bootstrap 上汇总：bootstrap 自身 + 直接选择它的普通/挖矿节点 + 在线 core + core 上报的子节点。

该值是有时间窗口的在线观测数。正常变化按心跳刷新；无断开通知的失联最多经历 60 秒声明过期和下一次上报。core 失联时其子树暂不可见；节点切换上级时也可能短暂重复或遗漏。`ValidatorId` 和上报数沿用现有信任方式，不构成身份认证、反女巫计数或共识依据。

端到端覆盖位于 `integration/contract_e2e/node_access_e2e_test.go`，使用真实节点、连接和数据库验证选择、重启复用首选、多配置连接不受影响、故障切换、bootstrap 重启、去重及过期；`TestNetworkPOSV2MultiCoreMultiMinerOffline` 同时验证多 core、多矿工的统计和正常出块。

连接生命周期用例等待真实的 10 分钟选择窗口，并验证显式配置连接在长时间停机后恢复，运行时使用 `go test ./integration/contract_e2e -run '^TestNetworkNodeAccessLifecycle$' -count=1 -timeout=18m -v`。`TestAccessCoreExit` 使用双方签名的真实退押交易，验证 core 仍保持 P2P 连接时的重新选择及持久化。

Relevant files:

- `wire/msgversion.go`
- `peer/peer.go`
- `server.go`
- `server_node_access.go`
- `wire/msgping.go`

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
