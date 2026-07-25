# DKVS Blob、RPC 目录同步与原子 Batch-CAS 优化方案

更新时间：2026-07-25
目标仓库：`sat20-labs/satoshinet`、`sat20-labs/sat20wallet`
状态：Review；设计、实现和本地定向验证已完成，等待 PR 审核与远端合并门禁

## 1. 已确认的产品与协议决策

本轮按以下决策实施，不保留未发布方案的兼容代码：

1. 普通 DKVS value 仍受普通 record 上限约束；当前普通 value 上限为 16 KiB。
2. 大于普通 value 上限的数据使用 `/blob/<account_id>/<blob_key>`，一个 blob 对应一条完整 `DKVSRecord`，不拆分。
3. blob value 的硬上限为 1 MiB，即 1,048,576 bytes；任何入口均不得接受更大的 value。
4. 不保留旧的多记录 blob 格式、读取分支、迁移工具或兼容路径。非两段 blob key 直接视为非法 key。
5. blob 同时支持：
   - `AUTOPAY`：参与节点间同步和网络持久化；
   - `FREE_LOCAL`：仅保存在接受写入的服务节点，由该节点公开的 DKVS policy 限制，不参与 P2P relay。
6. DKVS 有两套职责不同的同步接口：
   - P2P 原生协议用于节点之间的数据复制、镜像和 anti-entropy；
   - RPC/indexer 接口用于 wallet SDK 和其他应用按目录同步 KV，这是应用侧的标准同步方式。
7. 多端同步不是另一套协议，而是 RPC 目录同步、CAS 和 batch-CAS 的应用特性。
8. 同时更新多个 key 使用通用原子 batch-CAS：所有前置条件针对同一已提交视图检查，全部 mutation 在一个 KVDB write batch 中提交；任一冲突或校验失败时整批不落库。

## 2. 当前实现基线与问题

现有 DKVS 已具备签名 record、namespace 权限、TTL/expiry、tombstone、fee proof、AUTOPAY、FREE_LOCAL、checkpoint/snapshot、P2P filtered sync 和 REST API。需要重点修正的问题如下：

### 2.1 Blob 模型

旧实现把一个大对象表示为多条相关 record，导致：

- 一次业务写入跨多条 key，容易出现部分成功；
- P2P 与 snapshot 需要额外的组合、排序和整组校验逻辑；
- AUTOPAY 容量按 record 数计算时，一个对象占用多个 slot；
- SDK 必须处理组装、缺失片段和跨代混合；
- 代码路径和状态空间明显扩大。

新模型将 blob 恢复为 DKVS 的基本抽象：一个 key 对应一个已签名 value。

### 2.2 多端并发

`Seq + hash` 的确定性选择可以让节点最终收敛，但不能替代应用写入冲突检测。两个设备基于同一旧值分别生成新值时，如果服务端只选出一个“较大”record，另一个设备会遭遇静默覆盖。

因此应用写入必须带明确前置条件：

- key 不存在；或
- 当前 active record hash 等于调用方读取的 hash。

### 2.3 多 key 业务一致性

钱包快照与 head、blob 与 mailbox locator 等业务状态包含多个 key。逐条调用单 key CAS 仍可能留下半状态。需要通用 batch-CAS，而不是为每个业务定义专用事务接口。

### 2.4 应用同步边界

P2P filtered sync 是节点协议，不应成为 wallet SDK 的直接应用契约。应用需要稳定、简单的目录接口，并能验证分页期间视图没有变化、删除没有丢失、返回集合没有被截断或替换。

## 3. 单记录 Blob 协议

### 3.1 Key 结构

唯一合法结构：

```text
/blob/<account_id>/<blob_key>
```

约束：

- `account_id` 使用 DKVS 现有 canonical account ID；
- `blob_key` 是一个合法 DKVS path segment；
- blob key 只允许两段 namespace 参数，不允许附加子路径；
- record 必须是 account-scoped record，`PubKey` 为空，签名公钥由 `account_id` 还原；
- signer 必须与路径中的 `account_id` 一致。

