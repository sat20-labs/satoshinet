# DKVS Requirements Traceability

更新时间：2026-07-04

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
| G5 | 费用证明 | Partial | `FeeVerifier`、`JSONFeeVerifier`、ONESHOT/LEASE/FREE_LOCAL/AUTOPAY helper、AUTOPAY template contract verifier 已接入；主网费用参数和收益策略仍待定。 |
| G6 | miner 全量同步 | Done | `MsgDKVSSyncRequest/Response`、serverPeer sync、包内三 miner startup sync 测试。 |
| G7 | 普通节点按 key/prefix/mailbox/service 订阅 | Done | subscription set、filtered sync、notify filtering；key/prefix/mailbox/service 集成测试。 |
| G8 | mailbox 与离线 IM | Done | `/mail/msg`、`/mail/share` 权限/quota/TTL/size；SDK app helpers 和 examples。 |
| G9 | 小型 blob / chunk | Done | owner-scoped named object key、manifest-first、same-generation chunk、chunk/content hash/size/count 校验；SDK blob helper。 |
| G10 | 预定义 namespace | Done | `/sys`、`/name`、`/svc`、`/personal`、`/mail`、`/blob`、`/tmp` parser/validator；`/sys` v1 子路径收紧到 params/checkpoint/snapshot/miner/pool。 |
| G11 | Ordinals DID name/service 权限解析 | Partial | `DIDResolver` 契约、L1 `GET /ns/name/:name` owner-address resolver、p2tr pubkey->address 校验、static resolver tests；ascended-sat owner 和 service 生产规则仍待细化。 |
| G12 | DKVS Pool 合约收费并向 miner 分配收益 | Partial | AUTOPAY verifier 已校验全局 template、service、recipient、fee asset，以及每个 p2tr delegate 的 active、余额和独立容量；主网合约参数和 miner 收益分配规则仍待定。 |
| G13 | `MsgDKVSNotify` 通知 | Done | wire command、peer listener、serverPeer notify/get/data flow、notify event helper。 |
| G14 | checkpoint / snapshot | Done | 实现未签名的非共识 checkpoint/snapshot API 和同步视图比较；按当前决策不签名、不发布 DKVS system record、不做链上 anchor。 |
| G15 | SDK / API 给 PWA、钱包、dApp、本地 Agent 使用 | Partial | REST API、record hash 精确读取、Go SDK client/helpers/examples、verified get/list/subscribe、本地 record set 验证、PWA REST API doc、PWA DKVS developer tool；缺真实 DID/Pool 生产样本。 |

## 迁移实现

| ID | Requirement | Status | Evidence / Notes |
| --- | --- | --- | --- |
| M1 | 接入聪网底层 P2P DKVS 消息 | Done | 6 个 native wire commands、peer switch/listener、serverPeer handlers。 |
| M2 | 实现 namespace validator | Done | `ParseKey` / `ParsePrefix` / namespace shape tests。 |
| M3 | 接入 Ordinals DID resolver | Partial | L1 NS HTTP resolver and p2tr owner-address verification done; ascended-sat owner resolver and service production mapping remain. |
| M4 | 实现 fee proof | Partial | Structured proof and AUTOPAY proof implemented; ONESHOT/LEASE production semantics remain future work. |
| M5 | 接入 DKVS Pool 合约校验 | Partial | AUTOPAY template contract state verifier implemented; mainnet contract parameters and revenue policy remain. |
| M6 | miner 全量同步 | Done | Sync request/response, pagination, checkpoint root compare. |
| M7 | 普通节点订阅 | Done | key/prefix/mailbox/service subscription and filtered sync tests. |
| M8 | `MsgDKVSNotify` | Done | Command, routing, notify/get/data convergence tests. |
| M9 | mailbox 容量控制 | Done | `MailboxPolicy`, full behavior, tombstone frees capacity. |
| M10 | blob chunk | Done | Manifest/chunk validation and SDK assembly. |
| M11 | checkpoint / snapshot | Done | Local non-consensus computed views implemented; signing, auto publish, external handoff and chain anchor are intentionally excluded. |

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
| I1 | 三个 miner 全量同步 DKVS | Done | `TestThreeMinerNotifyAndStartupSyncConverge` and wallet e2e `TestRealSatoshiNetDKVSAutopayNameSync`. |
| I2 | 普通节点同步链数据 | Done | Ordinary node subscribed filtered sync tests. Chain data sync is existing SatoshiNet behavior, not changed by DKVS. |
| I3 | 普通节点订阅 `/mail/<mailbox_id>` | Done | `TestOrdinaryNodeMailboxSubscriptionSyncAndNotify`. |
| I4 | miner 重启后从 peers 同步 DKVS | Done | Startup sync path covered by new miner sync test. |
| I5 | DKVS put 后所有 miner 收敛 | Done | notify/get/data convergence in three miner test and real wallet AUTOPAY/name e2e. |
| I6 | mailbox 满后发送失败 | Done | mailbox quota tests. |
| I7 | recovery 数据长期保存和续费 | Done | SDK app helper and renewal tests/examples. |
| I8 | 临时数据过期清理 | Done | tmp policy and prune tests. |
| I9 | 无 fee proof 写入失败 | Done | `TestDefaultFeeVerifierRejectsMissingProof`. |
| I10 | checkpoint 生成和比较 | Done | checkpoint/snapshot tests and sync response root compare. |
| I11 | blob 分片写入和读取 | Done | DKVS blob tests and SDK blob tests. |
| I12 | service namespace owner 权限校验 | Done | resolver/static service permission and service subscription tests. |
| I13 | Ordinals DID owner 更新后新 owner 可写 `/name` 和 `/svc` | Done | Existing same-pubkey updates skip resolver unless local name-transfer notify marked the name dirty; pubkey changes resolve current owner and can replace lower seq. Covered by name/service owner replacement, L1 NS owner-address, and name-transfer notify tests. |
| I14 | 普通节点先拉取再通过 notify 自动同步 | Done | key/prefix/mailbox/service sync-and-notify tests. |

