# DKVS Wallet / PWA API

更新时间：2026-10-04

本文描述当前 Wallet 应用协议。  
**PWA 业务代码应调用 Wallet SDK/WASM，不应自己维护 Seq、generation、CAS、request signature、fee proof 或 outbox。**

下面的 HTTP shape 主要用于 SDK 开发、调试和接口验收。

---

## 1. 当前 Wallet API

~~~
GET  /v3/dkvs/config
GET  /v3/dkvs/record?key=...
GET  /v3/dkvs/key-state?key=...

POST /v3/dkvs/records/batch-cas

POST /v3/dkvs/active/sync
POST /v3/dkvs/active/watch

POST /v3/dkvs/prefixes/read
~~~

不存在：

~~~
/v3/dkvs/write-context
mutation log API
delete history API
global generation API
~~~

checkpoint、node snapshot 和 node subscription 是本地管理接口，不是 PWA 协议。

---

## 2. Config

~~~
GET /v3/dkvs/config
~~~

返回至少包括：

~~~json
{
  "endpoint_id": "core-node-id",
  "free_local": {
    "enabled": true,
    "max_ttl_blocks": 120000
  },
  "blob": {
    "max_value_size": 1048576
  },
  "max_batch_mutations": 256,
  "max_batch_record_bytes": 4194304
}
~~~

EndpointGeneration 只在同一个 endpoint 内有效。切换服务节点后必须重新同步。

FREE_LOCAL TTL 必须使用当前节点返回的 policy；客户端不能猜测 fallback TTL。

---

## 3. 单 key 读取

~~~
GET /v3/dkvs/record?key=/personal/...
GET /v3/dkvs/key-state?key=/personal/...
~~~

record response 同时返回 ETag = RecordHash。

正常 current-state 模型下，key-state 主要表现为：

~~~
active
never_seen
~~~

删除后不保留历史 deleted state；物理不存在的 key 返回 never_seen。

---

## 4. Managed scope full sync

Wallet 第一次管理一个 prefix、恢复进程或 generation 缺失时执行完整同步：

~~~http
POST /v3/dkvs/active/sync
content-type: application/json
~~~

~~~json
{
  "scope": {
    "prefix": "/personal/<account>/wallet"
  },
  "endpoint_id": "core-node-id",
  "after": 0,
  "full": true
}
~~~

响应：

~~~json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "meta": {
      "endpoint_id": "core-node-id",
      "scope": {
        "prefix": "/personal/<account>/wallet"
      },
      "generation": 12,
      "root": "...",
      "view_height": 3456
    },
    "records": [],
    "complete": true
  }
}
~~~

分页时 next cursor 固定：

~~~
endpoint
scope
generation
root
view height
last key
~~~

服务端状态在分页期间变化时，旧 cursor 失效，SDK 重新开始 full sync。

完整同步完成后：

~~~
验证 root
→ 原子替换 confirmed replica
→ 保存 generation
→ scope READY
~~~

---

## 5. Incremental sync

已有 confirmed generation 时：

~~~json
{
  "scope": {
    "prefix": "/personal/<account>/wallet"
  },
  "endpoint_id": "core-node-id",
  "after": 12,
  "full": false
}
~~~

服务端返回 generation 12 之后**仍然当前存在**的变化 records。

它不是 mutation log：

- 不返回历史 tombstone；
- 不返回已经不存在的旧 value；
- 删除通过 current root / full reconciliation 发现；
- 如果增量结果不能得到服务端当前 root，SDK 转 full sync。

同一次服务端原子 batch 中，多个 key 可以共享同一个 generation。

---

## 6. Long-poll Watch

在线 PWA 通过 SDK 使用：

~~~
POST /v3/dkvs/active/watch
~~~

请求：

~~~json
{
  "endpoint_id": "core-node-id",
  "scopes": [
    {
      "scope": {
        "prefix": "/personal/<account>/wallet"
      },
      "generation": 12,
      "root": "..."
    }
  ]
}
~~~

服务端：

- 不保存 Wallet session；
- 不保存 offline queue；
- 不保存 mutation log；
- 最多返回一个变化 scope 的 current page；
- 约 20 秒无变化时返回空结果，客户端重新 watch。

Watch 只用于提示和传递当前变化；本地 confirmed state 仍按 ActivePage 校验/install。

---

## 7. PUT / Batch CAS

写入入口：

~~~
POST /v3/dkvs/records/batch-cas
~~~

普通业务请求示意：

