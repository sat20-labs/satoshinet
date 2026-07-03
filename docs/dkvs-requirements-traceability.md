# DKVS Requirements Traceability

更新时间：2026-07-03

本文按 `DKVS_Requirements_and_Design_v0.6.md` 逐项追踪当前实现证据。状态含义：

- `Done`: 当前代码、测试或文档已有可验证证据。
- `Partial`: 已有接口、占位或本地实现，但缺真实外部规格或生产集成。
- `Blocked`: 不能在 DKVS core 内部安全完成，需要外部协议、合约或跨模块决策。

## 总体目标

| ID | Requirement | Status | Evidence / Notes |
| --- | --- | --- | --- |
| G1 | 全网统一 key-value 小数据存储 | Done | `indexer/indexer/dkvs` store、REST API、P2P sync、local KVDB 子库。 |
| G2 | 固定 key 下 value 更新 | Done | `CompareRecords` selection、seq/expiry/hash 规则、更新替换 hash index。 |
| G3 | record 签名校验 | Done | `SigningHash` / `VerifySignature`，本地/远端写入均验证。 |
| G4 | TTL / expiry | Done | `IsExpired`、Get/List/Sync/Checkpoint active filtering、prune。 |
| G5 | 费用证明 | Partial | `FeeVerifier`、`JSONFeeVerifier`、ONESHOT/LEASE/FREE_LOCAL helper、缺真实 DKVS Pool 合约接入。 |
| G6 | miner 全量同步 | Done | `MsgDKVSSyncRequest/Response`、serverPeer sync、包内三 miner startup sync 测试。 |
| G7 | 普通节点按 key/prefix/mailbox/service 订阅 | Done | subscription set、filtered sync、notify filtering；key/prefix/mailbox/service 集成测试。 |
| G8 | mailbox 与离线 IM | Done | `/mail/msg`、`/mail/share` 权限/quota/TTL/size；SDK app helpers 和 examples。 |
| G9 | 小型 blob / chunk | Done | manifest/chunk key、chunk hash/content hash/size/chunk count 校验；SDK blob helper。 |
| G10 | 预定义 namespace | Done | `/sys`、`/name`、`/svc`、`/personal`、`/mail`、`/blob`、`/tmp` parser/validator；`/sys` v1 子路径收紧到 params/checkpoint/snapshot/miner/pool。 |
| G11 | Ordinals DID name/service 权限解析 | Partial | `DIDResolver` 契约、dynamic permission filtering、static resolver tests；缺真实 Ordinals DID 数据源。 |
| G12 | DKVS Pool 合约收费并向 miner 分配收益 | Blocked | `FeeVerifier` 契约已定义；缺 Pool 合约地址/ABI/state/proof/revenue 规则。 |
| G13 | `MsgDKVSNotify` 通知 | Done | wire command、peer listener、serverPeer notify/get/data flow、notify event helper。 |
| G14 | checkpoint / snapshot | Partial | 非共识 checkpoint/snapshot API、signed system records；缺自动发布和链上锚定。 |
| G15 | SDK / API 给 PWA、钱包、dApp、本地 Agent 使用 | Partial | REST API、record hash 精确读取、Go SDK client/helpers/examples、verified get/list/subscribe、本地 record set 验证、PWA REST API doc；缺真实 PWA/dApp UI 落点。 |

## 迁移实现

| ID | Requirement | Status | Evidence / Notes |
| --- | --- | --- | --- |
| M1 | 接入聪网底层 P2P DKVS 消息 | Done | 6 个 native wire commands、peer switch/listener、serverPeer handlers。 |
| M2 | 实现 namespace validator | Done | `ParseKey` / `ParsePrefix` / namespace shape tests。 |
| M3 | 接入 Ordinals DID resolver | Partial | Resolver interface and contract done; real L1 DID source blocked. |
| M4 | 实现 fee proof | Partial | Structured proof implemented; real Pool verification blocked. |
| M5 | 接入 DKVS Pool 合约校验 | Blocked | Needs contract spec and state query interface. |
| M6 | miner 全量同步 | Done | Sync request/response, pagination, checkpoint root compare. |
| M7 | 普通节点订阅 | Done | key/prefix/mailbox/service subscription and filtered sync tests. |
| M8 | `MsgDKVSNotify` | Done | Command, routing, notify/get/data convergence tests. |
| M9 | mailbox 容量控制 | Done | `MailboxPolicy`, full behavior, tombstone frees capacity. |
| M10 | blob chunk | Done | Manifest/chunk validation and SDK assembly. |
| M11 | checkpoint / snapshot | Partial | Local/non-consensus implementation done; chain anchor blocked. |

## 单元测试要求

