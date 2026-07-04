# DKVS Implementation Status

更新时间：2026-07-04

本文记录当前 SatoshiNet 内置 indexer DKVS 对 `DKVS_Requirements_and_Design_v0.6.md` 的实现状态。它是项目级实现说明，不替代对外协议文档。

## 已实现

- DKVS core record / key / validator / selector / store，使用 SatoshiNet indexer KVDB，数据目录为 indexer DB 下的 `dkvs` 子库。
- key namespace 校验：`/sys`、`/name`、`/svc`、`/personal`、`/mail`、`/blob`、`/tmp`，包含长度、segment 字符集、namespace shape 和 tombstone 基础规则；`/name` 按文档限定为 `/name/<name>` 单段；`/sys` 按 v1 文档限定为 `/sys/params`、`/sys/checkpoint/<epoch>`、`/sys/snapshot/<epoch>`、`/sys/miner/<miner_id>`、`/sys/pool/<pool_id>`。
- record 签名、TTL / expiry、record size、seq / expiry / hash 选择规则。
- record hash 反查索引随 active record 替换同步更新，避免同 key 旧 record hash 索引长期残留。
- `/personal/<account_id>/...` 使用 `sha256(pubkey)` owner 校验。
- `/name/<name>`、`/svc/<service_name>/...` 通过 `DIDResolver` 接口校验当前 signing key 或 owner p2tr address；默认 resolver 不可用，因此主网未配置 resolver 时默认不可写。已存在同 key record 后，后续同 pubkey 写入不再重复调用 resolver；pubkey 变更时才调用 resolver，并允许当前 owner 低 seq 覆盖旧 pubkey record。
- DKVS 提供本地 `NotifyNameTransfers` / `NotifyDKVSNameTransfers` 接口，供 indexer 在观察到一个区块内的 name 转移后通知 DKVS。通知会持久化 dirty 标记，使对应 `/name/<name>` 下次写入即使 pubkey 未变化也必须重新 resolver；resolver 校验通过后清除标记，即使 selector 最终判定为 no-op，resolver 失败则保留。
- DKVS 提供可选 `HTTPDIDResolver` 适配器，支持按 `GET /name/<name>`、`GET /service/<service>` 读取 direct 或 `{code,msg,data}` 包装的 DID identity JSON；identity 可返回 `signing_keys`，也可返回 `owner_addresses` / `address`。
- DKVS 提供 `L1NSResolver` 适配器，支持接入 L1 indexer `GET /ns/name/:name`，使用 `data.address` 作为 owner，按 record pubkey 派生 p2tr 地址并与 owner address 对比；SatoshiNet 节点启动时若配置了 L1 indexer host/proxy，会把该 endpoint 注入为 DKVS L1 NS resolver base URL。
- DKVS resolver / fee verifier / system verifier 支持初始化配置和运行期注入：`indexer.Config.DKVS`、`SetDKVSResolver`、`SetDKVSFeeVerifier`、`SetDKVSSystemVerifier`；运行期传入 nil 会恢复保守默认。嵌入式 indexer 配置层还支持 `ResolverL1NSBaseURL` / `ResolverHTTPBaseURL` / `FeeVerifierHTTPEndpoint` / `SystemVerifierHTTPEndpoint`，在未显式注入接口时自动构造 `L1NSResolver` / `HTTPDIDResolver` / `HTTPFeeVerifier` / `HTTPSystemVerifier`。
- `/mail/<mailbox_id>/msg/<msg_id>` 允许有效 signer 投递，受 mailbox msg quota / TTL / size 限制。
- `/mail/<mailbox_id>/share/<package_id>/<share_id>` 要求 mailbox owner 写入，受 share quota / TTL / size 限制。
- `/sys/*` 通过 `SystemVerifier` 授权；默认拒绝普通写入。
- `/tmp/<random_id>` 作为短期临时数据，要求非零 TTL，默认 TTL 上限 24 小时，受可配置单条 size 限制。
- `/blob/<object_id>/manifest` 与 `/blob/<object_id>/chunk/<index>`，包含 manifest、chunk hash、content hash、chunk count / size / total size 校验。
- DKVS fee proof 接口、JSON 结构校验器和 ONESHOT / LEASE / FREE_LOCAL / AUTOPAY proof 构造 helper；helper 会校验传入 namespace 与 key namespace 一致并规整必填字段；`fee_proof.record_hash` 使用 `FeeAnchorHash(record)`，避开 proof 内自引用和 signature 循环；支持可选 payer proof signature，签名覆盖 fee proof 规范字段且不包含 `proof_signature` 自身；AUTOPAY verifier 读取 `autopay.tc` 合约 state，校验 active、deployer/payer、recipient、fee asset、expiry 和按 full-size record 计算的容量。
- DKVS 提供可选 `HTTPFeeVerifier` 适配器，可把 `record_hash`、`key_hash`、namespace、record size、expiry height 和 raw fee proof 转发给外部 DKVS Pool verifier service；不默认启用，不定义合约语义。
- DKVS 提供可选 `HTTPSystemVerifier` 适配器，可把 `/sys/*` key 和 record signer pubkey 转发给外部 system authority service；不默认启用，不定义 system signer 治理语义。
- Checkpoint / snapshot 是未签名的本地计算结果，用于节点视图对账、调试和 snapshot 校验；embedded indexer 不持有 checkpoint/snapshot system signer 私钥，也不自动发布 signed `/sys/*` checkpoint record。
- DKVS 单测显式覆盖默认非免费策略下无 fee proof 写入失败，避免主网节点误开放免费写入；测试网默认策略使用 AUTOPAY verifier，主网不凭默认值放行，测试环境或本地策略仍可通过 `AllowFreeLocal` 或自定义 `FeeVerifier` 开启。
- 6 个原生 wire 消息：`dkvsnotify`、`dkvsinv`、`dkvsget`、`dkvsdata`、`dkvssyncreq`、`dkvssyncres`。
- peer listener 和 serverPeer DKVS 消息分发；miner 新连接后通过 sync request / response 分页同步 active records，完成同步时会对比远端 response checkpoint root 与本地 active root 并记录 mismatch。
- REST API：
  - `POST /v3/dkvs/records`
  - `GET /v3/dkvs/records?key=...`
  - `GET /v3/dkvs/records?hash=...`
  - `GET /v3/dkvs/records/prefix?prefix=...&start=...&limit=...`
  - `GET /v3/dkvs/usage?prefix=...`
  - `POST /v3/dkvs/tombstone`
  - `GET /v3/dkvs/checkpoint`
  - `GET /v3/dkvs/snapshot`
  - `POST /v3/dkvs/snapshot`
  - `POST /v3/dkvs/prune`
  - `POST /v3/dkvs/subscriptions`
  - `DELETE /v3/dkvs/subscriptions`
  - `GET /v3/dkvs/subscriptions`