### 3.2 尺寸

```text
MaxDKVSValueSize       = 16 KiB
MaxDKVSBlobValueSize   = 1 MiB
MaxDKVSBlobRecordSize  = 1 MiB + 有界 envelope 开销
```

wire codec 在读取 value 前先读取 key，再依据 key 选择允许的 value/record 上限。普通 namespace 不因 blob 支持而放宽。

HTTP 请求体上限必须覆盖 JSON/base64 编码后的单个 blob，但服务端解码后仍以 wire value/record 上限为最终判据。batch-CAS 另设整批 record bytes 上限，防止多个 1 MiB blob 形成无界请求。

### 3.3 完整性与身份

blob 仍是标准 `DKVSRecord`：

- `SigningHash` 覆盖 key、value hash、seq、issue time、TTL/expiry、fee proof 和 flags；
- `RecordHash` 覆盖完整 canonical record；
- value 在传输、落库和读取时均受 record 签名保护；
- SDK 可以在 value 内定义自己的版本化 envelope，但不得绕过外层 record 校验；
- 空 blob value 被拒绝；业务若需表达空内容，应在应用 envelope 中编码，而不是使用零长度 DKVS blob。

## 4. Blob 保存模式与配额

### 4.1 AUTOPAY Blob

AUTOPAY blob 规则：

```text
FeeMode       = AUTOPAY
TTL           = 0
ExpiryHeight  = 0
```

每个 AUTOPAY delegate 默认允许一个 active blob key。调用 `autopay.tc` 的 `config` 接口时可设置：

```text
blobKeyLimit
```

规则：

- 未显式设置时归一化为 1；
- 合约与 DKVS verifier 使用同一默认值；
- 设置值必须在协议上限内，当前上限为 1024；
- 覆盖同一 blob key 不增加占用；
- 新建不同 blob key 才增加占用；
- tombstone 删除后释放占用；
- 普通 AUTOPAY record slot 与 blob-key slot 分开统计，避免一个 1 MiB blob被错误当成多条普通 record。

容量检查必须在持有 indexer 写锁的最终提交阶段，对 batch 的最终投影视图统一计算，避免并发超卖。

### 4.2 FREE_LOCAL Blob

FREE_LOCAL blob 规则：

```text
FeeMode       = FREE_LOCAL
TTL           > 0
ExpiryHeight  = 节点 policy 允许的值
```

它只存在于接收该写入的服务节点：

- 不进入 P2P notify/inv/get/data；
- 不进入 miner 或普通节点 mirror；
- 不进入网络 checkpoint/snapshot；
- 远端节点收到 FREE_LOCAL record 必须拒绝；
- 到期后由本地 prune 删除。

节点通过 `GET /v3/dkvs/config` 返回实际 admission policy：

```json
{
  "free_local": {
    "enabled": true,
    "max_ttl_ms": 86400000,
    "max_records_per_signer": 100,
    "max_bytes_per_signer": 2097152,
    "max_total_records": 100000,
    "max_total_bytes": 1073741824
  },
  "blob": {
    "max_value_size": 1048576,
    "max_free_local_keys_per_signer": 1
  },
  "max_batch_mutations": 64,
  "max_batch_record_bytes": 8388608
}
```

SDK 可用该配置提前拒绝明显非法请求，但节点必须在提交时重新检查。FREE_LOCAL blob 同时受：

- max TTL；
- 每 signer record 数；
- 每 signer 总 bytes；
- 节点总 record 数和总 bytes；
- 每 signer distinct active blob key 数；
- blob value 硬上限。

默认 distinct FREE_LOCAL blob key 数为 1。该值是服务节点策略，不由 AUTOPAY 合约控制。

## 5. 通用原子 Batch-CAS

### 5.1 API

单 key CAS：

```text
POST /v3/dkvs/records/cas
```

多 key batch-CAS：

```text
POST /v3/dkvs/records/batch-cas
```

请求示例：

