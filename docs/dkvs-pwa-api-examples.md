# DKVS PWA / dApp API Examples

更新时间：2026-07-03

本文给 PWA、dApp 和本地 Agent 提供当前 SatoshiNet indexer DKVS REST API 的最小调用样本。它只描述现有 HTTP/API 行为，不替代钱包签名 SDK，也不引入新的 wire command。

当前 `sat20wallet/pwa` 已提供 DKVS developer tool 页面：`/wallet/dkvs`。该页面只调用现有 REST API，支持 key/hash 查询、prefix 列表、提交已签名 record、提交 tombstone 和读取 checkpoint；record 签名与 fee proof 仍应由钱包/SDK 或外部流程生成。

## 约定

- API base URL 示例使用 `http://127.0.0.1:8334`，如果节点配置了 proxy prefix，需要把 prefix 加到 `/v3/dkvs/...` 前。
- DKVS 写入必须提交已签名的 `DKVSRecord`。PWA 不应手工拼接签名；应通过钱包/SDK 构造 record、附加 fee proof、签名后再提交。
- Go `[]byte` 字段在 JSON 中是 base64 字符串，包括 `Value`、`PubKey`、`Signature`、`FeeProof`。
- 默认主网兼容策略下，未配置免费策略或真实 `FeeVerifier` 时，缺少 `FeeProof` 的写入会被拒绝。
- `/name`、`/svc` 和 `/sys` 默认未开放普通写入，除非节点注入真实 DID resolver 或 system verifier。

## Record JSON Shape

```json
{
  "Version": 1,
  "Key": "/personal/<account_id>/profile",
  "Value": "base64-encoded-value",
  "PubKey": "base64-encoded-compressed-pubkey",
  "Signature": "base64-encoded-ecdsa-signature",
  "Seq": 1,
  "IssueTime": 1783094400000,
  "TTL": 60000,
  "ExpiryHeight": 100,
  "FeeProof": "base64-encoded-compact-fee-proof",
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

Mailbox IDs and sender IDs use `hex(sha256(pubkey))`. A message key is `/mail/<mailbox_id>/msg/<sender_id>/<msg_id>`, and `<sender_id>` must match the record signing public key. The sender's AUTOPAY delegate pays for message storage; the recipient does not need a delegate and may submit a fee-free signed tombstone. Share writes remain mailbox-owner paid and signed. Mailbox messages are constrained by both mailbox-wide and per-sender quotas.

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

## Single-record Blob

Blob uses one signed record:

- `/blob/<account_id>/<blob_key>`

The value is opaque bytes. Normal DKVS values remain limited to 16 KiB; Blob values may be up to 1 MiB. The key owner is the account encoded by `account_id`. Blob supports both `AUTOPAY` and node-local `FREE_LOCAL`; the connected node's `/v3/dkvs/config` response defines the applicable FREE_LOCAL TTL, byte, record and distinct-Blob-key limits.

```js
async function getBlob(accountId, blobKey) {
  return getDKVSRecord(`/blob/${accountId}/${blobKey}`);
}

async function putSignedBlob(signedBlobRecord) {
  return putDKVSRecord(signedBlobRecord);
}
```

For application synchronization, use the directory RPC rather than the node-to-node P2P protocol:

```js
async function syncDKVSDirectory(prefix, cursor = null, limit = 100) {
  const body = await dkvsFetch("/v3/dkvs/sync/directory", {
    method: "POST",
    body: JSON.stringify({ prefix, cursor, limit }),
  });
  return body.data;
}

async function watchDKVSDirectory(prefix, root, timeoutSeconds = 20) {
  const body = await dkvsFetch("/v3/dkvs/watch/directory", {
    method: "POST",
    body: JSON.stringify({
      prefix,
      root,
      timeout_seconds: timeoutSeconds,
    }),
  });
  return body.data;
}
```

Applications that must update multiple keys together should submit a signed atomic batch-CAS request to `/v3/dkvs/records/batch-cas`; any failed precondition or validation rejects the entire batch.

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