| ID | Requirement | Status | Evidence / Notes |
| --- | --- | --- | --- |
| U1 | key parser | Done | `TestParseKey` and namespace-specific key tests. |
| U2 | namespace validator | Done | namespace shape validation tests. |
| U3 | key 长度限制 | Done | parser/edge tests in DKVS package and wire payload tests. |
| U4 | segment 长度限制 | Done | parser tests. |
| U5 | DID name resolver 校验 | Done | default resolver closed and static resolver tests. |
| U6 | service name 存在性校验 | Done | default service resolver unavailable and static resolver tests. |
| U7 | `/name/<name>` 写入权限 | Done | owner rotation and tombstone permission tests. |
| U8 | `/svc/<service_name>/...` 写入权限 | Done | service owner rotation and sync tests. |
| U9 | record 签名 | Done | signature verification and signed SDK tests. |
| U10 | record seq 选择 | Done | selection/update tests. |
| U11 | ttl 过期 | Done | expiry filtering and prune tests. |
| U12 | fee proof | Done | JSON proof builders/verifier/signature/default missing proof tests. |
| U13 | tombstone | Done | tombstone selection, API and mailbox tombstone tests. |
| U14 | mailbox full | Done | `TestMailboxQuotaAndTombstone`. |
| U15 | blob chunk hash | Done | manifest/chunk validation tests. |
| U16 | notify event 编码 | Done | `NotifyEvent` JSON encode/decode test. |
| U17 | personal key owner 校验 | Done | `/personal/<account_id>` owner tests. |

## 集成测试要求

| ID | Requirement | Status | Evidence / Notes |
| --- | --- | --- | --- |
| I1 | 三个 miner 全量同步 DKVS | Done | `TestThreeMinerNotifyAndStartupSyncConverge`. |
| I2 | 普通节点同步链数据 | Done | Ordinary node subscribed filtered sync tests. Chain data sync is existing SatoshiNet behavior, not changed by DKVS. |
| I3 | 普通节点订阅 `/mail/<mailbox_id>` | Done | `TestOrdinaryNodeMailboxSubscriptionSyncAndNotify`. |
| I4 | miner 重启后从 peers 同步 DKVS | Done | Startup sync path covered by new miner sync test. |
| I5 | DKVS put 后所有 miner 收敛 | Done | notify/get/data convergence in three miner test. |
| I6 | mailbox 满后发送失败 | Done | mailbox quota tests. |
| I7 | recovery 数据长期保存和续费 | Done | SDK app helper and renewal tests/examples. |
| I8 | 临时数据过期清理 | Done | tmp policy and prune tests. |
| I9 | 无 fee proof 写入失败 | Done | `TestDefaultFeeVerifierRejectsMissingProof`. |
| I10 | checkpoint 生成和比较 | Done | checkpoint/snapshot tests and sync response root compare. |
| I11 | blob 分片写入和读取 | Done | DKVS blob tests and SDK blob tests. |
| I12 | service namespace owner 权限校验 | Done | resolver/static service permission and service subscription tests. |
| I13 | Ordinals DID owner 更新后新 owner 可写 `/name` 和 `/svc` | Partial | Dynamic owner rotation tests done with static resolver; real Ordinals DID source blocked. |
| I14 | 普通节点先拉取再通过 notify 自动同步 | Done | key/prefix/mailbox/service sync-and-notify tests. |

## 阶段建议

| Phase | Requirement | Status | Notes |
| --- | --- | --- | --- |
| 1 | DKVS 核心迁移 | Done | Core record/store/validator/selector/P2P/local DB implemented. |
| 2 | Key、Name 与权限 | Partial | Namespace/personal/mail/name/service contract done; real Ordinals DID resolver blocked. |
| 3 | 同步 | Done | Miner full sync, notify, ordinary subscriptions, checkpoint/snapshot implemented. |
| 4 | 费用 | Partial | Proof format and verifier interface done; DKVS Pool contract blocked. Expiry/renewal done. |
| 5 | Mailbox / Blob | Done | Mailbox quota, blob chunk, SDK helpers implemented. |
| 6 | 应用样本 | Partial | Go SDK helpers/examples and REST API examples done; PWA/dApp UI and true DID sample blocked. |

## Blocked Items

| Item | Missing Input | Why DKVS Core Should Not Invent It |
| --- | --- | --- |
| Real Ordinals DID resolver | Canonical name source, active state, DKVS signing key declaration, service mapping, key rotation rules | Incorrect resolver would incorrectly grant or deny `/name` and `/svc` write permissions on mainnet. |
| Real DKVS Pool verifier | Contract address/ABI/state query, payment confirmation rules, lease quota rules, replay prevention, miner revenue distribution | JSON proof validation alone cannot prove payment or enforce revenue distribution. |
| Checkpoint chain anchor | Anchor transaction or system contract format, epoch cadence, signer/miner authority | Anchoring changes chain/system behavior and needs protocol design. |
| PWA/dApp UI sample | Product placement, wallet signing integration, UX flow and existing dirty PWA worktree decision | Current REST/SDK samples are safe; UI integration should not overwrite unrelated PWA changes. |

## Current Safe Next Steps

1. Provide or design the real Ordinals DID data model and resolver source.
2. Provide DKVS Pool contract ABI/state API and payment proof semantics.
3. Decide checkpoint anchor mechanism and system signer/miner authority.
4. Choose PWA/dApp UI location after reconciling existing PWA worktree changes.
