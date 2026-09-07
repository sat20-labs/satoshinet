# DKVS Wallet 同步优化最终方案

日期：2026-08-28

状态：方案已确认并按本文落地。开发版本直接清理旧 DKVS、Wallet replica、subscription
state 和异常 outbox，不提供旧格式升级或自动修复生产逻辑。

## 1. 目标

Wallet 只需要两类能力：

1. 自己管理的数据在启动时确认最新，并在运行期间低成本发现变化；
2. 只读数据在使用时直接读取，并用短时间缓存降低请求量。

服务端保持无 Wallet session、无 cursor、无 change log、无 watcher。节点之间原有的 P2P
path sync 不属于 Wallet 应用协议，继续保留。

## 2. 两套状态边界

`PathMeta` 同时保存两种用途不同的状态：

```go
type PathMeta struct {
    Generation         uint64
    EndpointGeneration uint64
    StateRoot          Hash
    ViewHeight         uint64
    // count/size/expiry fields omitted
}
```

### 2.1 canonical P2P 状态

`Generation + StateRoot + ViewHeight` 只描述可 relay 的 path 状态：

- 用于节点间比较、snapshot 和修复；
- 不包含 FREE_LOCAL；
- 不暴露给 Wallet 作为写前置条件；
- 不因纯 FREE_LOCAL 变化而改变。

### 2.2 endpoint-local freshness

`EndpointGeneration` 是当前服务端该 path 的最小状态变量：

- 本端可见内容发生任何变化都递增；
- FREE_LOCAL put、update、delete、expiry 同样递增；
- 服务端直接返回已有值，不重新计算 hash 或 token；
- Wallet 只做相等比较，不解释其数值；
- 不进入 P2P snapshot，不 relay，也不跨节点比较。

因此 FREE_LOCAL 与其他数据在当前端的 prefix status、snapshot、读写和缓存失效处理完全一致。
唯一差异是 FREE_LOCAL 不通过 P2P 广播到其他节点。

## 3. Wallet 管理的 prefix

### 3.1 启动同步

Wallet 启动后：

1. 获取服务端 `EndpointID`；
2. 计算并持久化当前账户管理的 prefix 集合；
3. 对每个 prefix 获取完整 snapshot；
4. 按 prefix 原子替换 confirmed replica；
5. 保存 snapshot 返回的 `EndpointGeneration`；
6. 全部成功后将对应 scope 标记为 ready。

首次启动、服务端切换、prefix 集合变化或本地 generation 缺失时，必须重新 snapshot。
`generation = 0` 是有效状态，不能与“尚未同步”混淆；本地持久化必须单独记录 generation 是否
存在。Endpoint 切换时先清空旧 generation，所有目标 prefix 安装完成后才标记 READY，确保
进程在中途退出后不会把新旧 endpoint 的 replica 混合成已同步状态。

### 3.2 定时检查

Wallet 默认每 1 分钟批量发送：

```json
{
  "endpoint_id": "...",
  "prefixes": [
    {"prefix": "/personal/<account>/catalog", "generation": 12}
  ]
}
```

服务端只返回 generation 不同的 prefix。Wallet 仅对这些 prefix 获取新 snapshot 并原子替换
本地 replica。通常没有变化，因此响应为空。

服务端不为终端保存订阅状态，也不创建等待 goroutine。

## 4. 非管理数据的直接读取

Wallet 对自己只有读权限或不需要长期维护的 key/prefix：

- 使用时直接读取服务端；
- 请求超时为 5 秒；
- 成功结果放入 endpoint-scoped 内存缓存 1 分钟；
- 1 分钟内重复读取只访问缓存；
- 不注册 managed prefix，不写入 confirmed replica；
- 写入命中缓存中的 key 时立即使相应缓存失效。

Mailbox/message 数据属于按需读取，不作为 Wallet managed prefix 持久化。

## 5. 写入与 CAS 冲突

写入仍使用 per-key CAS/batch-CAS。一次业务 mutation 的处理为：

1. 读取当前 confirmed value；
2. 执行业务 builder，构造并签名下一版本；
3. 提交 batch-CAS；
4. 若返回 `WriteConflict` 或 `InvalidSequence`，从服务端直接刷新本次涉及的全部 key；
5. 基于新 value 重新执行业务 builder、重新签名并使用新 request ID 提交；
6. 最多尝试 3 次。

