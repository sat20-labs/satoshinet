# DKVS v1 整体设计

更新时间：2026-07-31  
适用仓库：`sat20-labs/satoshinet`、`sat20-labs/sat20wallet`  
文档性质：**DKVS 唯一规范性设计文档**

> 本文同时约束 SatoshiNet 节点中的 DKVS 存储与传播实现，以及
> `sat20wallet/sdk` 中的 `dkvsManager` 和领域模块接入方式。
>
> 其他 DKVS 文档只能记录实现状态、开放问题、外部接口或测试结果，不能定义与本文
> 冲突的协议和行为。发生冲突时以本文为准。

---

## 1. 定位与目标

DKVS 是面向 Bitcoin/SatoshiNet 应用的 **owner-controlled distributed KV**。
它不是通用分布式数据库，不解决任意多主并发写、跨账户事务或通用业务合并。

设计目标按优先级为：

1. **简单**：协议、状态和错误语义容易理解；
2. **可靠**：权限、签名、顺序和最终收敛规则明确；
3. **高性能**：不同 owner path 可并行处理，正常写入只访问一个目标节点；
4. **最终一致**：网络传播完成后，各节点对同一 path 得到相同有效状态。

核心前提：

- 普通 path 有唯一控制者；
- 正常情况下，同一个 owner path 同一时间只有一个 active writer；
- DKVS 支持不同账户通过不同节点并发写入；
- 同一账户在多个设备、多个进程或多个节点同时修改同一个 path，不属于 DKVS
  的正确性保证；
- 特殊共享 path 必须使用明确的 append/create-only 模型，不能退化为多人覆盖同一个
  mutable key。

当前仍处于开发测试阶段。本文直接定义最终 DKVS v1，不新增 v2，也不保留未发布旧
行为的兼容层、双写路径或旧协议分支。

---

## 2. 保证与非目标

### 2.1 DKVS 保证

DKVS v1 保证：

- namespace 和 path 权限正确；
- record 签名、身份、value 完整性和费用证明得到验证；
- owner key 正常更新满足连续 sequence；
- 同代异常冲突按确定性规则收敛；
- owner path 的 PathMeta generation 和 state root 可跨节点比较；
- 接收写入节点上的 CAS 和 batch-CAS 原子执行；
- relayable record 通过 P2P 最终传播；
- 删除状态不会因为旧 record 重放而复活；
- exact-record 重试具备幂等语义；
- 不同 owner path 的写入可以并行。

### 2.2 DKVS 不保证

DKVS v1 不保证：

- 同一账户多设备并发修改时，两个业务修改都被保留；
- 自动理解或合并钱包、RGB11、账户管理等领域状态；
- 跨账户事务；
- 跨节点线性一致事务；
- quorum、leader、BFT 或链上 commit certificate；
- CRDT 或任意多主数据库语义；
- 使用 `FREE_LOCAL` 数据完成跨节点、跨设备恢复。

违反单 active writer 约束造成的覆盖、分支或业务错误由应用层负责。

---

## 3. Path 与权限模型

### 3.1 Path 是一致性和并发控制单位

DKVS key 属于一个逻辑 path。PathMeta、写入串行化、generation 和同步比较都以 path
为单位，而不是以整个 DKVS 数据库为单位。

推荐的 v1 path 划分：

| Key | Logical path | 模式 |
| --- | --- | --- |
| `/personal/<account_id>/<module>/...` | `/personal/<account_id>/<module>` | OwnerExclusive |
| `/blob/<account_id>/<blob_key>` | 完整 blob key | OwnerExclusive |
| `/mail/<receiver>/msg/<sender>/<msg_id>` | `/mail/<receiver>/msg/<sender>` | SharedAppend |
| `/mail/<receiver>/share/...` | `/mail/<receiver>/share` | OwnerExclusive |
| `/name/<name>` | 完整 name key | AuthorityExclusive |
| `/svc/<service_name>/...` | `/svc/<service_name>` | AuthorityExclusive |
| `/sys/...` | 由 system policy 定义 | AuthorityExclusive |
| `/tmp/...`、`FREE_LOCAL` | 节点本地 scope | LocalOnly |

