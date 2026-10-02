# RGB11 naming: SatoshiNet indexer implementation

## Scope

This change implements the SatoshiNet **indexer-side** RGB11 naming registry and Primary Ordinals DID binding required before STP/Transcend supports RGB asset deposit/withdraw.

It does **not** implement RGB custody, RGB balance crediting, Transcend deposit/withdraw, private-channel splicing, or RGB consignment validation on bridge ingress. Those remain later STP work.

RGB11 naming is built into the L2 indexer and is active by default. There is no RGB11 feature flag and no `Config.RGB11NamingSource`.

## Identity model

Strict RGB identity is the complete ContractID. No fingerprint is generated or accepted as a second identity.

The registered SatoshiNet asset name is:

```text
rgb11:<existing type>:<baseTicker>[_ordinal]@<providerDID>
```

Examples:

```text
rgb11:f:usdt@tether
rgb11:f:usdt_2@tether
```

Rules:

- asset type keeps its existing meaning;
- provider is the Primary Ordinals DID of the RGB Genesis address;
- DID is 1-10 Unicode code points, canonical lowercase, and is never truncated;
- the first ordinal is 1 and omitted; later values use `_2`, `_3`, ...;
- the ordinal namespace is `(provider DID, normalized base ticker)`;
- ordinals follow successful L2 registration order and are never reused in canonical history;
- a registered `ContractID <-> AssetName` mapping is immutable;
- local SDK labels never enter this registry.

## Primary DID Bind

Primary DID Bind is a generic SatoshiNet identity operation, not an RGB11- or Transcend-specific operation.

The SatoshiNet OP_RETURN content type is:

```text
CONTENT_TYPE_PRIMARYDIDBIND = OP_DATA_45
```

The inner bind payload contains:

```text
DID
public key
DER ECDSA signature
```

The signature covers:

```text
satoshinet-primary-did-bind-v1|<network>|<did>
```

The indexer derives the P2TR address from the supplied public key. This proves control of the SatoshiNet address performing the Bind operation.

The indexer then reuses the existing DKVS/L1 Ordinals DID resolver to resolve the DID. A valid bind requires:

1. the DID passes the <=10-character canonical-name rule;
2. the bind signature is valid;
3. the P2TR address derived from the signing public key is a current owner address returned by the L1 resolver;
4. the L1 resolver reports the DID active;
5. the resolver exposes the current DID owner UTXO.

The owner UTXO is stored as the ownership revision token. If the DID sat moves, its owner UTXO changes, so the old Primary DID Bind becomes inactive. A transfer away and later back to the same address therefore still requires an explicit new Bind.

An address has one Primary DID slot. A later valid Bind replaces the selected DID for future registrations. Existing RGB11 asset names never change.

### L1 replay boundary

The current L1 name endpoint exposes current owner state and owner UTXO. That is sufficient to validate a live Bind and to detect subsequent owner-UTXO changes, but it is **not yet a cryptographic historical ownership proof**.

A production-grade full historical rebuild must eventually consume a replayable L1 ownership proof/attestation pinned to an L1 block, rather than querying wall-clock current ownership for an old L2 Bind. This limitation is explicit; it must not be hidden behind a `verified` boolean.

The persisted SatoshiNet registry remains deterministic after it has been written. This open item concerns rebuilding Primary DID Bind history from genesis on a fresh node.

## Automatic RGB11 registration from transcend.tc

There is no public "register RGB asset name" mutation API.

The first valid RGB11 `transcend.tc` channel-contract deployment is the registration trigger.

The RGB11 SDK adds an immutable registration descriptor to the encoded `transcend.tc` contract content:

```text
marker          = rgb11-reg-v1
BaseTicker      = original RGB ticker
GenesisOutpoint = deterministic RGB Genesis provider outpoint
GenesisAddress  = address resolved from that Genesis outpoint
```

The full ContractID is **not duplicated** in this suffix. It is already the ticker of the signed contract asset identity:

```text
rgb11:<type>:<full ContractID>
```

The indexer derives ContractID from that signed AssetName. This keeps the deploy OP_RETURN within the existing payload limit and avoids redundant on-chain bytes.

The descriptor deliberately does **not** contain:

- Provider DID;
- ordinal;
- final SatoshiNet AssetName.

Those values are assigned by SatoshiNet state.

Because the descriptor is part of the encoded channel-contract content, the existing Transcend deployment invoice signatures cover it.

### L2 indexer recognition

For every accepted L2 block the base indexer scans STP OP_RETURN records.

A `CONTENT_TYPE_DEPLOYCONTRACT` entry is eligible for RGB11 automatic registration only when all of the following hold:

