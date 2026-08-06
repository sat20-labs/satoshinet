# DKVS 整体设计

更新时间：2026-08-06  
适用仓库：`sat20-labs/satoshinet`、`sat20-labs/sat20wallet`  
文档性质：**DKVS 唯一规范性设计文档**

> 本文直接描述当前开发协议。不使用版本分叉、旧格式 fallback、双写或迁移兼容。
> 其他文档只能记录实现状态、接口样例和未决产品问题；与本文冲突时，以本文为准。

---

## 1. 定位

DKVS 是面向 SatoshiNet 应用的小数据 owner-controlled distributed KV。它负责：

- key/path 权限；
- record 签名和完整性；
- 单 key revision；
- path 级 CAS、batch-CAS 和状态摘要；
- FREE_LOCAL 或 AUTOPAY 存储策略；
- 节点间传播、完整 path 修复和普通节点按需订阅；
- Wallet SDK 本地 confirmed replica 的持续同步。

DKVS 不负责：

- 理解账户、RGB 或其他业务 value；
- 合并同一账户多设备对同一 key 的并发业务修改；
- 跨账户事务；
- 分布式线性一致事务、quorum、BFT、CRDT 或 leader election。

同一账户在多个设备同时修改同一个 key 时，应用必须先同步最新 value，再基于最新 value 重算完整 mutation。CAS 冲突后重新同步和重试。DKVS 只保证协议状态确定性，不保证两个业务意图都被保留。

---

## 2. Key、Path 与权限

### 2.1 Logical path

PathMeta、写入串行化、CAS 和同步比较以 logical path 为单位。

| Key | Logical path | 模式 |
| --- | --- | --- |
| `/personal/<account_id>/<module>/...` | `/personal/<account_id>/<module>` | OwnerExclusive |
| `/blob/<account_id>/<blob_key>` | 完整 blob key | OwnerExclusive |
| `/mail/<receiver>/msg/<sender>/<msg_id>` | `/mail/<receiver>/msg/<sender>` | SharedAppend |
| `/mail/<receiver>/share/...` | `/mail/<receiver>/share` | OwnerExclusive |
| `/name/<name>` | 完整 name key | AuthorityExclusive |
| `/svc/<service_name>/...` | `/svc/<service_name>` | AuthorityExclusive |
| `/sys/...` | system policy 决定 | AuthorityExclusive |
| `/tmp/...`、FREE_LOCAL | endpoint-local scope | LocalOnly |

### 2.2 Path 模式

**OwnerExclusive**

- owner 由 account ID 等确定性字段推导；
- 只有 owner 可以创建、更新和删除；
- 应用避免同一时刻多个 active writer。

**AuthorityExclusive**

- 当前写入者由外部 resolver/verifier 决定；
- owner 变化不重置 PathMeta generation。

**SharedAppend**

- 多个主体只能创建不同的唯一 key；
- 不允许多人覆盖同一个 mutable value；
- mailbox message 按 sender 子 path 隔离。

**LocalOnly**

- 记录只存在于写入 endpoint；
- 不进入 P2P、network PathMeta、checkpoint 或跨节点 snapshot；
- 只能作为有限期缓存，不得宣称为网络备份。

---

## 3. DKVSRecord

### 3.1 字段

```go
type DKVSRecord struct {
    Version     uint32
    Key         string
    Value       []byte
    PubKey      []byte
    Signature   []byte
    Seq         uint64
    IssueHeight uint64
    TTL         uint64
    FeeProof    []byte
    Flags       uint32
}
```

record 中不存在：

```text
PathGeneration
ExpiryHeight
IssueTime / Unix time
```

record 签名覆盖全部协议字段。节点和转发者不得修改已签名内容。

### 3.2 Seq

`Seq` 只表示当前 key 的 value revision：

```text
新建 key：Seq = 1
更新 key：Seq = current.Seq + 1
删除 key：Seq = current.Seq + 1
```

同一路径中不同 key 的 `Seq` 互不相关。`Seq` 不承担 path revision 的职责。

相同 `Seq` 的完全相同 record 是幂等重试。异常同 `Seq` 候选按确定性 selector 收敛；应用不得把这种收敛理解成并发业务合并。

### 3.3 IssueHeight 与 TTL

协议时间统一使用 SatoshiNet 可信区块高度：

```text
expiry_height = IssueHeight + TTL
```

有效区间：

```text
[IssueHeight, IssueHeight + TTL)
```

规则：

- `TTL > 0`：有限区块租期；
- `TTL = 0`：record 没有固定租期，生命周期由 AUTOPAY 等外部策略决定；
- `IssueHeight + TTL` 溢出时 record 无效；
- Unix 时间只用于日志、请求超时和本地维护，不参与 record 有效性、StateRoot 或费用状态。