`/personal/<account_id>` 下按 module 划分 path，避免账户管理、RGB11 和其他业务因为
无关 key 更新而共享同一个 generation 和写锁。

### 3.2 PathMode

```go
type PathMode uint8

const (
    PathOwnerExclusive PathMode = iota
    PathAuthorityExclusive
    PathSharedAppend
    PathLocalOnly
)
```

#### OwnerExclusive

- path owner 由 `account_id` 等确定性字段决定；
- 只有 owner 可以创建、更新和删除；
- 正常情况下只有一个 active writer；
- 使用完整 PathMeta generation/root 同步规则。

#### AuthorityExclusive

- 当前写入者由 DID resolver、service resolver 或 system verifier 决定；
- 同一时刻仍只有一个有效控制者；
- owner 变更以外部权威状态为准，旧 owner record 不再具有写权限；
- PathMeta generation 不因 owner 变更重置。

#### SharedAppend

- 多个主体可以在同一业务 namespace 下创建不同的唯一 key；
- 不允许多个主体修改同一个普通 value；
- 每个并发写入者必须落在独立子 path，或使用 create-only key；
- mailbox message 是该模式的标准实现。

#### LocalOnly

- 数据仅存在于接收节点；
- 不进入 P2P、checkpoint、snapshot 或跨节点 PathMeta 比较；
- endpoint 必须固定；
- UI 不得显示为“已同步到网络”。

### 3.3 Mailbox 规则

标准 message key：

```text
/mail/<receiver_id>/msg/<sender_id>/<msg_id>
```

规则：

- `sender_id` 必须与创建 record 的 signer 匹配；
- message key 只能 create，sender 不能覆盖已有 message；
- `msg_id` 必须在 sender 子 path 内唯一；
- receiver 可以提交 tombstone 删除 message；
- 不同 sender 使用不同 logical path，可并行 append；
- 不存在允许多人更新的 `/mail/<receiver>/inbox` 聚合 mutable key。

---

## 4. Record 模型

### 4.1 基本字段

DKVSRecord v1 保留以下核心含义，并在最终 v1 格式中加入 `PathGeneration`：

```text
Version
Key
Value
PubKey / account identity
Seq
PathGeneration
IssueTime
TTL
ExpiryHeight
FeeProof
Flags
Signature
```

`PathGeneration` 是该 record 对所属 logical path 的 owner mutation revision。它由
`dkvsManager` 基于已确认 PathMeta 分配，并由 owner 签名覆盖。节点和 P2P 转发者不得
修改它。

record 签名必须覆盖所有影响 record 语义的字段。P2P 转发节点不得修改已签名字段。

### 4.2 Sequence

普通 owner key 的正常更新：

```text
new.Seq = current.Seq + 1
```

新建 key 使用协议规定的初始 sequence。节点在接收本地 RPC 写入时必须验证：

- key owner/authority；
- record 签名；
- `Seq` 连续；
- CAS/path generation 前置条件；
- tombstone/delete floor；
- fee、TTL、size 和 quota。

sequence 是单 key 的顺序，不是整个 path 的 generation。

### 4.3 IssueTime

`IssueTime` 用于相同 sequence 的异常冲突裁决。它不是全网可信时钟，也不用于代替
sequence。

为避免客户端本机时钟偏差，节点的 pathmeta/sync/write 响应必须返回：

```text
server_time_ms
```

`dkvsManager` 构造 record 时使用：

```text
IssueTime = max(server_time_ms, previous_issue_time + 1)
```

接收节点按自身时间检查未来偏差。record 一旦签名，IssueTime 在后续传播中保持不变。

同一账户违反单写者约束、分别使用不同节点时间写入时，节点时钟误差可能影响最终
winner；这是明确接受的应用层风险。

### 4.4 Deterministic record selection

