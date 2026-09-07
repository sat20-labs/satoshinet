# DKVS Implementation Status

更新时间：2026-08-06  
性质：非规范性实现状态；协议与行为以 [`dkvs-design.md`](./dkvs-design.md) 为准。

## 当前已实现

### DKVSRecord 与生命周期

- `Seq` 是单 key revision。
- record 已删除 `PathGeneration` 和独立 `ExpiryHeight`。
- `IssueHeight` 使用 SatoshiNet 可信区块高度。
- `TTL` 单位为区块；派生到期高度为 `IssueHeight + TTL`。
- `TTL=0` 表示没有固定 record 租期，用于 AUTOPAY。
- Unix 时间不参与 record 有效性、PathMeta 或 StateRoot。
- wire codec、签名 hash、record hash、renewal、expiry heap、fee usage 和客户端验证均已切换到当前格式。

### PathMeta、CAS 与同步

- PathMeta generation 由节点在提交 mutation/batch 时维护，不存入 record。
- StateRoot 覆盖网络可见 active records 和 delete floors。
- FREE_LOCAL 不进入 network PathMeta、snapshot 或 P2P。
- AUTOPAY 停止当前区块支付后，记录可保留本地 grace，但退出 network path view。
- 单 key CAS 和 batch-CAS 使用 expected PathMeta/record 条件。
- standalone notify 只触发 stale/path repair，不根据到达顺序推测 generation。
- 完整 path snapshot 会验证 record、fee、root、count、size、MinExpiryHeight 和 delete floors，并原子替换本地 path。
- snapshot 应用后恢复 AUTOPAY retention cache，并可向下游重新广播 active records。
- 同 peer 多 stale path FIFO 排队；支持去重、分页 cursor/session、请求超时、终态失败释放和延迟重试。

### Namespace 与存储策略

- 支持 `/sys`、`/name`、`/svc`、`/personal`、`/mail`、`/blob`、`/tmp`。
- `/personal`、`/blob` 使用 account owner 校验。
- `/name`、`/svc` 使用可注入 resolver。
- `/sys` 使用可注入 system verifier。
- mailbox message 按 sender 子 path append，receiver 可 tombstone。
- Blob 是单 record opaque value，当前硬上限 1 MiB。
- FREE_LOCAL 有有限 TTL、endpoint affinity 和本地 quota。
- AUTOPAY 按 signer/payer delegate、当前区块支付、余额和容量验证。

### P2P 与 RPC

- 原生 DKVS P2P notify/inventory/get/data/sync 消息已接入 peer/server。
- miner 可全量同步；普通节点按 key/prefix/mailbox/service subscription 同步。
- Wallet REST API 只暴露 config、record、key-state、batch-CAS、prefix status/snapshot/read；
  canonical path sync、checkpoint/snapshot 和节点 subscription 保持节点内部或 node-local 管理接口。
- P2P path repair 和 RPC path snapshot 共用确定性验证逻辑。

### Wallet SDK 本地副本

- `dkvsManager` 是 SDK 唯一 DKVS 协调层。
- Wallet 启动时注册 path、启动 worker，并主动执行首轮同步。
- managed prefix 启动时完整 snapshot，之后默认每分钟比较 endpoint-local generation；只重拉变化 prefix。
- FREE_LOCAL 参与同一套本端 generation/status/snapshot，唯一差异是不进入 P2P relay。
- unmanaged key/prefix 按需直读，使用 5 秒超时和 1 分钟 endpoint-scoped 内存缓存。
- 服务端无 Wallet session、cursor、change log 或 watcher。
- 完整同步成功后按 prefix 原子替换 confirmed replica；全部目标 prefix 完成后恢复 ready。
- read/write 同时检查当前 session ready 和持久化 prefix generation 状态。
- prepared/inflight/conflict/error/stale 或首轮同步未完成时 fail-closed。
- 写入前使用最新本地 confirmed value；CAS 冲突后直读相关 key、重新执行业务 builder、重签并最多重试 3 次。
- 旧单-record outbox 已删除；写入只使用 per-key CAS/batch-CAS，不使用 path write precondition。

### 账户管理统一托管数据

- 创建或导入第一个 mnemonic wallet 时，账户管理自动启用。
- `RecoveryConfigured` 与 `Active` 分离；未配置恢复材料时仍进行临时账户数据管理。
- 账户 catalog 枚举所有 mnemonic wallets 和全部启用 subaccounts。
- 通用 `AccountManagedDataProvider` 接口已实现，未来模块无需修改账户同步核心。
- provider payload 按 provider/scope 隔离并统一加密。
- account state 与 `account-managed-data` blob 通过 root account batch-CAS 写入。
- 无 delegate 时使用 FREE_LOCAL；激活 AUTOPAY 后统一改写为 TTL=0 并回读验证。
- provider import 先拒绝未知 provider，再全量 Validate，最后统一 Import。
- 多设备对不同 provider/scope 的修改支持三方合并；同 provider/scope 冲突 fail-closed。
- wallet/subaccount catalog 变化会标记 managed data dirty；移除 scope 会删除对应 provider items。