~~~json
{
  "endpoint_id": "core-node-id",
  "request_id": "random-request-id",
  "mutations": [
    {
      "record": {
        "Version": 1,
        "Key": "/personal/...",
        "Seq": 2
      },
      "expected_etag": "current-record-hash"
    }
  ],
  "authorization": {
    "context": {
      "endpoint_id": "core-node-id",
      "prefixes": [
        {
          "prefix": "/personal/<account>/wallet",
          "generation": 12
        }
      ]
    },
    "signature": "<base64>"
  }
}
~~~

新 key 使用：

~~~json
{
  "record": {
    "Key": "...",
    "Seq": 1
  },
  "expect_absent": true
}
~~~

mutation 的 expected_etag 与 expect_absent 必须二选一。

### Request authorization

authorization 由 Wallet SDK 生成，覆盖：

~~~
EndpointID
RequestID
prefix generations
RecordHash
CAS 条件
~~~

因此 PWA 不应复制历史 record 后自己添加 expect_absent。

### Generation 来源

**PUT 不调用 /write-context。**

SDK 优先从 confirmed replica 读取本地已同步 generation。

如果没有：

~~~
full sync prefix
→ 得到 generation
→ 再 PUT
~~~

服务端返回 STALE_GENERATION 时：

~~~
同步
→ 根据最新 current state 重建 mutation
→ 重签 record/request
→ 新 RequestID 再提交
~~~

不能仅修改旧请求中的 generation。

---

## 8. ACK 与本地副本

成功 batch response 包含：

~~~
applied
records / etags
prefix_states
view_height
endpoint_id
request_id
~~~

ACK 的作用只有：

- 验证请求结果；
- 完成 outbox；
- 记录必要的 request completion fence。

ACK **不**：

- 写入 confirmed KV；
- 删除 confirmed KV；
- 推进 completed sync generation。

PWA 看到的新确认状态来自后续 ActiveSync/Watch。

因此应用可以显示：

~~~
提交成功 / 等待同步确认
~~~

但不能把 ACK echo 当成本地 authoritative replica。

---

## 9. 网络未知与 Outbox

请求发送后连接中断：

- 保留原始 request；
- 保留原始 request signature；
- 保留原始 prefix generation；
- 重连后不能重新签一个新 generation 给旧 mutation。

如果服务端已经接受原请求，exact retry 返回幂等结果。

如果 generation 已被其他写入推进，旧请求不能恢复已删除或被替换的状态。

---

## 10. Delete

删除通过 Wallet SDK 根据当前 record 构造 signed delete operation。

语义：

~~~
Seq = current.Seq + 1
target = current RecordHash
~~~

服务端提交成功后物理删除 key。

不存在：

~~~
persistent tombstone
delete floor
deleted-key history
~~~

删除后的 ACK 不直接删除 PWA confirmed replica；同步看到当前集合中已经没有该 key 后，再由 replica installer 删除。

之后合法重建：

~~~
Seq = 1
IssueHeight = current / -1 / -2
~~~

---

## 11. Prefix helper APIs

### Status

~~~
~~~

请求：

~~~json
{
  "endpoint_id": "core-node-id",
  "prefixes": [
    {
      "prefix": "/personal/<account>/wallet",
      "generation": 12
    }
  ]
}
~~~

只返回 generation 已变化的 prefix。

### Snapshot

~~~
~~~

返回 endpoint-local 当前集合，包括 FREE_LOCAL。

### Delta

~~~
~~~

返回当前仍存在的 changed records，不是历史事件流。

### Read

~~~
POST /v3/dkvs/prefixes/read
~~~

按需读取 current records，不建立 managed replica。

这些 helper 与 ActiveSync 使用同一 endpoint-local generation 语义。

---

## 12. FREE_LOCAL

FREE_LOCAL：

- 使用当前服务节点 policy；
- 只存在当前 endpoint；
- 进入 Wallet/PWA endpoint-local generation/current snapshot；
- 不进入 P2P；
- TTL 到期物理清理。

Endpoint 切换后，旧节点的 FREE_LOCAL 不可伪装成新节点已同步数据。

---

## 13. DID transfer

Wallet SDK 提供 ResignDIDRecords。

接收 DID 后：

~~~
读取完整 /svc/<did>
+ optional /name/<did>
→ current owner 重签全部 current records
→ one batch-CAS
~~~

不要基于 PWA 本地旧缓存只重签部分 records。

---

## 14. PWA 开发原则

PWA 不自行实现：

~~~
Seq allocation
generation management
request authorization
record signing
fee proof
outbox retry policy
DID takeover batch
delete operation
CAS rebase
~~~

这些统一由 Wallet SDK/WASM 负责。

PWA 只处理：

- 领域 value；
- 用户交互；
- sync/readiness 状态；
- 冲突/费用/绑定错误的产品提示。