同 key 多个候选 record 的最终比较顺序：

1. `Seq` 较大者优先，防止正常版本回退；
2. `Seq` 相同时：
   - 若 owner、value、flags 等业务内容相同，仅 retention 不同，则按更长有效期选择
     renewal；
   - 否则 `IssueTime` 较新者优先；
3. 仍相同时，以 `RecordHash` 的字节序确定 winner。

禁止使用 `ExpiryHeight`、付费额度或保存期限决定两个不同业务 value 的胜负。

该规则只保证最终确定性，不保证异常并发下的业务语义正确。

### 4.5 Delete state

删除使用签名 tombstone，并保留足以阻止旧 value 复活的 delete floor。

Path state root 必须覆盖：

- 当前 active records；
- 保留期内 tombstones；
- tombstone 压缩后的 delete floors。

完整 tombstone 被压缩为 delete floor 时，不应改变该 path 的有效状态语义。

---

## 5. 确定性 PathMeta

### 5.1 网络可比较字段

```text
PathMeta {
    Version
    Path
    Generation
    StateRoot
    ActiveRecords
    ActiveTotalSize
    MinExpiryHeight
    ViewHeight
}
```

以下内容属于节点本地运行状态，必须与网络 PathMeta 分离：

```text
UpdatedAt
LastSyncAt
LastSyncPeer
Dirty
LocalRetryState
```

本地时间、接收时间和节点自己的数据库维护状态不能参与跨节点 PathMeta 相等判断。

### 5.2 Generation 定义

`Generation` 是 path 已接受的最大 owner mutation revision：

```text
PathMeta.Generation = max(confirmed PathGeneration watermark)
```

规则：

- 每个真正改变 path 有效状态的 put、update、delete、renewal 使用
  `PathGeneration = current_generation + 1`；
- exact retry、重复 notify 和 selector no-op 不增加 generation；
- 一个 batch 内的 mutation 按 canonical key order 分配连续 PathGeneration；
- batch 返回最后一个 generation；
- 远端节点从 record 中读取已签名的 PathGeneration，不能按“自己收到了多少条 record”
  重新计数；
- full path sync 同时传输 generation watermark，避免最新 mutation 已过期或压缩后新节点
  丢失路径高水位。

generation 的目标是快速判断 path 是否有新的 owner mutation，不是数据库本地写次数。

### 5.3 StateRoot

`StateRoot` 是 path 当前有效状态的确定性摘要。v1 可继续使用高性能、可增量更新的
XOR accumulator：

```text
leaf = H("dkvs-path-leaf-v1" || key || effective_state_hash)
state_root = XOR(all leaves)
```

`effective_state_hash` 对 active record 使用 `RecordHash`，对 delete floor 使用其 canonical
hash。

该 root 用于同步检测和完整状态复算，不是 Merkle membership proof，也不是共识承诺。

### 5.4 Expiry 的确定性边界

为了使 relayable path 在相同链高度下得到相同状态：

- 网络传播和长期保存的数据使用 `ExpiryHeight` 或不设置过期；
- wall-clock `TTL` 只用于 `LocalOnly/FREE_LOCAL` 数据；
- relayable record 不依赖各节点本地 wall-clock 决定有效性；
- PathMeta 的 root 以 `ViewHeight` 为参照计算；只有处于同一链视图高度时才直接比较
  root。

record 因达到 `ExpiryHeight` 失效不增加 owner generation，但会改变相应高度下的
StateRoot。同步判断因此始终使用 `(Generation, StateRoot, ViewHeight)`，不能只比较
Generation。

### 5.5 比较规则

manager 或节点比较两个 PathMeta：

1. 先确认 path 和链视图兼容；
2. generation 较小的一方需要同步；
3. generation 相同且 root 相同，视为已同步；
4. generation 相同但 root 不同，执行完整 path reconciliation；
5. endpoint generation 小于客户端已经确认的 generation 时，该 endpoint 对此 path
   是 stale，不能接受新写入。

---

## 6. 节点写入协议