```json
{
  "mutations": [
    {
      "record": { "...": "signed DKVSRecord" },
      "expect_absent": true
    },
    {
      "record": { "...": "signed DKVSRecord" },
      "expected_hash": "<current-record-hash>"
    }
  ]
}
```

每条 mutation 必须且只能指定一个前置条件：

- `expect_absent=true`；
- `expected_hash=<hash>`。

### 5.2 原子语义

一批 mutation 的处理顺序：

1. 检查 batch 数量、重复 key 和 aggregate record bytes。
2. 在锁外完成不依赖可变本地状态的解析、签名、身份、权限和 fee state 验证。
3. 记录每个 key 的 active record、delete floor、owner 和 policy generation 快照。
4. 获取 indexer 写锁。
5. 确认准备阶段使用的 key 状态和 policy generation 仍未变化。
6. 针对同一个 committed view 校验全部前置条件。
7. 将全部 mutation 投影为最终视图，统一校验：
   - record selection / seq；
   - tombstone；
   - mailbox quota；
   - AUTOPAY 普通 slot 和 blob-key slot；
   - FREE_LOCAL record/bytes/blob-key quota；
   - namespace 特有规则。
8. 在一个 KVDB write batch 中写入 record、hash index、delete state 和 path metadata。
9. 成功后一次性刷新本地 usage/expiry cache、递增 generation，并在锁外发送 notify。

任何一步失败：

```text
applied = 0
所有 key 保持原状态
```

### 5.3 幂等重试

若一整批 mutation 的完整 `RecordHash` 均已经是当前 active record，或均是当前仍保留的同一签名 tombstone，则该整批 exact retry 视为已经提交。即使原始前置条件此时已经不再成立，也返回幂等成功，不重复：

- 写数据库；
- 增加 seq；
- 占用配额；
- 发送 notify；
- 产生额外计费语义。

整批 exact retry 返回 `applied=0`。如果只有部分 mutation 已经存在而其余 mutation 尚未提交，服务端返回 `ErrWriteConflict`，不会补写剩余 mutation；这避免把一次未知结果的旧请求错误地转换成新的半事务。

签名 tombstone 的 exact-retry 识别以其传播保留期为边界。保留期结束并压缩为 sequence floor 后，旧值仍不能复活，但服务端不再保存完整 tombstone hash，因此调用方应重新同步目录，而不是无限期重放旧删除请求。

### 5.4 冲突

前置条件不匹配返回 `ErrWriteConflict`。当前 indexer REST 契约保持 HTTP 200 并通过响应 `code/msg` 返回业务错误；Wallet SDK 将 `dkvs write conflict` 映射为可用 `errors.Is` 判断的 `ErrWriteConflict`。服务端不得以 record hash tie-break 代替 CAS 冲突响应。

batch 中任意一条冲突，整批冲突。调用方必须重新读取业务目录或相关 key，决定重建、合并或放弃 mutation。

### 5.5 原子性边界

batch-CAS 保证接受 RPC 的节点本地数据库原子性。提交成功后，该节点才逐条产生现有 DKVS notify。因此：

- 应用通过同一 RPC 节点读取时不会看到半批次；
- P2P 网络仍按 record 粒度传播，其他节点最终收敛；
- 本轮不新增 P2P transaction/batch wire message，也不宣称跨节点线性一致事务。

## 6. 两套数据同步方式

### 6.1 节点间 P2P 同步

继续使用六个原生消息：

```text
dkvsnotify
dkvsinv
dkvsget
dkvsdata
dkvssyncreq
dkvssyncres
```

职责：

- miner 间全量同步；
- 普通节点按 subscription mirror；
- 增量 notify；
- anti-entropy；
- 网络 active-record checkpoint root 对账。

P2P 只复制 relayable record。AUTOPAY blob 是普通单条 record；FREE_LOCAL blob 不进入该路径。

### 6.2 应用 RPC 目录同步

标准应用接口：

```text
POST /v3/dkvs/sync/directory
POST /v3/dkvs/watch/directory
```

请求只暴露一个合法 prefix：

```json
{
  "prefix": "/personal/<account_id>/rgb11",
  "cursor": null,
  "limit": 100
}
```