- Checkpoint / snapshot 生成，snapshot import 会先校验 root / count / size / namespace roots，再逐条走 remote record 校验落库；SDK 提供 snapshot root 本地验证 helper。
- DKVS prefix usage 统计，返回指定 prefix 下当前 active record 数量和总 record size，可用于 `/personal`、`/svc`、mailbox、blob 等命名空间的本地用量展示和 quota 辅助判断。
- 非共识 checkpoint / snapshot 通过现有 API 导出/导入，不塞入单条 signed DKVS record，也不自动对接 anchor 服务。
- 普通节点本地 subscription 状态和 notify 过滤；非 miner 只保存订阅范围内的 record。
- 普通节点如果本地已有 DKVS subscription，连接 miner peer 时会用现有 `MsgDKVSSyncRequest/Response` 拉取当前 active records，并在本地按 subscription 过滤落库；不扩展 wire 协议。
- 普通节点运行中新增 DKVS subscription 后，会通过 server callback 对已连接 miner peers 发送现有 `MsgDKVSSyncRequest`，收到 response 后仍按本地 subscription 过滤落库；重复订阅不会重复触发远端 sync；不扩展 wire 协议。
- `MsgDKVSSyncRequest` 兼容追加可选 subscription filters，新 miner 会按 key / prefix / mailbox / service 过滤 sync response；旧格式 payload 仍可解码。主动发起请求时如果 filters 会使 payload 超过旧节点 `MsgDKVSSyncRequest` 上限，则退回旧格式全量 sync，由普通节点本地过滤落库，避免影响旧主网节点。
- DKVS 包内集成测试覆盖普通节点按 exact key 和 prefix 订阅后先用 filtered sync 拉取当前数据、过滤掉未订阅 key/prefix，再通过 notify/get/data 模拟拉取 prefix 下新增 record。
- DKVS 包内集成测试覆盖普通节点订阅 `/mail/<mailbox_id>` 后先用 filtered sync 拉取 mailbox 当前数据、过滤掉其他 mailbox，再通过 notify/get/data 模拟拉取新增 mailbox message。
- DKVS 包内集成测试覆盖普通节点订阅 `/svc/<service_name>` 后先用 filtered sync 拉取 service 当前数据、过滤掉其他 service，再通过 notify/get/data 模拟拉取新增 service record；权限仍走 `DIDResolver`，默认未配置 resolver 时 `/svc` 不开放。
- 过期 record 手动 prune 和 indexer 低频自动 prune。
- DKVS Go helper / SDK-style builder：record signing、tombstone、renewal record、fee proof 构造、personal/name/service/mail/blob/tmp key builder、单条 record 本地验证、prefix record set 本地验证、subscription record set 本地验证、blob manifest/chunk record builder、blob manifest 解析、chunk hash 校验和 blob 内容拼接。
- `sat20wallet/sdk` 新增 SatoshiNet DKVS REST client，覆盖 records、record hash 精确读取、verified record get、verified record hash get、verified prefix list、signed put、signed tombstone、signed renewal、personal record 读写删除续费、prefix usage、key/prefix 订阅、verified subscription initial records、checkpoint、verified snapshot export、snapshot import、prune、subscriptions、mailbox message/share 读写删除订阅、mailbox account_id 创建、signed mailbox message、deleteMessage、blob records 写入、putBlob、putChunkedBlob、getBlob / getChunkedBlob 读取校验拼接、name record 读写、record 级 `ResolveNameRecord(name)`、signed name record、service record 读写列表订阅、signed service record。
- `sat20wallet/sdk` 新增 DKVS 应用级 Go helper，覆盖 wallet recovery encrypted backup、wallet recovery renewal、guardian mailbox share、offline IM message、service authenticity record 的 record 构造、写入、读取和基础 value 校验；这些 helper 只组合现有 DKVS REST client，不新增 wire command 或链语义。
- `sat20wallet/sdk` 新增可执行 Go examples，覆盖钱包恢复、Guardian mailbox、离线 IM、服务正版检测和 record 级 name 解析样本；示例使用 fake HTTP，不访问外网，不依赖真实 DID resolver。
- `docs/dkvs-pwa-api-examples.md` 新增 PWA / dApp REST API 调用样本，覆盖 put/get/tombstone、prefix list、usage、subscription、mailbox、blob、checkpoint/snapshot、name/service record 读取；样本明确 record 签名和 fee proof 应由钱包/SDK 完成，不把 REST 样本等同于前端 UI。
- `sat20wallet/pwa` 新增 DKVS 开发者工具页 `/wallet/dkvs`，并在 Tools 页面提供入口；UI 支持按 key/hash 查询、prefix 列表、提交已签名 record、提交 tombstone、读取 checkpoint。页面只调用现有 REST API，不在前端臆造 DID、fee proof 或 record 签名逻辑。
- `docs/dkvs-external-integration-contracts.md` 新增外部集成契约，明确真实 Ordinals DID resolver、DKVS Pool fee verifier 和 `/sys` system verifier 的接口、输入输出、失败模式和主网默认关闭边界。
- `docs/dkvs-open-decisions.md` 新增剩余开放决策清单，列出 DID resolver、DKVS Pool、checkpoint anchor、PWA UI 的待确认问题、推荐最小接入和代码接入点。
- `docs/dkvs-requirements-traceability.md` 新增需求追踪矩阵，按总体目标、迁移实现、单元测试、集成测试和开发阶段逐项标注 Done / Partial / Blocked，并列出证据和外部输入需求。
- Notify event 常量覆盖文档中的事件类型；mailbox message 写入使用 `MAILBOX_MESSAGE`，tombstone 使用 `RECORD_TOMBSTONE`，同 key / 同 seq / 同 owner / 同 value 的 expiry 延长写入使用 `RENEWAL`，过期 prune 删除 record 后发送 `EXPIRED`。
- DKVS core 提供 `NotifyEvent` 构造、JSON encode/decode helper；事件字段包含 event_type、key、key_hash、record_hash、seq、expiry_height、size、source_node、flags。现有本地 notify 回调复用同一构造逻辑，wire 层仍使用 `dkvsnotify` 原生命令，不新增协议。