### 6.1 单 key 写入

请求至少包含：

```text
signed_record
expected_path_generation
expected_record_hash 或 expect_absent
```

处理顺序：

1. 在锁外解析 key、验证 record 结构、签名、owner、fee、size；
2. 获取 logical path 的写锁；
3. 读取当前 PathMeta、record 和 delete floor；
4. 校验 expected generation 和 record CAS；
5. 校验 sequence、quota、expiry 和 namespace policy；
6. 验证 record.PathGeneration 等于当前 generation + 1，并计算新 winner 和 state root；
7. 在一个本地 KVDB write batch 中提交 record、hash index、delete state 和 PathMeta；
8. 释放锁；
9. 返回 accepted record、最新 PathMeta 和 `server_time_ms`；
10. 在锁外异步发送 P2P notify。

写响应已经包含新的 record 和 PathMeta，wallet SDK 成功后不需要再执行一次完整目录刷新。

### 6.2 Batch-CAS

Batch-CAS 用于同一 owner 下多个 key 的本地原子提交，例如 RGB11 snapshot 与 head。

约束：

- mutation 必须属于同一个 owner/authority；
- 涉及多个 path 时，按 path 字符串排序获取锁，避免死锁；
- 每个 path 都带 expected generation；
- batch 内 key 按 canonical order 分配连续 generation；
- 全部校验成功后在一个本地 DB batch 中提交；
- 任一校验失败时 `applied=0`；
- 不支持 Alice 与 Bob 的跨账户事务；
- 只保证接收 RPC 节点的本地原子性，不宣称其他节点瞬时原子可见。

### 6.3 Idempotency

客户端必须重试完全相同的签名 record/batch bytes。

如果当前 active record hash 与请求完全相同，返回幂等成功：

```text
applied = 0
```

不得重复：

- 增加 generation；
- 更新 sequence；
- 消耗额外 quota；
- 发送重复业务通知。

如果只有 batch 的一部分已经存在，整批返回 conflict，不补写剩余 mutation。

---

## 7. P2P 传播与节点同步

### 7.1 增量 record 传播

P2P 继续传播完整签名 record，不需要引入 quorum、transaction certificate 或第二套
DKVS 协议。增量顺序由 record 中签名覆盖的 `PathGeneration` 表达。

远端节点收到 record 后：

- 完整验证 record、权限、fee、namespace 和 PathGeneration；
- `PathGeneration == local_generation + 1`：应用 record，更新 generation 并重算增量
  StateRoot；
- `PathGeneration <= local_generation`：按 exact hash/record selector 处理重复或异常同代
  冲突，不按接收次数增加 generation；
- `PathGeneration > local_generation + 1`：说明存在 gap，不直接应用并猜测中间 generation，
  标记 path stale 并发起 full path sync；
- 同 generation 最终状态 root 不一致：执行完整 reconciliation；
- `FREE_LOCAL` record 一律不进入该流程。

现有 `dkvsnotify/dkvsdata/dkvssyncres` 只需携带最终 v1 `DKVSRecord`；不新增 PathUpdate
事务消息。开发阶段直接更新 v1 record codec 和签名域，不保留旧 record 格式兼容分支。

### 7.2 完整 path sync

完整同步返回：

```text
PathMeta
active records
retained tombstones / delete floors
server_time_ms
```

接收方必须：

1. 校验所有 key 都属于目标 path；
2. 验证所有 record；
3. 对同 key 候选执行确定性 selector；
4. 重算 StateRoot、count 和 size；
5. 与远端 PathMeta 比较；
6. 在一个本地 DB batch 中替换该 path 的 confirmed state；
7. 仅在原子替换成功后解除 stale 状态。

P2P 和 RPC 可以共用相同的 path snapshot 验证逻辑。

---

## 8. RPC 接口契约

建议统一为 path-oriented API：

```text
GET  /v3/dkvs/pathmeta?path=...
POST /v3/dkvs/sync/path
POST /v3/dkvs/watch/path
POST /v3/dkvs/records/cas
POST /v3/dkvs/records/batch-cas
```