1. it decodes as a `transcend.tc` deployment;
2. the embedded asset is `rgb11:f:<full ContractID>` or the supported RGB11 type;
3. the descriptor ContractID exactly equals that asset ContractID;
4. the contract path is the expected RGB11 asset + `transcend.tc` path;
5. the deployment funds an already known SatoshiNet channel;
6. both existing channel participants' signatures verify over the exact unsigned deploy invoice;
7. descriptor ticker, Genesis outpoint and Genesis address pass canonical validation.

Arbitrary or malformed OP_RETURN data cannot create a registration.

After a candidate passes channel-deploy authentication, the indexer checks the Genesis address's active Primary DID Bind. If it exists and is still backed by the current owner UTXO, the indexer allocates the next ordinal and atomically writes:

```text
ContractID -> Registration
AssetName  -> ContractID
(providerDID, baseTicker) -> maxOrdinal
```

If no valid Primary DID Bind exists, the `transcend.tc` deployment itself remains valid, but canonical RGB11 name registration is skipped. A naming-policy failure must not invalidate an otherwise valid channel-contract deployment.

A repeated Transcend deployment for an already registered ContractID reuses the immutable original mapping and consumes no ordinal.

## Important STP boundary

The automatic registration described above recognizes an authenticated channel-contract deployment and its descriptor. It does **not** prove that an RGB consignment entering the bridge actually matches that descriptor.

When STP/Transcend RGB deposit support is implemented, ingress validation must verify the real RGB consignment/ContractID and its Genesis facts against the registered descriptor before any L2 balance is credited.

Naming registration alone never creates, mints, or credits an RGB asset on SatoshiNet.

## Persistence and rollback

The naming subindex is owned by `BaseIndexer`.

Naming state and base `SyncStats`/UTXO state are staged into the same database write batch. A failed batch retains pending naming writes for retry.

The persisted naming cursor must match the base indexer's persisted height/hash on restart. Reverse-map, counter and provider-lineage corruption fails closed.

Uncommitted fork state can be discarded and replayed using the existing indexer rollback policy. Ordinal non-reuse applies to canonical history, not abandoned branches.

## Read-only RPC

Routes are read-only:

| Method | Route | Purpose |
|---|---|---|
| GET | `/v3/rgb11/naming/status` | Naming snapshot height/hash |
| GET | `/v3/names/primary/:address` | Current active Primary DID |
| GET | `/v3/rgb11/contract/:contractid` | ContractID -> registration |
| GET | `/v3/rgb11/name/:assetname` | AssetName -> registration/ContractID |
| GET | `/v3/rgb11/ordinal?provider=alice&ticker=USD` | Current max ordinal |

There is no HTTP naming mutation route and no ordinal-reservation endpoint.

## SDK integration

Before SatoshiNet registration, the wallet keeps the human-readable RGB11 name as mutable SDK-local metadata.

If the Genesis address has no qualified Primary DID, the default local name is:

```text
ticker@<genesis-address-last-12>
```

The immutable wallet asset key remains the full ContractID projection.

For a validated RGB11 contract, the SDK can build a `TranscendRegistrationDescriptor` from:

- full ContractID;
- original base ticker;
- deterministic Genesis provider outpoint;
- Genesis address.

`TranscendContract.Encode()` requires and appends this descriptor for RGB11 assets. Non-RGB11 Transcend contracts preserve their previous encoding.

## Test coverage

Indexer coverage includes:

- DID 10/11-character boundary;
- signed Primary DID Bind;
- L1 owner-address validation;
- owner-UTXO transfer invalidating an old Bind;
- transfer-away/back requiring rebind;
- signed known-channel `transcend.tc` deployment recognition;
- malformed/unsigned/non-channel deployment refusal;
- automatic `ContractID <-> AssetName` registration;
- same-provider ordinal allocation and immutable mappings;
- missing Primary DID causing registration skip without rejecting the channel deployment;
- shared database batch, restart and RPC snapshot behavior;
- race tests and full indexer regression.

The wallet PR adds:

- local mutable-name identity regression tests;
- RGB11 Transcend descriptor validation and encode/decode round-trip;
- connected SDK e2e coverage for issue -> local name -> local rename -> immutable balance/key -> descriptor creation -> Transcend contract round-trip.

## Deferred work

Not part of this PR:

- RGB deposit/withdraw through Transcend;
- RGB private-channel splicing;
- L2 RGB balance credit/debit;
- RGB consignment validation against the registration descriptor;
- final replayable L1 Ordinals ownership proof format for fresh-node historical Primary DID Bind rebuild.
