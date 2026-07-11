# DKVS External Integration Contracts

更新时间：2026-07-04

本文定义 SatoshiNet DKVS 与外部 DID、费用合约和系统授权模块的接入契约。它记录当前代码已经支持的接口边界，避免在 DKVS core 内部臆造 Ordinals DID 或 DKVS Pool 合约语义。

## Design Boundary

- DKVS core 负责 key/record/permission/fee/TTL/size/selection/store/sync 的本地校验和存储。
- DKVS core 不直接绑定 L1 Ordinals DID、SNS/referrer 或 DKVS Pool 合约实现。
- 主网未配置真实 resolver / verifier 时，`/name`、`/svc`、`/sys` 默认关闭，缺少 fee proof 的写入默认拒绝。
- 外部接入必须通过接口注入；不得新增 `libp2p` 依赖，不得修改交易、区块、签名、共识、mempool 或 mining 语义。

## DID Resolver Contract

Current interface:

```go
type DIDResolver interface {
    ResolveName(name string) (DIDIdentity, error)
    ResolveService(serviceName string) (DIDIdentity, error)
}

type DIDIdentity struct {
    CanonicalName string
    NameID        string
    SigningKeys   [][]byte
    OwnerAddresses []string
    AddressParams  *chaincfg.Params
    Active        bool
}
```

### Required Semantics

`ResolveName(name)` is used for `/name/<name>`.

`ResolveService(serviceName)` is used for `/svc/<service_name>/...`.

The resolver must return the current DID identity at validation time:

- `CanonicalName`: canonical Ordinals DID name.
- `NameID`: DKVS-safe name id. If canonical name is not DKVS-safe, use `hex(sha256(canonical_name))`.
- `SigningKeys`: current compressed public keys allowed to sign DKVS records for this name or service.
- `OwnerAddresses`: current owner addresses allowed to sign DKVS records. DKVS derives a p2tr address from `record.pubkey` and compares it with this list.
- `Active`: false means no current write permission.

DKVS core then checks:

```text
identity.Active && (
  record.pubkey in identity.SigningKeys ||
  p2tr(record.pubkey) in identity.OwnerAddresses
)
```

For writes, DKVS avoids unnecessary resolver calls:

- if the key is new, `/name` and `/svc` writes must pass resolver validation;
- if the key already exists and the new record uses the same pubkey as the stored record, DKVS treats that pubkey as the established key owner and does not call the resolver;
- if the local indexer has called `NotifyNameTransfers(names)` for this name, the next `/name/<name>` write must resolve again even when the pubkey is unchanged;
- if the key already exists but the pubkey changes, DKVS calls the resolver and the current owner can replace the old record even with lower seq;
- Get/List/Sync/Checkpoint validate stored record shape, signature, TTL and fee, but do not re-resolve `/name` or `/svc` owner on every read.

### Error Semantics

- Resolver unavailable or DID missing: return `ErrDIDResolverUnavailable`.
- DID inactive or signer mismatch: return identity with `Active=false` or signing keys that do not include signer; DKVS returns `ErrPermissionDenied`.
- Internal resolver failure should be propagated as a validation error and must not fall back to open writes.

### Mainnet Safety

The default resolver returns `ErrDIDResolverUnavailable`. This means `/name` and `/svc` remain closed until a real resolver is explicitly configured.

Current SatoshiNet referrer data is not sufficient for this resolver contract because it only carries referrer name and bind height; it does not contain DID owner, active status, DKVS signing key declaration, service mapping, or key rotation semantics.

### Optional HTTP Resolver Adapter

DKVS also provides an optional `HTTPDIDResolver` for integration tests, gateway deployments, or future L1 indexer adapters. It is not enabled by default.

Default endpoints:

```text
GET <base_url>/name/<name_id_or_canonical_name>
GET <base_url>/service/<service_name>
```

The response may be direct:

```json
{
  "canonical_name": "alice",
  "name_id": "alice",
  "signing_keys": ["02...compressed-pubkey-hex"],
  "active": true
}
```

Or wrapped:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "canonical_name": "alice",
    "name_id": "alice",
    "signing_keys": ["02...compressed-pubkey-hex"],
    "active": true
  }
}
```

`signing_keys` should use compressed public keys encoded as hex strings. The adapter also accepts base64 strings for compatibility with Go `[]byte` JSON encoding. The adapter can also accept `owner_addresses` or a single `address` field for p2tr-address-based identity. HTTP 404 maps to `ErrDIDResolverUnavailable`; other non-2xx responses are propagated as resolver errors.

### Optional L1 NS Resolver Adapter

DKVS also provides an optional `L1NSResolver` for the current L1 indexer NS API. It is not enabled by default.

Default endpoints:

```text
GET <base_url>/ns/name/<name>
GET <base_url>/ns/name/<service_name>
```

The resolver expects the current L1 response shape:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "name": "alice",
    "address": "bc1p-or-tb1p-owner-address",
    "utxo": "txid:vout"
  }
}
```