`ExpiryHeight` 只是在运行时由 `IssueHeight + TTL` 派生，不存入 record。

### 3.4 Selector

同 key 候选的确定性比较：

1. `Seq` 较大者优先；
2. 相同 `Seq` 且业务内容相同的续期，派生到期高度更晚者优先；
3. 其他相同 `Seq` 异常冲突按 `IssueHeight`；
4. 仍相同时按 `RecordHash` 字节序。

费用额度不能让不同业务 value 获胜。

### 3.5 Tombstone 与 DeleteFloor

删除使用 owner/receiver 签名的 tombstone。完整 tombstone 在保留期后可压缩为 DeleteFloor，防止旧 record 重放复活。

DeleteFloor 可保存 path 内部 generation watermark；这是节点 path 状态，不是 DKVSRecord 字段。

---

## 4. PathMeta

### 4.1 网络可比较状态

```go
type PathMeta struct {
    Version         uint32
    Path            string
    Generation      uint64
    StateRoot       Hash
    ActiveRecords   uint64
    ActiveTotalSize uint64
    MinExpiryHeight uint64
    ViewHeight      uint64
}
```

本地时间、最后同步 peer、重试次数、dirty/stale 等运行状态不得进入网络 PathMeta。

### 4.2 Generation

`Generation` 是 path 的节点维护 mutation revision：

- 单个有效 mutation 成功提交后递增；
- batch 内同 path 的 mutation 按 canonical key order 计数；
- exact retry/no-op 不递增；
- record 不携带 generation；
- full path snapshot 携带最终 PathMeta generation；
- standalone notify 只能提示 path 发生变化，接收节点不能根据到达顺序推测 generation。

收到非幂等远端 record 时，节点把目标 path 标记 stale，并通过认证的完整 path snapshot 修复。

### 4.3 StateRoot

StateRoot 覆盖当前网络可见状态：

- active relayable records；
- delete floors；
- 不包含 FREE_LOCAL；
- AUTOPAY 停止当前区块支付后，即使记录仍处于节点本地 grace，也必须退出 network PathMeta 和 snapshot。

PathMeta rebuild、PathSnapshot 和 P2P relay 必须使用完全相同的记录可见性规则。

### 4.4 到期与 root

`MinExpiryHeight` 从所有有限租期 record 的 `IssueHeight + TTL` 派生。

record 到期不代表 owner mutation，因此不增加 generation；但会在相应 `ViewHeight` 改变 StateRoot。同步必须比较：

```text
Generation + StateRoot + ViewHeight
```

不能只比较 generation。

---

## 5. 写入协议

### 5.1 单 key CAS

请求包含：

```text
signed record
expected path generation
expected current record hash 或 expect_absent
```

节点：

1. 锁外解析、验证签名、owner、fee、size；
2. 获取 path 锁；
3. 读取 PathMeta、record、delete floor；
4. 校验 path/record CAS 和 key-local Seq；
5. 校验 TTL、quota、namespace；
6. 计算 winner、PathMeta generation 和 StateRoot；
7. 一个本地 DB batch 原子提交；
8. 返回 accepted record 和最新 PathMeta；
9. 锁外发送 notify。

### 5.2 Batch-CAS

用于同一 owner 下多 key 的目标节点原子提交。

- mutation 可跨多个 path；
- path 按 canonical order 加锁；
- 每个 path 都有 expected generation/root；
- 任一前置条件失败则 `applied=0`；
- 不提供跨账户分布式事务；
- 写响应是目标节点的提交凭证，其他节点通过 path sync 最终收敛。

### 5.3 幂等

重试必须复用完全相同的签名 bytes。完全相同 active hash 返回幂等成功，不重复消耗 sequence、generation、quota 或业务通知。

---

## 6. 存储策略

### 6.1 FREE_LOCAL

- 必须 `TTL > 0`；
- 只保存在写入 endpoint；
- 不进入 P2P 和 network PathMeta；
- 受 endpoint 的 record/bytes/blob-key quota；
- 到期后本地清理。

### 6.2 AUTOPAY

- record 使用 `TTL = 0`；
- fee proof 指向配置的 AUTOPAY 合约；
- signer 派生 payer/delegate；
- 节点按当前区块支付状态、余额和容量验证；
- 停止支付后记录可在 endpoint 本地 grace 期间可读，但不再进入网络 path view；
- 恢复支付后可重新进入网络 path view。

### 6.3 Blob

- `/blob/<account_id>/<blob_key>`；
- 单 record opaque value；
- 最大 value 由节点 policy 限制，当前硬上限 1 MiB；
- owner-exclusive；
- 支持 FREE_LOCAL 或 AUTOPAY。

---

## 7. P2P 与完整 path 修复

### 7.1 Notify