## 保持主网兼容的边界

- 未引入 `github.com/libp2p/*`。
- 未修改交易、区块、签名、共识、mempool、mining 或 txscript 语义。
- 未修改 `go.mod` / `go.sum`。
- `/name`、`/svc`、`/sys` 在未注入真实 resolver / verifier 前仍默认关闭。
- DKVS wire command 保持当前 6 个原生命令；`MsgDKVSSyncRequest` 只追加兼容的可选 filters 字段，且主动发送时保持旧 payload 上限兼容。

## 已验证

- `go test ./wire ./peer ./indexer/indexer ./indexer/indexer/dkvs ./indexer/rpcserver/indexer ./indexer/share/indexer`
- `go test ./...`
- `go test ./indexer/indexer/dkvs -run '^TestNotifyEventEncoding$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^TestDefaultFeeVerifierRejectsMissingProof$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^TestOrdinaryNodeKeyAndPrefixSubscriptionSyncAndNotify$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^TestOrdinaryNodeMailboxSubscriptionSyncAndNotify$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^TestOrdinaryNodeServiceSubscriptionSyncAndNotify$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^(TestL1NSResolverUsesOwnerAddress|TestExistingNameRecordSkipsResolverForSamePubKey|TestNameOwnerRotationAllowsLowerSeqReplacement|TestServiceOwnerRotationAllowsLowerSeqReplacement|TestNameTombstonePermissionAllowsExistingOwnerAndNewOwnerReplace)$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^(TestNameTransferNotifyForcesNextNameResolve|TestNameTransferNotifyPersistsDirtyMarker|TestExistingNameRecordSkipsResolverForSamePubKey)$' -count=1`
- `go test ./indexer/indexer/dkvs -run '^TestHTTPSystemVerifier$' -count=1`
- `go test ./indexer/indexer -run '^(TestPublishDKVSSystemRecordsOnBlock|TestDKVSConfigMergesExternalIntegrations|TestPruneExpiredDKVSOnBlock)$' -count=1`
- `go test ./indexer/indexer -run '^TestDKVSConfigMergesExternalIntegrations$' -count=1`
- `sat20wallet/sdk`: `go test ./wallet -run '^TestSatsNetDKVSClient' -count=1`
- `sat20wallet/sdk`: `go test ./... -run '^$'`
- `sat20wallet/pwa`: `npm run compile`
- `sat20wallet/pwa`: `npm run build:skip-check`
- `rg "github.com/libp2p" go.mod go.sum indexer wire peer server.go` 无命中。
- `git diff -- go.mod go.sum blockchain mempool mining txscript chaincfg` 无输出。