所有 pathmeta、sync 和 write 响应返回：

```text
server_time_ms
pathmeta
```

业务错误必须有稳定 machine-readable code，例如：

```text
DKVS_WRITE_CONFLICT
DKVS_STALE_GENERATION
DKVS_STALE_ENDPOINT
DKVS_PERMISSION_DENIED
DKVS_INVALID_SEQUENCE
DKVS_PATH_DIVERGED
DKVS_LOCAL_ONLY_ENDPOINT_MISMATCH
DKVS_QUOTA_EXCEEDED
```

Go SDK 将其映射为可通过 `errors.Is/As` 判断的 typed error。不得解析英文 `msg` 决定
业务分支。

---

## 9. SatoshiNet 节点实现要求

### 9.1 并发模型

为满足不同账户高并发：

- 解析、签名、fee 等不依赖可变状态的检查在锁外完成；
- 使用 per-path lock 或固定数量的 striped path locks；
- 不使用一个全局 DKVS 写锁串行所有账户；
- batch 多 path 加锁按 canonical path order；
- DB commit 临界区保持最小；
- notify、watch 和日志在提交后异步执行。

### 9.2 Selector

节点 selector 必须调整为本文规定的：

```text
Seq -> renewal special case / IssueTime -> RecordHash
```

现有“同 Seq 先比较 ExpiryHeight”的规则只能用于相同业务内容的 renewal，不能用于
不同 value。

### 9.3 PathMeta

节点必须：

- 把 network-comparable PathMeta 与 local status 分开存储；
- 最终 v1 record 携带签名覆盖的 PathGeneration；
- gap 时同步，不按本地接收次数自增；
- full sync 后以经过验证的远端 generation/root 建立本地状态；
- exact retry/no-op 不增加 generation；
- 将 delete floor 纳入 StateRoot；
- 将 relayable TTL 收敛到 height-based expiry。

### 9.4 FREE_LOCAL

- 不 relay；
- 不进入 miner/普通节点 mirror；
- 不进入 network PathMeta、checkpoint 或 snapshot；
- 只受接收节点 policy 和本地清理器管理；
- API 明确返回 `local_only=true` 和 endpoint identity。

---

## 10. Wallet SDK `dkvsManager`

### 10.1 唯一入口

`dkvsManager` 是 SDK 内唯一 DKVS 协调层。RGB11、账户管理和其他领域模块不能持有
transport client，也不能自行管理 sequence、generation、root、outbox 或 sync worker。

PWA JavaScript 层不感知 DKVS：只调用领域级 WASM/API，不直接调用 `/v3/dkvs`，不
处理 record 或冲突。

### 10.2 内部结构

```text
dkvsManager
├── PathPolicyRegistry
├── EndpointRouter
├── PerPathActor / PerPathLock
├── ConfirmedReplica
├── PathStateStore
├── SimpleOutbox
├── SyncWorker
└── ChangeNotifier
```

### 10.3 PathPolicyRegistry

每个领域模块注册：

```text
logical path
owner id
path mode
key/value codec
fee mode
size limit
local-only policy
```

manager 只执行通用 DKVS 规则，不调用 RGB11 或账户管理专用恢复逻辑。

### 10.4 Endpoint affinity

一个 account 的 active write session 固定到一个 endpoint。

允许切换 endpoint，但必须满足：

1. 当前没有未确认 outbox 写入；
2. 新 endpoint 对所有受管 owner path 的 generation/root 不低于本地 confirmed state；
3. 完成 path sync 后才恢复写入。

这不是分布式 lease，只是应用层避免同账户跨节点分叉的简单约束。

### 10.5 读路径

区分：

- **Local read**：正常钱包 UI 和本地业务立即读取本地领域数据库；
- **Synced read**：恢复、备份校验或明确要求网络最新状态时，等待对应 path 完成
  generation/root 对账。