P2P notify 携带签名 record，但 record 不携带 PathMeta generation。接收节点：

- exact 已存在 record：幂等忽略；
- 非幂等变化：标记 path stale，向可信 source 请求完整 path snapshot；
- FREE_LOCAL：拒绝传播。

### 7.2 Path snapshot

snapshot 包含：

```text
PathMeta
active records
delete floors
server_time_ms（诊断字段）
```

接收节点：

1. 验证 source 和 session；
2. 验证所有 record、fee、namespace 和 path；
3. 重算 network-visible record set、StateRoot、count、size、MinExpiryHeight；
4. 与 PathMeta 比较；
5. 一个本地 batch 原子替换 path；
6. 恢复 AUTOPAY retention cache；
7. 解除 stale；
8. 可重新广播已认证 active records，帮助下游节点修复。

### 7.3 Path repair 队列

同一 peer 的多个 stale path：

- FIFO 排队；
- 相同 path 去重；
- 请求以 `sessionID + cursor` 绑定；
- 单页超时后当前 path 回队尾，先推进其他 path；
- 终态错误释放 session；
- 临时失败延迟重试，避免重试风暴。

---

## 8. Wallet SDK 本地副本

`dkvsManager` 是 SDK 内唯一 DKVS transport、replica、path state 和同步协调层。领域模块不能直接管理 record、Seq、PathMeta、outbox 或 endpoint。

### 8.1 启动同步

Wallet 启动后：

1. 注册当前账户体系所需 path；
2. 启动同步 worker；
3. 主动执行首轮同步；
4. 当前 session 尚未同步成功的 scope 保持 not-ready。

持久化旧缓存可以展示为 cached，但不能宣称为最新状态，也不能用于生成写入 CAS。

### 8.2 持续同步

watch 发现 generation/root 变化，或 watch 请求异常时：

- 立即撤销对应 scope ready；
- 完整同步并原子替换本地 confirmed replica；
- 成功后恢复 ready。

### 8.3 读写 fail-closed

写入前必须满足：

- 当前 SDK session scope ready；
- PathMeta 存在；
- session state 为 idle/confirmed；
- LastErrorCode 为空；
- endpoint affinity 未失效。

prepared、inflight、conflict、error、stale 或首轮同步未完成时拒绝使用旧缓存写入。

### 8.4 并发边界

Wallet SDK 能做的最大努力是：

```text
写入前同步最新远端 value
→ 应用本地领域 mutation
→ 构造完整下一 value
→ CAS/batch-CAS
→ 冲突后重新同步并重算
```

同一账户两个设备同时修改同一字段时，应用决定 merge/覆盖语义，DKVS 不自动合并。

---

## 9. 账户管理统一托管数据

### 9.1 始终启用

PWA/Wallet 创建或导入第一个 mnemonic wallet 时，账户管理立即存在：

- 建立 root account identity；
- 建立完整 wallet/subaccount catalog；
- 生成本地 account secret；
- 默认使用 FREE_LOCAL 服务节点缓存；
- `RecoveryConfigured=false` 表示尚未配置恢复材料，不表示账户管理未启用。

### 9.2 统一 provider 接口

其他模块通过通用接口交给账户管理保存必要数据：

```go
type AccountManagedDataProvider interface {
    ID() string
    Export(AccountManagedDataCatalog) ([]AccountManagedDataPayload, error)
    Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error
    Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error
}
```

规则：

- provider ID 全局唯一且稳定；
- catalog 枚举全部 mnemonic wallets 和全部启用 subaccounts；
- payload 按 `provider + network + wallet fingerprint + account index` 隔离；
- 空 scope 不创建 payload；
- 未知 provider 在任何 import 前拒绝；
- 全部 provider 先 Validate，全部通过后才 Import；
- provider Import 必须幂等，可在同步失败后重试；
- 添加未来模块不需要修改账户同步核心。

### 9.3 统一状态与 blob

账户管理使用 root account 签名和支付：

```text
/personal/<root_account_id>/account/state
/blob/<root_account_id>/account-managed-data
```

state 保存：

- wallet/subaccount catalog；
- state revision；
- managed-data revision/hash；
- recovery configuration。

managed-data blob 保存加密的 provider items。state 与 blob 通过 batch-CAS 一起更新并回读验证。

### 9.4 存储策略切换

无有效 delegate：

```text
state + managed-data blob = FREE_LOCAL，有限 TTL
```

激活 AUTOPAY：

```text
账户管理刷新 catalog 和全部 provider
→ 用 root account 将 state + managed-data blob 改写为 TTL=0 AUTOPAY
→ 回读验证
→ 全部成功后显示 AUTOPAY 已配置
```

领域 provider 不感知 AUTOPAY、fee proof、DKVS key 或 endpoint。

### 9.5 钱包和子账户生命周期