说明：`sat20wallet/sdk` 的完整 `go test ./wallet` 当前会运行既有网络型测试并访问 `apiprd.sat20.org`，本轮在多次 EOF / TLS handshake timeout 后手动中断；新增 DKVS client 测试已用精确 `-run` 验证，全包用 `-run '^$'` 做编译检查。

## 未闭环，需要规格或跨模块设计

开放决策清单见 `docs/dkvs-open-decisions.md`。

### Ordinals DID resolver

当前已实现 resolver 接口、L1 `/ns/name/:name` owner-address resolver、record pubkey p2tr address 校验，以及本地 name transfer notify dirty 标记。仍需继续明确的规则：

- canonical name / name_id 的来源；
- DID active 状态判断；
- DKVS signing key 声明和轮换方式；
- service name 与系统预置 service key 的来源；
- owner 变化与历史 record 失效的最终协议确认。

这些信息应来自 L1 Ordinals DID / SNS / referrer 等索引状态或新的 DID 协议字段，不能在 SatoshiNet DKVS 内部臆造。

当前已在 `docs/dkvs-external-integration-contracts.md` 固化 resolver 接口契约：`ResolveName` / `ResolveService` 必须返回 canonical name、name_id、当前 signing keys 或 owner addresses 和 active 状态；resolver 不可用或 DID 缺失必须保持拒绝写入。