`data.address` is treated as the current owner. DKVS derives the p2tr address from `record.pubkey` using the configured chain params and requires it to match `data.address`. This mirrors the wallet-side `PubKeyToPkScript -> AddrFromPkScript` flow without importing `sat20wallet/sdk` into SatoshiNet.

### Local Name Transfer Notify

When the local indexer observes name transfers in a block, it can call the in-process DKVS interface:

```go
assetIndexer.NotifyDKVSNameTransfers([]string{"alice", "bob.btc"})
```

This is intentionally a local Go interface, not an HTTP or P2P command. It marks each name as requiring resolver validation on the next `/name/<name>` write. The marker is stored in the DKVS DB, so it survives restart. It is cleared after the next write passes resolver validation, even if selector rules make that write a no-op; failed resolver validation leaves the marker in place.

## Fee Verifier Contract

Current interface:

```go
type FeeVerifier interface {
    VerifyFeeProof(
        recordHash [32]byte,
        keyHash [32]byte,
        namespace string,
        recordSize int,
        expiryHeight uint64,
        feeProof []byte,
    ) error
}
```

### Inputs

- `recordHash`: current `FeeAnchorHash(record)`, which is the record hash with `Signature` cleared.
- `keyHash`: `KeyHash(record.Key)`.
- `namespace`: parsed top-level namespace.
- `recordSize`: `RecordSize(record)`.
- `expiryHeight`: record expiry height.
- `feeProof`: raw record `FeeProof` bytes.

### Supported Proof Shape

Current compact binary proof modes:

```text
ONESHOT
LEASE
FREE_LOCAL
AUTOPAY
```

Current encoded fields:

```text
AUTOPAY: pool_contract
FREE_LOCAL: <none>
ONESHOT: pool_contract, payer, payment_txid, paid_amount
LEASE: pool_contract, lease_contract, plan_id
```

`FeeProof` is covered by the record signature. AUTOPAY does not carry a separate payer pubkey or proof signature; the verifier derives the payer p2tr address from `record.PubKey`.

### Required Real Pool Semantics

A production DKVS Pool verifier must validate at least:

- pool contract address is recognized;
- `ONESHOT` payment exists, is confirmed enough, has not been replayed outside allowed policy, and covers namespace, size and expiry;
- `LEASE` contract is active, funded, covers namespace/key scope, size quota and expiry;
- `AUTOPAY` contract is the configured active global `autopay.tc` template contract; the p2tr address derived from `record.PubKey` has an active delegate entry, recipient, service and fee asset match node policy, and that delegate's independent per-block payment covers its own full-size active records;
- `FREE_LOCAL` is accepted only under an explicit local policy, normally not for mainnet public writes;
- mailbox quota and daily message quota are enforced consistently with plan state if the plan is quota-based.

### Default Behavior

The default verifier rejects missing fee proof unless `AllowFreeLocal` is explicitly enabled. This is a mainnet compatibility guard.

The current `JSONFeeVerifier` name is historical; it validates compact fee proof structure for tests/local policy. It is not a DKVS Pool contract verifier.

### AUTOPAY Template Verifier

`AutopayFeeVerifier` is the current contract-backed verifier. It reads contract state through an injected `AutopayStateProvider`; the production path uses `RPCAutopayStateProvider` and calls `getcontractstate`.

For each record it validates:

- proof mode is `AUTOPAY`;
- proof contains a non-empty `pool_contract`;
- `record.PubKey` derives to a p2tr address;
- contract state `templateName` is `autopay.tc`, `status` is `active`, and `closed` is false;
- proof contract equals the configured global DKVS AUTOPAY contract when configured;
- contract service name, recipient and fee asset match node policy;
- the p2tr address derived from `record.PubKey` has an active delegate entry with enough balance for the next payment;
- that delegate's own per-block amount divided by configured full-record fee gives its max full-size-record capacity; usage is indexed by `(contract, delegate)` so delegates cannot consume one another's capacity.

Testnet defaults are hard-coded through `NetworkDefaultsForParams` and identify the global contract policy, service, recipient, fee asset, and full-record fee. They do not grant capacity by themselves: live contract state must contain an active funded delegate entry for the record signer. Mainnet intentionally has no active default AUTOPAY verifier until production parameters and revenue rules are finalized.

### Optional HTTP Fee Verifier Adapter

DKVS also provides an optional `HTTPFeeVerifier`. It is not enabled by default and does not define DKVS Pool contract semantics; it only forwards the current `FeeVerifier` inputs to an external verifier service.

Request:

```http
POST <endpoint>
content-type: application/json
```