返回：

```json
{
  "records": [],
  "next_cursor": null,
  "done": true,
  "root": "<directory-root>"
}
```

应用目录视图包含：

- 当前 active records；
- 保留期内的签名 tombstones。

目录 root 对返回的完整签名视图计算，因此删除也会改变 root。wallet SDK 必须：

1. 验证每条 record 的 key 范围、flags、IssueTime、签名和 identity；active record 必须未过期，保留期内的签名 tombstone允许已经到达其原 record expiry；
2. 检查所有分页返回同一个 root；
3. 检查 cursor 严格推进；
4. 拒绝同 key 不同 hash 的重复项；
5. 完成分页后本地重算 directory root并比较；
6. 仅在完整批次验证并持久化后替换本地 confirmed mirror。

`watch/directory` 只通知 root 是否变化；发生变化后客户端重新执行完整目录同步。这样 API 不依赖 WebSocket 连接状态，也不会把单条通知误当成完整状态。

### 6.3 多端同步语义

同一账户多个设备均使用 RPC 目录同步：

- 不同 key 的 mutation 可以合并；
- 同一 key 的更新使用 CAS，冲突显式返回；
- 多 key 业务状态使用 batch-CAS；
- tombstone 防止离线旧值重新出现；
- 整批 exact retry 在 active record 或签名 tombstone 保留期内幂等；
- 本地 outbox 先拉取 confirmed view，再提交带前置条件的 mutation；
- 提交后重新拉取目录，确认 root 和 active state。

## 7. Wallet SDK 落地

wallet SDK 提供通用接口：

```go
SyncDirectory(...)
SyncDirectoryAll(...)
WatchDirectory(...)
PutRecordCAS(...)
PutRecordBatchCAS(...)
GetDKVSClientConfig(...)
```

blob API 只处理一个 record：

```go
BuildDKVSSignedBlobRecord(...)
BuildDKVSSignedBlobRecordFreeLocal(...)
PutBlobWithAutopay(...)
PutBlobFreeLocal(...)
GetBlob(...)
```

RGB11 使用方式：

- encrypted wallet snapshot blob 与 wallet head 在一个 batch-CAS 中更新；
- 大型 address consignment blob 与 mailbox locator 在一个 batch-CAS 中更新；
- AUTOPAY 可用于 relayable/persistent 保存；
- 未启用 AUTOPAY 时按节点配置使用 FREE_LOCAL；
- batch 失败不得更新本地 head、retention 或 transfer delivery 状态。

## 8. 数据安全、一致性与完整性审查

### 8.1 已纳入本轮的控制

- namespace shape 和 account identity 绑定；
- canonical signing hash 和 record hash；
- 普通/大 value 分离的 wire 上限；
- HTTP body、batch count 和 aggregate bytes 上限；
- AUTOPAY/FREE_LOCAL 明确模式，拒绝模糊 fee proof；
- FREE_LOCAL fail-closed、不可 relay；
- 配额在最终临界区按 batch 投影视图检查；
- CAS 消除同 key 静默覆盖；
- batch-CAS 消除本地多 key 半提交；
- tombstone 参与目录同步 root；
- hash reverse index 与 active record 同批更新；
- path metadata 与 mutation 同批标脏；
- P2P payload bytes 限制，防止一个包含多个大 blob 的消息超过协议上限；
- sync cursor non-progress、root change、重复冲突和 staging size 防护。

### 8.2 保持的保守边界

- DID resolver、system authority 和 mainnet fee policy 未配置时继续 fail-closed；
- 应用目录 root 是节点提供并由客户端复算的状态完整性证明，不是全网共识证明；
- checkpoint/snapshot 仍是非共识本地计算结果；
- batch-CAS 不提供跨节点分布式事务；
- 本轮不增加超过 1 MiB 的分片、流式上传或断点续传。

### 8.3 后续建议

以下不阻塞本轮，但建议在生产前继续处理：

