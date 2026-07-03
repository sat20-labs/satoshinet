# DKVS External Integration Contracts

更新时间：2026-07-03

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
- `Active`: false means no current write permission.

DKVS core then checks:

```text
record.pubkey in identity.SigningKeys && identity.Active
```

If DID state changes, active filtering is dynamic:

- old records signed by no-longer-authorized keys stop being returned by Get/List/Sync/Checkpoint;
- a new authorized owner can replace the old active candidate even with lower seq;
- if the old record is still authorized, existing seq/expiry/hash selection rules apply.

### Error Semantics

- Resolver unavailable or DID missing: return `ErrDIDResolverUnavailable`.
- DID inactive or signer mismatch: return identity with `Active=false` or signing keys that do not include signer; DKVS returns `ErrPermissionDenied`.
- Internal resolver failure should be propagated as a validation error and must not fall back to open writes.

### Mainnet Safety

The default resolver returns `ErrDIDResolverUnavailable`. This means `/name` and `/svc` remain closed until a real resolver is explicitly configured.

Current SatoshiNet referrer data is not sufficient for this resolver contract because it only carries referrer name and bind height; it does not contain DID owner, active status, DKVS signing key declaration, service mapping, or key rotation semantics.

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

- `recordHash`: `FeeAnchorHash(record)`, not the final `RecordHash(record)`. This avoids fee proof self-reference and signature cycles.
- `keyHash`: `KeyHash(record.Key)`.
- `namespace`: parsed top-level namespace.
- `recordSize`: `RecordSize(record)`.
- `expiryHeight`: record expiry height.
- `feeProof`: raw record `FeeProof` bytes.

### Supported Proof Shape

Current JSON proof modes:

```text
ONESHOT
LEASE
FREE_LOCAL
```

Current fields:

```text
mode
pool_contract
payer
payer_pubkey
payment_txid
lease_contract
plan_id
key_hash
record_hash
record_size
expiry_height
namespace
paid_amount
proof_signature
```

`proof_signature` signs the canonical fee proof fields and excludes `proof_signature` itself.

### Required Real Pool Semantics

A production DKVS Pool verifier must validate at least:

- pool contract address is recognized;
- `ONESHOT` payment exists, is confirmed enough, has not been replayed outside allowed policy, and covers namespace, size and expiry;
- `LEASE` contract is active, funded, covers namespace/key scope, size quota and expiry;
- `FREE_LOCAL` is accepted only under an explicit local policy, normally not for mainnet public writes;
- proof payer and optional proof signature are valid under the final contract rules;
- mailbox quota and daily message quota are enforced consistently with plan state if the plan is quota-based.

### Default Behavior

The default verifier rejects missing fee proof unless `AllowFreeLocal` is explicitly enabled. This is a mainnet compatibility guard.

The current `JSONFeeVerifier` is a structured payload verifier and test/local helper. It is not a DKVS Pool contract verifier.

## System Verifier Contract

Current interface:

```go
type SystemVerifier interface {
    CanWriteSystem(key string, pubKey []byte) error
}
```

It gates `/sys/*`, including:

- `/sys/checkpoint/<epoch>`
- `/sys/snapshot/<epoch>`
- future system parameter records.

Default behavior is deny-all. A production verifier must define:

- accepted system signer keys or miner/system authority source;
- key scope each signer can write;
- rotation and revocation rules;
- whether checkpoint/snapshot records are manually published, miner-published, or generated by another system component.

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

Leaving any of these nil preserves the conservative defaults:

- name/service writes unavailable;
- system writes denied;
- missing fee proof rejected unless free-local mode is explicitly enabled.

## Compatibility Requirements

Any future implementation of these contracts must preserve:

- existing 6 DKVS wire commands;
- no `libp2p` dependency;
- remote records are fully revalidated before local storage;
- Get/List/Sync/Checkpoint return only active, unexpired, currently-authorized records;
- no consensus, transaction, block, signature, mempool or mining semantic changes unless a separate protocol upgrade is explicitly designed and reviewed.