## 阶段建议

| Phase | Requirement | Status | Notes |
| --- | --- | --- | --- |
| 1 | DKVS 核心迁移 | Done | Core record/store/validator/selector/P2P/local DB implemented. |
| 2 | Key、Name 与权限 | Partial | Namespace/personal/mail/name/service contract done; real Ordinals DID resolver blocked. |
| 3 | 同步 | Done | Miner full sync, notify, ordinary subscriptions, checkpoint/snapshot implemented. |
| 4 | 费用 | Partial | Proof format, verifier interface and AUTOPAY template contract verifier done; mainnet AUTOPAY parameters/revenue policy and ONESHOT/LEASE production rules remain. Expiry/renewal done. |
| 5 | Mailbox / Blob | Done | Mailbox quota, blob chunk, SDK helpers implemented. |
| 6 | 应用样本 | Partial | Go SDK helpers/examples, REST API examples, PWA DKVS developer tool and wallet AUTOPAY/name e2e done; production DID/mainnet fee UX samples remain. |

## Blocked Items

| Item | Missing Input | Why DKVS Core Should Not Invent It |
| --- | --- | --- |
| Ascended-sat DID resolver and service mapping | L2 owner lookup for ascended name sat, service namespace mapping and any non-L1-NS override rules | L1 owner-address resolver is implemented; ascended-sat ownership and service production policy still need explicit rules. |
| Mainnet DKVS AUTOPAY / Pool policy | Mainnet AUTOPAY contract parameters, recipient, fee asset, full-record fee, product renewal flow, miner revenue distribution, and whether ONESHOT/LEASE are required | Testnet AUTOPAY verification is implemented; mainnet economics and non-AUTOPAY Pool semantics should not be invented inside DKVS core. |
| Checkpoint chain anchor | 当前明确不实现 | checkpoint 仅为未签名的本地 active-view 对账结果；不提供外部 anchor handoff，也不把局部存储视图冒充全局共识根。 |
| True DID/mainnet fee production samples | Real DID resolver data, mainnet AUTOPAY parameters and production fee UX | UI and SDK examples must not claim production identity/payment coverage before those external rules exist. |

## Current Safe Next Steps

1. Decide whether L2 ascended-sat ownership overrides L1 `/ns/name/:name`, and define service namespace production mapping.
2. Provide mainnet AUTOPAY parameters, revenue policy and any non-AUTOPAY Pool proof semantics.
3. Add production DID/mainnet fee examples after the real resolver and mainnet fee policy are available.

决策问题、推荐最小方案和代码接入点见 `docs/dkvs-open-decisions.md`。