1. RPC 写接口增加认证、速率限制、连接级 body 限制和审计指标。
2. 为 batch-CAS 增加 operation ID 结果缓存，可在 signed tombstone 压缩以后仍查询历史事务结果；当前实现已保证 active record 与 tombstone 保留期内的整批 exact-record retry 幂等。
3. 对目录 root、CAS conflict、quota rejection、P2P payload split 和 FREE_LOCAL prune 增加运行指标。
4. 明确主网 AUTOPAY 合约、recipient、fee asset、普通 record 费率和 blob key 产品价格。
5. 对业务 envelope 中的敏感数据继续使用端到端加密；DKVS record 签名只保证来源和完整性，不提供保密性。

## 9. 测试计划与验收标准

### 9.1 SatoshiNet

必须覆盖：

- 普通 value 16 KiB 边界；
- blob value 1 MiB 边界和超限拒绝；
- 非法 blob key shape 拒绝；
- AUTOPAY blob 保存、覆盖、删除和 `blobKeyLimit`；
- FREE_LOCAL blob enabled/disabled、TTL、bytes、record 和 distinct key quota；
- 两个并发请求竞争最后一个 blob key slot；
- P2P 大 blob codec、notify/data/sync payload 分页；
- FREE_LOCAL blob 不 relay；
- 单 key CAS create/update/conflict/exact retry；
- batch-CAS 全成功、任一冲突整批失败、quota 失败整批失败、exact retry；
- batch 中 mailbox/AUTOPAY/FREE_LOCAL 配额最终投影；
- directory sync 分页 root、tombstone、watch；
- P2P checkpoint root 与应用 directory root 的边界。

### 9.2 Sat20wallet

必须覆盖：

- 单记录 blob envelope encode/decode；
- AUTOPAY 和 FREE_LOCAL blob；
- 节点 config 的 blob/TTL/batch 限制；
- SDK batch-CAS JSON、冲突和幂等；
- 多客户端同 key 冲突；
- 多客户端不同 key 合并；
- directory root 变化、分页、重复 key、错误 root；
- RGB11 snapshot/head 原子提交与故障注入；
- RGB11 blob/mailbox locator 原子提交与故障注入；
- tombstone 防复活；
-现有 wallet 包和相关 E2E 回归。

### 9.3 已完成的本地验证

SatoshiNet：

```text
go vet ./wire ./contract/template ./indexer/indexer/dkvs ./indexer/indexer/dkvs/p2p ./indexer/indexer ./indexer/rpcserver/indexer
go test ./wire -run DKVS -count=1
go test ./contract/template -count=1
go test ./indexer/indexer/dkvs -count=1
go test ./indexer/indexer/dkvs/p2p ./indexer/indexer ./indexer/rpcserver/indexer -count=1
git diff --check
```

Sat20wallet（模块根目录为 `sdk`）：

```text
go test ./wallet/... -count=1
go vet -copylocks=false ./wallet/...
go test ./e2e -run '^$' -count=1
GOOS=js GOARCH=wasm go build ./wasm
git diff --check
```

`go vet ./wallet` 的既有 `copylocks` 告警来自线上兼容数据结构，不属于本轮修改；按项目决定使用 `-copylocks=false` 验证本轮代码，不修改这些老结构。

本地 `go test ./... -count=1` 已覆盖并通过绝大多数包，但执行环境在后段触发总时限，因此不作为“全仓完整通过”的证据。race 和 PWA 构建以 GitHub Actions 原生 runner 的结果作为最终门禁。

### 9.4 PR Review / 合并门槛

- 所有新文件 gofmt；
- `git diff --check` 无错误；
- SatoshiNet DKVS/wire/contract/P2P/RPC 相关测试通过；
- Sat20wallet SDK 全包测试、E2E 编译和 WASM 构建通过；
- 文档中不再把多记录大对象描述为有效 DKVS blob 方案；
- PR 中不包含用于工作区传输的临时 workflow、payload 或 artifact 配置；
- 两个 PR 基于各自主线形成干净提交并转为 Ready for review；
- GitHub Actions 必须全部通过后才可合并。
