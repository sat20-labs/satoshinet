# DKVS PWA / dApp API Examples

更新时间：2026-07-03

本文给 PWA、dApp 和本地 Agent 提供当前 SatoshiNet indexer DKVS REST API 的最小调用样本。它只描述现有 HTTP/API 行为，不替代钱包签名 SDK，也不引入新的 wire command。

## 约定

- API base URL 示例使用 `http://127.0.0.1:8334`，如果节点配置了 proxy prefix，需要把 prefix 加到 `/v3/dkvs/...` 前。
- DKVS 写入必须提交已签名的 `DKVSRecord`。PWA 不应手工拼接签名；应通过钱包/SDK 构造 record、签名、fee proof 后再提交。
- Go `[]byte` 字段在 JSON 中是 base64 字符串，包括 `Value`、`Data`、`PubKey`、`Signature`、`FeeProof`。
- 默认主网兼容策略下，未配置免费策略或真实 `FeeVerifier` 时，缺少 `FeeProof` 的写入会被拒绝。
- `/name`、`/svc` 和 `/sys` 默认未开放普通写入，除非节点注入真实 DID resolver 或 system verifier。

## Record JSON Shape

```json
{
  "Version": 1,
  "Key": "/personal/<account_id>/profile",
  "Value": "base64-encoded-value",
  "Data": "",
  "PubKey": "base64-encoded-compressed-pubkey",
  "Signature": "base64-encoded-ecdsa-signature",
  "Seq": 1,
  "IssueTime": 1783094400000,
  "TTL": 60000,
  "ExpiryHeight": 100,
  "FeeProof": "base64-encoded-fee-proof-json",
  "Flags": 0
}
```

Tombstone record 使用同一结构，`Value` 为空，`Flags` 包含 `1`。

## Minimal Fetch Wrapper

```js
const dkvsBase = "http://127.0.0.1:8334";

async function dkvsFetch(path, options = {}) {
  const res = await fetch(`${dkvsBase}${path}`, {
    ...options,
    headers: {
      "content-type": "application/json",
      ...(options.headers || {}),
    },
  });
  const body = await res.json();
  if (!res.ok || body.code !== 0) {
    throw new Error(body.msg || `DKVS request failed: ${res.status}`);
  }
  return body;
}
```

## Put / Get / Tombstone

```js
// signedRecord should come from the wallet SDK.
async function putDKVSRecord(signedRecord) {
  const body = await dkvsFetch("/v3/dkvs/records", {
    method: "POST",
    body: JSON.stringify(signedRecord),
  });
  return body.data;
}

async function getDKVSRecord(key) {
  const body = await dkvsFetch(`/v3/dkvs/records?key=${encodeURIComponent(key)}`);
  return body.data;
}

async function getDKVSRecordByHash(recordHash) {
  const body = await dkvsFetch(`/v3/dkvs/records?hash=${encodeURIComponent(recordHash)}`);
  return body.data;
}

async function tombstoneDKVSRecord(signedTombstone) {
  const body = await dkvsFetch("/v3/dkvs/tombstone", {
    method: "POST",
    body: JSON.stringify(signedTombstone),
  });
  return body.data;
}
```

## Prefix List And Usage

```js
async function listDKVSRecords(prefix, start = 0, limit = 100) {
  const qs = new URLSearchParams({ prefix, start: String(start), limit: String(limit) });
  const body = await dkvsFetch(`/v3/dkvs/records/prefix?${qs}`);
  return { records: body.data || [], total: body.total || 0, start: body.start || 0 };
}

async function getDKVSUsage(prefix) {
  const qs = new URLSearchParams({ prefix });
  const body = await dkvsFetch(`/v3/dkvs/usage?${qs}`);
  return body.data;
}
```

## Subscribe / Unsubscribe

Subscription `type` 可为 `key`、`prefix`、`mailbox`、`service`。

```js
async function subscribeDKVS(type, target) {
  const body = await dkvsFetch("/v3/dkvs/subscriptions", {
    method: "POST",
    body: JSON.stringify({ type, target }),
  });
  return {
    initialRecords: body.data || [],
    subscriptions: body.subscriptions || [],
    total: body.total || 0,
  };
}

async function unsubscribeDKVS(type, target) {
  const body = await dkvsFetch("/v3/dkvs/subscriptions", {
    method: "DELETE",
    body: JSON.stringify({ type, target }),
  });
  return body.subscriptions || [];
}

async function listDKVSSubscriptions() {
  const body = await dkvsFetch("/v3/dkvs/subscriptions");
  return body.subscriptions || [];
}
```

Examples:

```js
await subscribeDKVS("key", "/tmp/session-123");
await subscribeDKVS("prefix", "/personal/<account_id>");
await subscribeDKVS("mailbox", "<mailbox_id>");
await subscribeDKVS("service", "wallet");
```

## Mailbox

Mailbox IDs use `hex(sha256(pubkey))`. Message writes can be signed by any valid sender; share writes must be signed by the mailbox owner.

```js
async function readMailboxMessages(mailboxId, start = 0, limit = 100) {
  return listDKVSRecords(`/mail/${mailboxId}/msg`, start, limit);
}

async function readMailboxShares(mailboxId, packageId, start = 0, limit = 100) {
  return listDKVSRecords(`/mail/${mailboxId}/share/${packageId}`, start, limit);
}

async function sendMailboxMessage(signedMailMessageRecord) {
  return putDKVSRecord(signedMailMessageRecord);
}

async function deleteMailboxMessage(signedTombstoneRecord) {
  return tombstoneDKVSRecord(signedTombstoneRecord);
}
```

## Blob / Chunk

Blob data is represented by:

- `/blob/<object_id>/manifest`
- `/blob/<object_id>/chunk/<index>`

The manifest contains content hash, total size, chunk size, chunk count and chunk hashes. PWA clients should verify the manifest and each chunk hash before using the content.

```js
async function getChunkedBlob(objectId) {
  const manifestRecord = await getDKVSRecord(`/blob/${objectId}/manifest`);
  const manifest = JSON.parse(atob(manifestRecord.Value));
  const { records } = await listDKVSRecords(`/blob/${objectId}/chunk`, 0, manifest.chunk_count);
  return { manifest, manifestRecord, chunkRecords: records };
}
```

## Checkpoint And Snapshot

```js
async function getDKVSCheckpoint() {
  const body = await dkvsFetch("/v3/dkvs/checkpoint");
  return body.data;
}

async function exportDKVSSnapshot() {
  const body = await dkvsFetch("/v3/dkvs/snapshot");
  return body.data;
}

async function applyDKVSSnapshot(snapshot) {
  const body = await dkvsFetch("/v3/dkvs/snapshot", {
    method: "POST",
    body: JSON.stringify(snapshot),
  });
  return body.applied || 0;
}
```

## Name And Service Reads

Record-level name reads use `NormalizeNameID(name)` semantics from the Go SDK: DKVS-safe names are used directly; unsafe canonical names map to `hex(sha256(canonical_name))`.

```js
async function getNameRecord(nameId) {
  return getDKVSRecord(`/name/${nameId}`);
}

async function getServiceRecord(serviceName, path) {
  return getDKVSRecord(`/svc/${serviceName}/${path}`);
}

async function subscribeService(serviceName) {
  return subscribeDKVS("service", serviceName);
}
```

真实 DID owner、active 状态和 DKVS signing key 解析仍依赖后续 Ordinals DID resolver 规格；PWA 不应把 record-level `/name/<name_id>` 读取误认为完整 DID 身份验证。
