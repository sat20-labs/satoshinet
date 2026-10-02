# RGB11 naming on SatoshiNet

## Scope

This change provides the SatoshiNet-side storage and query primitives required by future STP/Transcend RGB11 ingress/egress.

It does **not** implement RGB custody, RGB balance crediting, Transcend deposit/withdraw, private-channel splicing, or RGB consignment validation.

RGB11 naming state is stored in DKVS. It is **not** reconstructed from SatoshiNet block replay.

## Identity model

Strict RGB identity remains the complete ContractID.

The canonical SatoshiNet asset name is:

```text
rgb11:f:<baseTicker>[_ordinal]@<providerDID>
```

Examples:

```text
rgb11:f:usdt@tether
rgb11:f:usdt_2@tether
```

Rules:

- `f` keeps its existing asset-type meaning;
- provider is an Ordinals DID;
- a DID must be at most 10 Unicode code points to be selected as Primary DID;
- the first ordinal is 1 and is omitted from the displayed name;
- later assets use `_2`, `_3`, ...;
- the ordinal namespace is `(providerDID, normalized baseTicker)`;
- ContractID is never shortened to a fingerprint;
- once a ContractID is registered, its canonical AssetName is immutable.

## Primary DID

Primary DID is ordinary account metadata stored in DKVS:

```text
/personal/<account_id>/primary_did
```

The record value is only the canonical DID string.

No inscription ID, owner address, owner UTXO or duplicate DID metadata is stored in this personal record because the L1 Indexer already resolves current DID ownership and inscription data.

A write is accepted only when:

1. the DKVS record is signed by the account itself;
2. the DID is canonical lowercase and no longer than 10 Unicode code points;
3. the configured L1 DID resolver reports the DID active;
4. that DID is currently owned by the P2TR address derived from the signing account key.

Deleting the personal parameter is allowed by the account owner.

Primary DID remains mutable. Changing it does not rename any already registered RGB11 asset.

## RGB11 registry storage

Canonical RGB11 registrations are DKVS network state under:

```text
/rgb11/<providerDID>/<baseTicker>/<ordinal>
```

Example:

```text
/rgb11/tether/usdt/1
/rgb11/tether/usdt/2
```

The value is exactly the raw 32-byte ContractID.

Nothing else is repeated in the value:

- provider is already in the key;
- base ticker is already in the key;
- ordinal is already in the key;
- AssetName is deterministically derived from the key;
- ContractID is stored as the only value because it cannot be derived from the other fields.

This representation keeps the registry compact and makes each successful ordinal an immutable DKVS fact.

## Registry invariants

For each `(providerDID, baseTicker)` namespace:

- ordinal starts at 1;
- there are no gaps;
- an ordinal cannot be reused;
- an existing key cannot be changed to another ContractID;
- the same ContractID cannot appear in another RGB11 registry key;
- registry records are permanent (`TTL=0`) and cannot be tombstoned;
- ordinary wallet DKVS writes cannot create registry entries.

The derived name is:

```text
ordinal == 1:
  rgb11:f:<ticker>@<provider>

ordinal > 1:
  rgb11:f:<ticker>_<ordinal>@<provider>
```

## Registration authority

The RGB11 registry is not directly writable by ordinary wallets.

A future Transcend/STP registration flow calls the node-internal RGB11 registry write after it has validated the bridge/channel operation and the relevant RGB11 facts.

The node-internal path requires a signed DKVS record and is exposed only through the local CoreNode administration boundary. The HTTP route is loopback-only and additionally checks that the record signer is a known CoreNode.

This PR deliberately does not implement the Transcend deposit/withdraw flow itself.

## Relationship to transcend.tc

A future first RGB11 Transcend ingress should follow this order:

1. determine the RGB ContractID and immutable Genesis facts from the RGB11 contract;
2. read the Genesis address account's `primary_did`;
3. use the L1 Indexer to verify that the DID currently belongs to the Genesis address;
4. allocate the next RGB11 ordinal through the trusted Transcend/STP registration path;
5. persist the immutable registry record in DKVS;
6. use the resulting canonical AssetName for the SatoshiNet/Transcend operation.

The Transcend deploy payload therefore does **not** need to duplicate naming metadata that already exists in DKVS or can be obtained from RGB/L1 state.

In particular, no RGB11 naming descriptor is required in the contract deployment merely to reconstruct names later.

## DKVS synchronization and block replay

RGB11 naming belongs to DKVS, not BaseIndexer block state.

A node that restores or rebuilds follows this conceptual order:

```text
DKVS synchronization
    ↓
RGB11 registry available
    ↓
SatoshiNet block replay / ordinary index reconstruction
```

SatoshiNet block replay does not:

- allocate RGB11 ordinals;
- recreate ContractID/AssetName mappings;
- query historical Primary DID state;
- rename RGB11 assets.

This avoids making canonical asset names depend on historical replay order or current external DID state.

## Query API

Read-only RGB11 queries are derived from the synchronized DKVS registry:

| Method | Route | Purpose |
|---|---|---|
| GET | `/v3/rgb11/naming/status` | reports that DKVS is the naming source |
| GET | `/v3/rgb11/contract/:contractid` | ContractID -> canonical registration |
| GET | `/v3/rgb11/name/:assetname` | AssetName -> ContractID/registration |
| GET | `/v3/rgb11/ordinal?provider=alice&ticker=USD` | number of allocated ordinals |

Primary DID is read through the existing DKVS personal record API, not through a second RGB11-specific Primary DID state table.

## Wallet behavior before registration

Before an RGB11 asset enters SatoshiNet, its human-readable name is SDK-local metadata and may change.

If no qualified DID is available, the default local label is:

```text
ticker@<genesis-address-last-12>
```

The strict wallet identity remains the complete ContractID.

Changing a local label or Primary DID before SatoshiNet registration does not alter RGB state, balances, proofs or ContractID.

## Test coverage

The SatoshiNet tests cover:

- Primary DID 10/11-character boundary;
- account-owned `/personal/.../primary_did` write;
- rejection when the DID belongs to another address;
- RGB11 registry key/value canonicalization;
- immutable ordinal records;
- gap rejection;
- duplicate ContractID rejection;
- ordinary DKVS write rejection;
- node-internal registration and idempotent retry;
- DKVS PathSnapshot synchronization of RGB11 registry state;
- ContractID -> AssetName and AssetName -> ContractID queries;
- local-only CoreNode registration endpoint authorization;
- full indexer package regression.

The wallet tests cover local RGB11 naming and Primary DID SDK behavior. STP/Transcend RGB deposit/withdraw e2e remains deferred until that feature is implemented.

## Deferred work

Not part of this PR:

- RGB11 Transcend deposit/withdraw;
- RGB11 private-channel splicing;
- RGB consignment validation on SatoshiNet ingress;
- L2 RGB balance credit/debit;
- calling the internal RGB11 DKVS registration from the Transcend service;
- final STP e2e that proves registration + deposit + withdrawal.