```json
{
  "record_hash": "hex-fee-anchor-hash",
  "key_hash": "hex-key-hash",
  "namespace": "personal",
  "record_size": 1024,
  "expiry_height": 100,
  "fee_proof_base64": "base64-encoded-raw-fee-proof"
}
```

Response may be direct:

```json
{
  "valid": true
}
```

Or wrapped:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "valid": true
  }
}
```

Only `valid: true` accepts the write. `valid: false`, non-zero `code`, or non-2xx HTTP status rejects the proof.

## System Verifier Contract

Current interface:

```go
type SystemVerifier interface {
    CanWriteSystem(key string, pubKey []byte) error
}
```

It gates `/sys/*`, including:

- `/sys/params`
- `/sys/miner/<miner_id>`
- `/sys/pool/<pool_id>`
- future system records.

Default behavior is deny-all. A production verifier must define:

- accepted system signer keys or miner/system authority source;
- key scope each signer can write;
- rotation and revocation rules;
- which future system records are writable.

The embedded indexer does not hold system signing private keys and does not auto-publish signed checkpoint/snapshot records. Current checkpoint and snapshot views are unsigned computed API results, not signed `/sys/*` records.

### Optional HTTP System Verifier Adapter

DKVS also provides an optional `HTTPSystemVerifier`. It is not enabled by default and does not define system signer governance semantics; it only forwards the current `SystemVerifier` inputs to an external authority service.

Request:

```http
POST <endpoint>
content-type: application/json
```

```json
{
  "key": "/sys/params",
  "pubkey_hex": "02...compressed-pubkey-hex",
  "pubkey_base64": "base64-encoded-compressed-pubkey"
}
```

Response may be direct:

```json
{
  "valid": true
}
```

Or wrapped:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "valid": true
  }
}
```

Only `valid: true` accepts the `/sys/*` write. `valid: false`, non-zero `code`, or non-2xx HTTP status rejects the write.

### Checkpoint and Snapshot Boundary

DKVS checkpoint and snapshot are unsigned computed views of the local active record set. They are exposed for node-to-node comparison, debugging and snapshot validation only. The embedded indexer does not hold private keys, does not create signed checkpoint records, and does not provide an HTTP checkpoint anchorer adapter.

Real chain anchoring remains undefined. A future anchoring service or system contract must define its own transaction format, authority model, retry policy and finality rules. Until that protocol exists, checkpoint mismatch is an operational signal to continue syncing, retry, or alert; it is not a consensus failure.

## Configuration Requirements

Production DKVS configuration should explicitly provide:

```go
dkvs.Config{
    Resolver:       realDIDResolver,
    FeeVerifier:    realPoolFeeVerifier,
    SystemVerifier: realSystemVerifier,
    CurrentHeight:  chainHeightFunc,
}
```

For the embedded SatoshiNet indexer, the same integrations can be injected after initialization:

```go
indexer.Config{
    DKVS: &indexer.DKVSIntegrationConfig{
        Resolver:       realDIDResolver,
        FeeVerifier:    realPoolFeeVerifier,
        SystemVerifier: realSystemVerifier,
    },
}
```

If the deployment uses HTTP gateway services and does not need custom in-process implementations, the embedded indexer can construct the adapters directly:

```go
indexer.Config{
    DKVS: &indexer.DKVSIntegrationConfig{
        ResolverHTTPBaseURL:     "http://127.0.0.1:18080/did",
        ResolverHTTPNamePath:    "/name/",
        ResolverHTTPServicePath: "/service/",
        FeeVerifierHTTPEndpoint: "http://127.0.0.1:18081/dkvs/fee/verify",
        SystemVerifierHTTPEndpoint: "http://127.0.0.1:18082/dkvs/system/verify",
    },
}
```

Explicit interface fields take precedence over URL fields. Leaving both unset preserves the conservative defaults.

They can also be replaced at runtime:

```go
assetIndexer.SetDKVSResolver(realDIDResolver)
assetIndexer.SetDKVSFeeVerifier(realPoolFeeVerifier)
assetIndexer.SetDKVSSystemVerifier(realSystemVerifier)
```

Leaving any of these nil preserves the conservative defaults:

- name/service writes unavailable;
- system writes denied;
- missing fee proof rejected unless free-local mode is explicitly enabled.

Calling the runtime setters with nil also restores the conservative defaults.

## Compatibility Requirements

Any future implementation of these contracts must preserve:

- existing 6 DKVS wire commands;
- no `libp2p` dependency;
- remote records are fully revalidated before local storage;
- Get/List/Sync/Checkpoint return only active, unexpired, currently-authorized records;
- no consensus, transaction, block, signature, mempool or mining semantic changes unless a separate protocol upgrade is explicitly designed and reviewed.