持久化 replica 可在启动时立即加载，但在完成当前 endpoint 对账前只能标记为 cached，
不能宣称 network-synced。

### 10.6 写路径

```text
1. 进入 per-path actor；
2. 确认 endpoint affinity；
3. 获取/校验 PathMeta；
4. 基于 confirmed record 分配 seq + 1；
5. 分配 `PathGeneration = confirmed_generation + 1`；
6. 使用响应中的 server_time_ms 生成单调 IssueTime；
7. 构造并签名 record；
8. 持久化 exact bytes 到 outbox；
9. 提交 CAS/batch-CAS；
10. 使用写响应原子更新 confirmed replica、PathMeta 和 outbox；
11. 在锁外通知领域模块。
```

领域 mutation builder 必须是纯函数。在远端提交确认前，不得修改不可回滚的钱包业务
状态或清除 pending mutation。

### 10.7 Outbox

Outbox 只解决网络中断和响应丢失，不承担分布式事务。

每项至少保存：

```text
path
exact signed record/batch bytes
record hash/batch hash
expected generation
created_at
retry_count
last_error
```

规则：

- 重试不重新生成 sequence、PathGeneration、IssueTime 或签名；
- active hash 完全相同才能判定已提交；
- 远端 generation 更高或出现不同 winner 时返回 stale/conflict；
- 不得因为“远端 record 排序更大”而静默删除本地 intent并冒充成功。

### 10.8 Session 状态

建议状态：

```text
CACHED
SYNCING
READY
WRITING
STALE_ENDPOINT
DIVERGED
LOCAL_ONLY
FAILED
```

领域层只看到业务化状态，例如“可写”“正在同步”“此设备只读”“备份仅保存在当前
节点”，不暴露 root、generation 和 transport 细节。

---

## 11. 领域接入规则

### 11.1 账户管理

- 使用 `/personal/<account_id>/account/...` owner path；
- 当前整体 state/snapshot 模型可以继续使用；
- 应用保证一个账户只有一个 active writer；
- 其他设备默认只读，或在完成显式 takeover 和同步后成为 writer；
- DKVS 不做多设备 operation merge；
- 如未来业务确实需要 merge，应在账户管理领域增加 operation log，而不是扩展 DKVS
  通用语义。

### 11.2 RGB11

- 使用独立 `/personal/<account_id>/rgb11/...` path；
- snapshot/blob 与 head 可使用同 owner batch-CAS；
- 应用保证同一 RGB11 wallet 只有一个 active writer；
- 其他设备在完成同步和显式切换前只读；
- DKVS 不判断两个 RGB11 wallet state 如何合并；
- 底层 UTXO/RGB 客户端验证仍是资产有效性的最终依据。

### 11.3 Blob

- 一个 blob key 对应一条完整 record；
- owner-exclusive；
- AUTOPAY blob 可以 relay；
- FREE_LOCAL blob 只能固定 endpoint 使用；
- blob 与 head 的 batch 只保证目标节点本地原子提交，远端节点通过 path sync 最终一致。

### 11.4 Mailbox

- message create-only；
- sender 子 path 并行；
- receiver tombstone 删除；
- 不构造需要所有 sender 共同覆盖的 inbox state；
- mailbox quota、TTL/expiry 和费用仍由 namespace policy 执行。

---

## 12. 性能原则

正常写入的目标成本：

- 一个 active endpoint；
- 一次 pathmeta/CAS 视图；
- 一次本地 DB batch；
- 一次写响应；
- 异步 P2P notify。

优化原则：

- per-path/striped lock，而非全局锁；
- 不同 account/module path 并行；
- 写响应直接携带新 PathMeta，避免写后全量 refresh；
- generation 相同且 root 相同则不下载目录；
- gap/divergence 才执行完整 path sync；
- batch 设置 mutation 数量和总 bytes 上限；
- P2P 按 payload bytes 分页；
- expensive signature/fee validation 尽量在锁外完成；
- root 使用可增量 accumulator。

