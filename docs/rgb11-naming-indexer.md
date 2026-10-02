# RGB11 naming: SatoshiNet indexer implementation

## Scope

This change implements the **indexer-side state machine and persistence**, following `sat20wallet/docs/rgb11-asset-naming-design.md` and wallet PR #10. It does not implement RGB custody, Transcend deposit/withdraw, private-channel operations, RGB balance crediting, or a production naming transaction/proof adapter.

The current deployment has no `RGB11NamingSource`. It indexes empty naming effects and reports `source_configured=false`. This is deliberate: arbitrary OP_RETURN data, a normal transfer recipient, an unsigned RPC request, or caller-supplied `verified=true` must not be able to assign a provider identity.

The future STP Transcend-channel integration supplies verified effects through the existing block-indexing lifecycle. This is an internal integration contract, **not a signature or RGB validation implementation**. A complete, externally usable Bind/registration flow still needs that adapter and the corresponding transaction/wallet entrypoints.

## Identity and names

- Strict identity: all 32 bytes of the RGB ContractID, encoded as **64 lowercase hex characters** at the indexer boundary. This is lossless encoding, not a fingerprint or another hash. An RGB adapter decodes the standard RGB textual ContractID before calling this API.
- Registered name: `rgb11:<existing asset type>:<normalized ticker>[_ordinal]@<canonical DID>`.
- The first ordinal is 1 and is omitted in the name. The second is `_2`.
- Asset type retains its existing meaning (`f` or `n`); it is not a provider/locator field.
- Counter namespace: `(canonical provider DID, normalized base ticker)`, shared across asset types.
- Normalization matches the wallet implementation (lowercase ASCII alphanumeric plus hyphens). Raw `_digits` suffixes, `@` and `:` are rejected before normalization. Ticker input is bounded to 1024 bytes.
- No fingerprint generation, collision extension, old-name compatibility, local-address fallback or editable SDK label enters this registry.
- Existing canonical-history mappings are never deleted or reassigned. Repeating an already registered ContractID returns its original mapping, without spending another ordinal or consulting the address's newly selected DID. Conflicting genesis/ticker/type facts are rejected.

## Trusted event source

`rgb11names.EventSource.RGB11NamingEvents(*common.Block)` is configured once at indexer startup using `Config.RGB11NamingSource`. It is inherited by validation and RPC snapshots and cannot be replaced after block processing starts.

The source MUST:

1. Return effects only for transactions already accepted by chain/contract validation.
2. Authenticate Bind actions to the address controlling the DID. For a new registration, authenticate the genesis-address authorization bound to the full ContractID; merely referencing another party's outpoint is not proof of control.
3. Verify the complete RGB ContractID ↔ genesis outpoint/address ↔ original ticker/type relationship. Never use a transfer's current recipient/terminal outpoint instead of genesis.
4. Verify Ordinals DID existence, canonical spelling, immutable sat lineage, and ownership against an immutable, hash-identified L1 view.
5. Emit every ownership change, including transfer away and back. `Ownership.Revision` advances for a change; it does not advance merely for a new confirmation/proof of the same owner.
6. Return the same ordered effects when a block is replayed. Do not query the latest wall-clock HTTP state while replaying historical blocks.
7. Filter invalid user actions according to the upstream protocol before returning approved effects. An invalid *approved* effect or a missing dependency is an integration error and stops the indexer before advancing its base state.
8. Avoid synchronous calls back into the compiling indexer; the source executes under its snapshot lock.

These requirements are intentionally not replaced with boolean flags. This PR does not expose a registration mutation RPC. Once a database has naming effects, restarting without its source permits historical reads but blocks further indexing instead of silently retaining stale ownership state.

## Indexed effects

Each event is matched against its block transaction ID and transaction index. Events are strictly ordered by `(tx_index, event_index)`; the indexer rejects duplicate/reordered positions rather than silently sorting them. Bind/registration effects cannot originate from a coinbase transaction. At most 4096 naming events are accepted per block.

### Ownership

`Ownership { DID, Sat, Address, Revision, L1Height, L1Hash }`

The source supplies verified facts. The indexer checks canonical addresses/network, monotonic revision and L1 height, and stable DID→sat identity. Conflicting hashes at the same L1 height are rejected. The same revision cannot change owner. Empty owner means explicitly inactive/burned.

### Primary DID Bind

`Bind { DID, Address }`

The canonical DID must contain **1–10 Unicode code points**, use canonical lowercase spelling, and contain no whitespace, control/format characters or reserved naming separators. It is never truncated. The current indexed DID owner must equal the binding address.

An address has one primary slot. Binding another owned DID replaces that slot. The binding captures the ownership revision; a transfer invalidates it, including transfer away and back. The new owner must Bind explicitly. A later proof of the same owner/revision does not invalidate the binding.

### Registration

`Register { ContractID, BaseTicker, AssetType, GenesisOutpoint, GenesisAddress, AuthorizedBy }`

