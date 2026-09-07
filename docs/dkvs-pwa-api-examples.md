# DKVS Wallet / PWA API Examples

更新时间：2026-08-31

本文只描述 Wallet 应用协议。PWA 业务代码应优先调用 Wallet SDK/WASM 的领域接口，不应自行
管理 DKVS record、Seq、fee proof、prefix generation 或 outbox。

节点间 P2P path sync、checkpoint、全库 snapshot 和节点本地 subscription 管理属于内部或
管理接口，不是 Wallet 同步协议。

## 1. Wallet 应用接口

```text
GET  /v3/dkvs/config
GET  /v3/dkvs/record?key=...
GET  /v3/dkvs/key-state?key=...
POST /v3/dkvs/records/batch-cas
POST /v3/dkvs/prefixes/status
POST /v3/dkvs/prefixes/snapshot
POST /v3/dkvs/prefixes/read
```

以下旧应用接口不再使用：

```text
/v3/dkvs/records
/v3/dkvs/tombstone
/v3/dkvs/records/prefix
/v3/dkvs/sync/directory
/v3/dkvs/watch/directory
/v3/dkvs/subscriptions/snapshot
/v3/dkvs/subscriptions/watch
```

## 2. Fetch wrapper

```js
const dkvsBase = "http://127.0.0.1:8334";

async function dkvsFetch(path, options = {}) {
  const response = await fetch(`${dkvsBase}${path}`, {
    ...options,
    headers: {
      "content-type": "application/json",
      ...(options.headers || {}),
    },
  });
  const body = await response.json();
  if (!response.ok || body.code !== 0) {
    const error = new Error(body.msg || `DKVS request failed: ${response.status}`);
    error.code = body.error_code || "";
    throw error;
  }
  return body.data;
}
```

Go `[]byte` 字段在 JSON 中使用 base64，包括 `Value`、`PubKey`、`Signature` 和 `FeeProof`。

## 3. 读取节点策略

FREE_LOCAL 保存期由当前服务节点决定，端上不能硬编码：

```js
async function getDKVSConfig() {
  return dkvsFetch("/v3/dkvs/config");
}

const config = await getDKVSConfig();
if (!config?.free_local?.enabled || !config.free_local.max_ttl_blocks) {
  throw new Error("The connected endpoint does not provide FREE_LOCAL storage");
}
```

Wallet SDK 在线构造 FREE_LOCAL record 时会使用当前节点策略覆盖调用方 TTL。切换 endpoint 后
必须重新读取；不同 endpoint 不保证含有相同 FREE_LOCAL 数据。

## 4. 单 key 读取

```js
async function getDKVSRecord(key) {
  return dkvsFetch(`/v3/dkvs/record?key=${encodeURIComponent(key)}`);
}

async function getDKVSKeyState(key) {
  return dkvsFetch(`/v3/dkvs/key-state?key=${encodeURIComponent(key)}`);
}
```

`key-state` 返回 `never_seen`、`active` 或 `deleted`，以及用于 per-key CAS 的 `seq`、`etag`。
Wallet SDK 对 unmanaged key 使用 5 秒请求超时和 1 分钟 endpoint-scoped 内存缓存。

## 5. Batch CAS 写入

写入只接受完整的、已签名 record 和 per-key precondition：

```js
async function putDKVSBatch(request) {
  return dkvsFetch("/v3/dkvs/records/batch-cas", {
    method: "POST",
    body: JSON.stringify(request),
  });
}
```

请求示意：

```json
{
  "request_id": "random-request-id",
  "endpoint_id": "required-when-batch-contains-free-local",
  "mutations": [
    {
      "record": {"Version": 1, "Key": "/personal/...", "Seq": 2},
      "precondition": {"expected_hash": "..."}
    }
  ]
}
```

- 全 batch 原子成功或失败；
- `expect_absent=true` 用于从未出现的 key；
- conflict 后必须重新读取全部相关 key，重算业务 value、Seq、ETag 和签名，再用新 request ID 提交；
- 网络失败重试完全相同的已签名请求；
- PAID/AUTOPAY 不允许降级成 FREE_LOCAL；
- 永久协议错误 fail-fast，旧异常 outbox 由维护工具清理。

## 6. Managed prefix 启动同步

Wallet 对自己管理的 canonical prefix 启动时获取完整 snapshot：

```js
async function getPrefixSnapshot(prefix) {
  return dkvsFetch("/v3/dkvs/prefixes/snapshot", {
    method: "POST",
    body: JSON.stringify({ prefix }),
  });
}
```

返回示意：

```json
{
  "endpoint_id": "core-103",
  "prefix": "/personal/<account_id>/wallet",
  "generation": 12,
  "view_height": 3422,
  "records": [],
  "key_states": []
}
```

`generation` 是服务端直接返回的 endpoint-local freshness token。Wallet 只原样持久化和比较，
不能自行计算。snapshot 包含当前 endpoint 可见的全部记录，包括 FREE_LOCAL。

## 7. Managed prefix 定时检查

Wallet 默认每 1 分钟提交已知状态：

```js
async function getChangedPrefixes(endpointId, prefixes) {
  return dkvsFetch("/v3/dkvs/prefixes/status", {
    method: "POST",
    body: JSON.stringify({
      endpoint_id: endpointId,
      prefixes, // [{ prefix, generation }]
    }),
  });
}
```

响应的 `changed` 只包含 generation 不同的 prefix。Wallet 仅重新 snapshot 这些 prefix。
服务端不保存 Wallet session、cursor、change log 或 watcher。

FREE_LOCAL 与其他记录使用完全相同的 status/snapshot 流程；唯一差异是 FREE_LOCAL 不进入
P2P relay，也不进入 canonical P2P generation/root。

## 8. Unmanaged / aggregate prefix 直读

只读、聚合或按需数据使用：

```js
async function readPrefix(prefix) {
  return dkvsFetch("/v3/dkvs/prefixes/read", {
    method: "POST",
    body: JSON.stringify({ prefix }),
  });
}
```

该接口无 generation/cursor 语义，不会创建 managed replica。Wallet SDK 使用 5 秒超时和
1 分钟 endpoint-scoped 内存缓存。

Mailbox/message 属于按需读取：

```js
const result = await readPrefix(`/mail/${recipientAccountId}/msg`);
```

它不会被注册为 Wallet managed prefix。

## 9. Blob

Blob key 为：

```text
/blob/<account_id>/<blob_key>
```

Blob value 是 opaque bytes，DKVS 底层不自动压缩。账户管理等已知领域 codec 可以在加密前
使用有边界、自描述的压缩 envelope。Blob 同样支持 AUTOPAY 或 FREE_LOCAL；FREE_LOCAL 在
本端 status/snapshot 中可见，但不经 P2P relay。

## 10. Record 生命周期

- `IssueHeight` 是可信 SatoshiNet 区块高度；
- 有限记录在 `IssueHeight + TTL` 到期；
- `TTL=0` 表示无固定 record 租期，通常由 AUTOPAY 决定；
- tombstone 使用相同 record 结构，空 Value，Flags 包含 tombstone 位；
- PWA 不手工拼 record 签名，应调用 Wallet SDK/WASM。