新增/删除 wallet 或 subaccount 由账户管理负责：

- 更新 catalog；
- 标记 managed data dirty；
- 下一次同步重新枚举 scope；
- 新空 scope 不生成 provider payload；
- 移除 scope 时统一删除 bundle 中该 scope 的所有 provider items；
- provider 不直接处理账户目录或远端 DKVS 生命周期。

当前派生账户索引采用 append-only 语义；未来如果提供逻辑删除，仍由账户 catalog 表达 active/deleted scope，provider 只接收最终 catalog。

---

## 10. RGB11 接入

### 10.1 永久恢复数据

RGB11 注册为账户管理 provider：`rgb11`。

只导出丢失后可能影响资产控制或未完成操作安全的数据：

- 当前 allocation proof；
- 对应最小 carrier output 信息；
- 必要 consignment object/validation receipt；
- 未终结发送 operation、reservation 和签名交易；
- 未终结接收 transfer、consignment object；
- active receive request、seal/blinding/reservation metadata。

不保存：

- 余额和 asset list 投影；
- 完成历史；
- ticker/icon/描述缓存；
- confirmation、scan height、raw tx cache；
- unrelated BTC/ORDX assets；
- 可从 canonical object 重建的索引。

恢复时：

```text
导入最小 recovery package
→ 重建 projection/cache
→ 重建 UTXO reservation
→ 链上 reconciliation
```

缺失 payload 表示该 scope 没有不可重建 RGB 状态，导入时权威清理旧本地 RGB 状态。

### 10.2 瞬态 RGB DKVS

以下仍可由 RGB 模块使用 DKVS：

- receive capability；
- address delivery；
- ACK/NACK；
- mailbox relay。

这些全部是有限 TTL FREE_LOCAL 传输记录：

- 不使用 AUTOPAY；
- 不代表永久备份；
- 到期后可清理；
- 永久恢复状态由账户管理 provider 负责。

---

## 11. 数据格式与开发阶段约束

当前协议直接替换旧开发格式：

- DKVSRecord wire layout、签名 hash 和 StateRoot 已变化；
- TTL 单位统一为 SatoshiNet blocks；
- RGB 独立 head/snapshot 永久备份已删除；
- 账户托管数据使用严格编码和加密 bundle；
- 不保留旧格式读取、迁移命令、fallback 或双写。

部署测试环境必须：

```text
所有 SatoshiNet 节点、Wallet SDK、WASM、PWA 同时升级
清理旧 DKVS/Wallet/PWA 开发数据
禁止新旧节点混跑
```

---

## 12. 验收要求

### SatoshiNet

- DKVSRecord 不含 PathGeneration/ExpiryHeight/Unix IssueTime；
- Seq 为 key-local revision；
- IssueHeight+TTL 在相同链高度确定性过期；
- PathMeta generation/root 在节点间收敛；
- FREE_LOCAL 不进入 network view；
- AUTOPAY grace 不泄漏到 PathMeta/snapshot；
- path snapshot 修复支持队列、超时和失败推进；
- batch-CAS 全成功或全失败；
- delete floor 阻止旧状态复活。

### Wallet SDK

- 启动同步与 watch 持续更新本地 replica；
- stale/conflict/error scope fail-closed；
- 多设备写前同步、CAS 冲突后重算；
- 首个钱包自动启用账户管理；
- catalog 覆盖所有 wallets/subaccounts；
- provider 未知/验证失败时没有部分 import；
- 独立 provider/scope 可三方合并；
- 移除 scope 会删除相应 provider item。

### 账户管理与 RGB

- FREE_LOCAL 与 AUTOPAY 都由账户管理统一写 state/blob；
- managed-data blob 可同时包含 RGB 和其他 provider；
- RGB 空 scope 不占 blob；
- RGB 最小恢复包不包含完成历史和派生缓存；
- 发送方、接收方在未完成流程中换机后可继续；
- 确认后恢复当前 allocation 并重建锁；
- PWA 不展示 RGB 独立备份状态，也不直接操作 DKVS record。

---

## 13. 最终原则

1. `Seq` 只管理单 key；`PathMeta.Generation` 管理 path。
2. record 生命周期只由 `IssueHeight + TTL` 表达。
3. DKVS 解决协议一致性，不解决领域并发合并。
4. Wallet SDK 本地副本必须持续同步；无法证明最新时 fail-closed。
5. 账户管理从第一个钱包起存在，并统一管理所有 wallet/subaccount scope。
6. 模块只通过 provider 接口交付必要恢复数据，不感知 DKVS/AUTOPAY。
7. RGB 永久状态归账户管理；RGB 自己只使用有限 TTL 的瞬态传输记录。
8. 开发阶段直接采用当前格式，不保留旧协议兼容路径。