本轮核查过当前 SatoshiNet referrer / referree 数据：`ReferrerInfo` 只有 `Name` 和 `BindBlock`，绑定逻辑是“地址绑定推荐人名字”，没有 DID owner、active 状态、DKVS signing key 或 key rotation 语义，因此不能安全接成 `/name` / `/svc` 的默认 resolver。

### DKVS Pool 合约与真实 fee proof

当前已接入 AUTOPAY template contract verifier：record 提交 AUTOPAY proof 后，节点通过 `getcontractstate` 检查 `autopay.tc` 是否 active、deployer 是否为 payer、recipient / fee asset / expiry 是否符合策略，并按每区块 payment 换算可持有 full-size record 数量。仍缺：

- 主网 DKVS AUTOPAY 合约地址、deployer、recipient、fee asset 与 full record fee 参数；
- 普通用户部署/续费 AUTOPAY 合约的产品流程和钱包 UX；
- miner 收益分配规则。

在这些规格明确前，不能把测试网默认参数宣称为主网费用策略。

当前已在 `docs/dkvs-external-integration-contracts.md` 固化 fee verifier 接口契约：AUTOPAY verifier 校验 template contract state 和容量；ONESHOT / LEASE 仍作为后续 Pool proof 语义保留；`JSONFeeVerifier` 仍只作为结构化本地 verifier。

### 普通节点远端订阅同步边界

当前已用 `MsgDKVSSyncRequest` 可选 filters 支持远端 key / prefix / mailbox / service 过滤同步。为了兼容旧节点，主动发送时如果 filters 使 payload 超过旧上限，会退回旧格式全量同步，本地再过滤落库。因此新 miner 之间可获得较小 response；遇到旧 miner 或过滤条件过多时仍会产生一次全量扫描 / 响应，但不会破坏旧 wire command。

### SDK 和应用样本

当前已有 SatoshiNet DKVS 包内 Go helper，`sat20wallet/sdk` 里的 REST client、signed put/tombstone、personal、key/prefix 订阅、mailbox/blob/name/service record 级便捷封装，以及 Go SDK 应用级 helper：wallet recovery encrypted backup、guardian mailbox share、offline IM message、service authenticity record。PWA 侧已有 DKVS 开发者工具页，覆盖 key/hash 查询、prefix 列表、signed record/tombstone 提交和 checkpoint 读取。

真实三节点 e2e 已使用 mnemonic 派生的 key-path p2tr actor 部署 AUTOPAY 合约，写入 `/name/8888.btc`，再模拟 L1 name 转移并由新 owner 低 seq 覆盖旧 owner record，验证 core/miner/bootstrap 收敛。

仍未闭环的是真实 Ordinals DID name 主网解析样本和主网 AUTOPAY fee proof 写入样本。SDK 已提供 record 级 `ResolveNameRecord(name)` 和可执行 Go example，会返回 canonical input、`NormalizeNameID(name)` 和 `/name/<name_id>` DKVS record；真实 DID owner / signing key / active 状态解析仍依赖 Ordinals DID resolver 规格，不在 SDK/PWA 内臆造。

### Checkpoint 链上锚定

当前已实现非共识 checkpoint / snapshot API，用于节点视图对账、调试和 snapshot 校验。checkpoint / snapshot 是未签名的本地计算结果，不作为 `/sys/*` signed DKVS record 发布，embedded indexer 也不持有 checkpoint 签名私钥。仍未实现真实链上 anchor；anchor 交易或系统合约格式、epoch 规则、发布者权限和失败处理规格仍需单独设计。