For first registration:

- the authenticated address must equal the canonical genesis address;
- that address must have an active primary DID whose current indexed ownership revision still matches;
- the indexer allocates the next ordinal in the normalized provider/ticker namespace;
- the immutable registration, reverse AssetName→ContractID mapping, and counter are staged together.

Provider DID/sat, original normalized ticker/type, genesis facts, ordinal and registration position are captured. Later Bind changes or DID transfers cannot rename a historical registration. The DID's next owner continues its namespace; counters do not restart at a new address.

The indexer does not create a `TickerInfo` balance entry, mint/credit an asset, or alter any existing STP asset gate. Naming registration alone is never proof that an asset is deposited or spendable on SatoshiNet.

## Atomic persistence and snapshots

`BaseIndexer` owns the naming subindex. Naming effects are applied before the base mutates the corresponding block. An event-source failure or invalid effect leaves both naming cursor and base height unchanged. Within a naming block, either all effects are applied or none are; a failed block consumes no ordinal.

`Index.Stage(baseWriteBatch)` adds naming data to the **same batch** as UTXOs and `SyncStats`. The returned acknowledgment is invoked only after successful Flush. Failed stage/flush retains the pending naming writes.

Buffer snapshots copy immutable metadata values. Unlike a live lookup that falls back to a newer database state, an older RPC snapshot remains unchanged after subsequent live flushes. Naming deltas are not subtracted before the backup flush; acknowledgment removes only matching pending versions, preserving newer live updates.

On restart, the persisted naming cursor must equal the base indexer's persisted sync height/hash. A mismatch, corrupt reverse mapping, orphan counter or inconsistent provider lineage fails closed. An existing pre-feature database with no naming namespace starts from its existing base checkpoint; no historical fingerprint-name migration is introduced.

Tests use the repository's default Pebble backend. The persistence boundary requires a transactional batch backend; a backend that splits a batch into independently visible transactions must not be used to claim the same crash-atomicity guarantee without additional validation.

As with the existing base indexer, unflushed/uncommitted fork state may be dropped and replayed from the durable checkpoint. Ordinal non-reuse applies to canonical history, not to abandoned branches. No new deep-reorg policy is introduced.

Naming metadata is kept in memory separately from the much larger UTXO/asset ledger. RPC and flush clones copy metadata map membership; values are immutable. There are no mutable record pointers returned to callers.

## Read-only HTTP API

Routes are under the existing network/proxy prefix:

| Method | Route | Purpose |
|---|---|---|
| GET | `/v3/rgb11/naming/status` | Indexed height/hash and whether a source is configured |
| GET | `/v3/names/primary/:address` | Active primary DID and its hash-identified L1 ownership fact |
| GET | `/v3/rgb11/contract/:contractid` | Full ContractID → immutable registration |
| GET | `/v3/rgb11/name/:assetname` | Full AssetName → immutable registration/ContractID |
| GET | `/v3/rgb11/ordinal?provider=alice&ticker=USD` | Current maximum ordinal; read only, not a reservation |

Success uses `{ "code": 0, "msg": "ok", "data": ... }`. `data.indexed_at` supplies the exact snapshot height/hash. Invalid input returns 400, a missing/inactive record returns 404, and unavailable/corrupt state returns 503. A backend without the optional naming reader capability returns 503 rather than pretending to have an empty authoritative registry.

There are no POST/PUT/DELETE naming routes. `source_configured=true` identifies an installed integration; it is not a claim that an untrusted source is cryptographically verified by this module.

## Test coverage

The new tests cover:

- 10/11-character and Unicode DID boundaries, reserved ticker syntax and canonical formatting;
- active owner/address validation, replacement of a primary DID, and normalized-ticker namespace collisions;
- multiple same-block registrations, ordinal allocation and repeated ContractID idempotency;
- DID transfer, transfer-back without automatic rebind, and historical provider/name freeze;
- incorrect authorization, changed genesis/ticker/type, malformed event unions/order/transaction identity;
- all-or-nothing event failure, counter overflow, collision/corruption refusal and replay determinism;
- failed storage stage/flush followed by retry, persisted base/name cursor matching, restart and unflushed replay;
- old RPC snapshots after newer flushes, copy isolation and concurrent readers under the race detector;
- actual BaseIndexer block processing and shared-batch flush/restart, with authenticated-effect fixtures;
- registry→real database→restart→production Gin routes, plus refusal of all mutation HTTP methods.

These are indexer/storage/RPC integration tests. They do **not** claim to validate a real Transcend deposit/withdraw, an Ordinals proof oracle or an issuer signature implementation.

## Next integration (not part of this change)

A production adapter should derive these effects from the selected validated contract/chain mechanism, supply replayable L1 ownership facts, and authenticate genesis-address authorization. Wallet Bind/registration entrypoints then submit the corresponding actions. The later Transcend channel implementation can reuse existing mappings when assets enter/exit, keeping custody verification and naming assignment separate.