不为了极少数不受支持的同账户多设备并发场景引入 quorum、leader、CRDT 或全局事务。

---

## 13. 测试与验收

### 13.1 SatoshiNet

必须覆盖：

- 不同 owner 通过不同节点并发写入并最终收敛；
- 同 owner 同 path 在单节点内 sequence 连续；
- same-seq 不同 value 按 IssueTime、RecordHash 收敛；
- renewal 不会因为 expiry 更长而覆盖不同 value；
- P2P notify 重复、乱序和 gap；
- gap 不会导致节点按本地接收次数错误增加 generation；
- generation 相同/root 不同触发 full reconciliation；
- full path sync 后 generation/root/records/delete floors 一致；
- batch-CAS 全成功或全失败；
- exact retry 不增加 generation；
- expiry height 下各节点 state root 一致；
- FREE_LOCAL 不 relay、不进入 network PathMeta；
- mailbox 不同 sender 并发 append；
- per-path 并发测试证明不同账户不会被全局写锁串行。

### 13.2 Sat20wallet SDK

必须覆盖：

- 领域模块无法获取 transport client；
- per-path 本地并发写严格串行；
- 不同 path 可并行；
- endpoint affinity；
- failover 前强制 PathMeta 对账；
- stale endpoint 拒绝写入；
- write response 直接更新 confirmed replica；
- exact outbox retry；
- 响应丢失后通过 active hash 确认结果；
- 远端不同 winner 不会被误判为 outbox success；
- local read 与 synced read 的状态边界；
- account/RGB11 的第二设备默认只读；
- FREE_LOCAL UI 不显示为网络备份成功；
- PWA 中不存在直接 DKVS REST、record、sequence、generation 管理逻辑。

### 13.3 明确不测试为 DKVS 保证

不把以下结果作为 DKVS 验收目标：

```text
同一账户两个设备同时写同一个 key，两个业务修改自动合并且均不丢失
```

相关测试只需证明节点最终按 selector 收敛，并明确记录其中一个业务 value 可能被覆盖。

---

## 14. 文档与代码收敛

### 14.1 唯一规范文档

唯一权威文件：

```text
satoshinet/docs/dkvs-design.md
```

以下旧的设计/优化文档应删除，或只保留不超过数行的迁移提示后删除：

```text
sat20wallet/docs/dkvs-manager-design.md
sat20wallet/docs/dkvs-review-optimization-plan.md
satoshinet/docs/dkvs-review-optimization-plan.md
```

`sat20wallet` 只在 README、package doc 或实现说明中链接到权威文件，不复制设计正文。

### 14.2 非规范性文档

以下文档可保留，但文件开头必须注明“非规范性，以 `dkvs-design.md` 为准”：

```text
dkvs-implementation-status.md
dkvs-open-decisions.md
dkvs-external-integration-contracts.md
dkvs-requirements-traceability.md
dkvs-pwa-api-examples.md
```

它们分别只记录：

- 当前实现状态；
- 未决产品问题；
- 外部接口契约；
- 需求追踪；
- API 使用样例。

不得在这些文件中重新定义 record selector、PathMeta、并发模型或 SDK 边界。

---

## 15. 最终原则

1. **普通 path 只有一个控制者。**
2. **不同账户可以高并发写，同一账户并发写由应用避免。**
3. **共享场景使用 append/create-only key，不共享 mutable key。**
4. **sequence 管单 key，generation 管 path。**
5. **PathGeneration 由 owner 签名，PathMeta 是确定性的网络状态；节点本地字段必须分离。**
6. **异常同代冲突按 IssueTime 和 RecordHash 收敛，但 DKVS 不保证业务正确。**
7. **CAS/batch-CAS 保证目标节点本地原子性，不包装成分布式事务。**
8. **wallet SDK 通过唯一 `dkvsManager` 管理 transport、replica、generation、outbox 和同步。**
9. **FREE_LOCAL 永远是节点本地语义。**
10. **不为不受支持的多设备多主场景牺牲系统的简单性、可靠性和性能。**