冲突重试不能复用旧业务结果，因为并发变化后 value、Seq、ETag 和签名都可能改变。

## 6. Outbox 失败分类

- 网络连接、超时等临时网络错误：保留完全相同的已签名请求并重试；
- CAS conflict：进入上述刷新、重算、重签流程；
- endpoint mismatch 或需要人工确认的数据归属冲突：保留冲突证据，由工具处理；
- 协议拒绝、非法记录、storage mode downgrade 等永久错误：直接 panic，尽早暴露设计或实现错误；
- 不支持 `DKVS_STORAGE_MODE_DOWNGRADE` 配置，端上也不提供该设置；
- 旧版本异常 outbox 由维护工具清理，生产代码不自动删除或改写异常数据。

PAID/AUTOPAY key 不允许降级为 FREE_LOCAL；FREE_LOCAL 升级到 AUTOPAY/PAID 使用普通 Put。

## 7. HTTP 应用协议

保留：

```text
GET  /v3/dkvs/config
GET  /v3/dkvs/record
GET  /v3/dkvs/key-state
POST /v3/dkvs/records/batch-cas
POST /v3/dkvs/prefixes/status
POST /v3/dkvs/prefixes/snapshot
POST /v3/dkvs/prefixes/read
```

删除：

```text
POST /v3/dkvs/subscriptions/snapshot
POST /v3/dkvs/subscriptions/watch
```

应用协议不暴露 canonical PathMeta root/generation，不接收 path write precondition。

## 8. Endpoint 切换

`EndpointGeneration` 只在同一 `EndpointID` 内有效。

- 没有 active/pending FREE_LOCAL 数据时，可以切换 endpoint 并重新 snapshot；
- 存在 active FREE_LOCAL 或 pending FREE_LOCAL outbox 时，自动切换必须失败；
- 不把某节点的 generation 带到另一节点比较；
- 不把 FREE_LOCAL 从旧 endpoint 自动迁移或伪装成已同步数据。

## 9. 开发版本数据处理

本版本改变 PathMeta 和 Wallet replica 的持久化格式，不做兼容升级：

- 重置测试网络 DKVS 数据；
- 删除本地所有测试 PWA profile/IndexedDB；
- 在回滚步骤中通过导入助记词重新建立钱包账户；
- 使用工具清理旧异常 outbox；
- 先验收账户管理，再验收 RGB 资产管理，随后覆盖其他重要功能。

## 10. 验收标准

### SatoshiNet

- 任意 endpoint-visible mutation 都推进 `EndpointGeneration`；
- FREE_LOCAL 出现在本端 prefix snapshot；
- 纯 FREE_LOCAL 变化不改变 canonical generation/root；
- FREE_LOCAL 永不进入 P2P relay；
- prefix status 只返回发生变化的 prefix；
- 无 Wallet cursor、change log、watcher 或 session；
- 旧 subscription API 不再暴露。

### Wallet SDK

- 启动时完整同步 managed prefixes；
- 1 分钟轮询仅重新同步变化 prefix；
- unmanaged read 使用 5 秒超时和 1 分钟 endpoint-scoped 内存缓存；
- mailbox 不持久化为 managed prefix；
- CAS conflict 会刷新、重算、重签并最多重试 3 次；
- 永久 outbox 错误 panic，仅网络错误持续重试；
- EndpointID 切换遵守 FREE_LOCAL affinity。

### PWA

- AUTOPAY 过期时提示用户充值并提供入口；
- 重置后通过助记词导入重建账户；
- 账户 catalog、wallet/subaccount、provider 数据完整恢复；
- RGB allocation、锁定状态和必要恢复数据正确；
- 账户管理与 RGB 验收通过后再覆盖发送、接收、channel 等重要流程。

## 11. 最终原则

1. FREE_LOCAL 的不同仅限于不通过 P2P relay；本端处理不另建一套协议。
2. Wallet freshness 直接使用服务端 PathMeta 已有的 `EndpointGeneration`。
3. Wallet 定时比较 prefix 状态，不持续 watch。
4. 非管理数据按需直读并短缓存。
5. CAS conflict 必须刷新后重算，不能盲重放旧 mutation。
6. 永久错误 fail-fast，异常历史数据交给工具清理。