### RGB11 接入

- RGB11 已注册为内置 account-managed provider：`rgb11`。
- 独立 RGB permanent head/snapshot、auto-backup policy、activation API 和迁移命令已删除。
- RGB 状态变更只通知账户管理 provider dirty，不直接写永久 DKVS。
- 最小恢复包仅包含：当前 allocation proof、最小 carrier、必要对象/receipt、未终结发送/接收、active receive request 和 reservation。
- 余额、完成历史、ticker 展示、scan/confirmation/raw tx 等派生缓存不进入账户托管数据。
- 空 RGB scope 不产生 payload；缺失 payload 在导入时权威清理旧本地 RGB 状态。
- RGB delivery、ACK/NACK、mailbox relay 仍使用有限 TTL FREE_LOCAL DKVS 瞬态记录，不使用 AUTOPAY。
- RGB Direct capability 合并进主账户唯一免费的 `/account/<network>/<root-address>` 服务描述符；该全网复制记录同时承载 AccountID、CoreNode binding、capability bits 和有界协议 TLV。
- 子账户不创建 DKVS mapping/binding/capability/mailbox identity；所有子账户 RGB 恢复数据由主账户 provider 统一管理，L1 监控仍覆盖每个 RGB scope。

### PWA/WASM

- PWA 不直接管理 DKVS record、Seq、PathMeta 或 fee proof。
- 账户设置页显示账户管理始终启用，并单独显示恢复/AUTOPAY 配置状态。
- RGB 页面已移除独立备份状态、模式和 retention 展示。
- WASM 已移除 RGB 独立 activation 返回字段和瞬态 AUTOPAY 选项。
- transient RGB TTL 使用区块数。

## 当前验证

已执行并通过：

- SatoshiNet DKVS 全量测试；
- DKVS P2P path repair、timeout、queue、AUTOPAY retention 定向测试；
- Wallet `go test ./wallet/...`；
- Wallet SDK `go build ./...`；
- WASM `make all`；
- PWA `npm run compile`；
- PWA production bundle；
- Account Management AUTOPAY 本地三节点 E2E；
- Account Management 生命周期与多设备并发 E2E；
- RGB sender/receiver 最小恢复包跨设备恢复测试；
- 通用 provider 注册、两阶段 import、多 scope merge、removed scope 测试；
- 两钱包、五子账户 RGB provider catalog/export/authoritative clear 测试。

## 部署边界

当前开发格式不兼容旧数据库和旧节点：

- 必须同时升级 SatoshiNet 节点、Wallet SDK、WASM 和 PWA；
- 测试环境需清理旧 DKVS/Wallet/PWA 数据；
- 禁止新旧 record codec 或旧 RGB snapshot 机制混跑；
- 不提供迁移、fallback 或双写。

## 尚未闭环

- 主网 `/name`、`/svc` resolver 和 system verifier 治理参数；
- 主网 AUTOPAY 合约地址、fee asset、recipient 和 miner 收益规则；
- account-managed blob 的未来分片/扩容策略；当前使用一个 root-owned blob，硬上限受 Blob policy 约束；
- 逻辑删除中间 subaccount 的产品语义；当前派生 index 为 append-only，未来由账户 catalog 表达 deleted scope；
- RGB 跨多个本地 store 的通用 operation journal；现有 minimum recovery、reservation owner 和 reconciliation 已覆盖主要崩溃恢复，但生产前仍应做更多 failpoint 测试。

## 2026-08-06 FREE_LOCAL、Blob 与 Wallet SDK 收敛

- `GET /v3/dkvs/config` 已成为 wallet SDK 在线 FREE_LOCAL 保存期的唯一来源。账户管理、
  RGB delivery/ACK、普通 record、account record 和 Blob 在线写入均采用服务节点
  返回的 `max_ttl_blocks`；配置缺失、禁用或为零时拒绝写入。
- 账户首次自动激活不再写入 SDK 内置 TTL。同步前读取当前服务节点策略，并把变化后的
  TTL 提交到本地 profile；后续同步可按节点新策略续写。
- 通用 Blob codec 保持 opaque，不自动压缩任意 value。
- `account-managed-data` 在 AES-GCM 加密前自动尝试 zlib 压缩；仅在明文不小于 1 KiB 且
  至少节省 64 bytes 时采用，解压上限为 1 MiB，并兼容既有未压缩 envelope。
- 既有未压缩 envelope 在后续账户同步时只在压缩确有收益的情况下透明迁移一次；
  无收益的旧 envelope 和内容未变化的已压缩 envelope 均直接复用，避免无意义续写。
- 新增 `sdk/wallet/dkvs` 低层包。record、Blob、FREE_LOCAL policy、typed errors、应用 payload
  codec 和 confirmed replica persistence 已迁入；父级 wallet 保留 manager 协调和兼容 facade。
