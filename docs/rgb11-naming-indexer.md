# Contract storage and RGB11 registration boundaries

## SatoshiNet contract storage

DKVS stores opaque application bytes. SatoshiNet does not depend on the RGB11
engine and does not define or decode RGB registrations, Genesis, tickers, asset
types, names or ordinals.

The directory layout remains:

```text
/contract/rgb11/<contract_id>
/contract/evm/source/<contract_address>
```

The first path is chosen and interpreted by the Wallet SDK. Generic DKVS contract
keys allow application directory segments and a final identifier. Only the SDK
requires the RGB identifier to be a nonzero, full lowercase 64-character hex
ContractID. The wire value limit for contract paths is 1 MiB.

Authority-managed contract slots require a signed record from the configured
CoreNode authority. Their storage envelope has `Seq=1`, `TTL=0`, no flags, no
FeeProof, a compressed public key and a nonempty value. Ordinary wallet writes,
CAS writes and deletes cannot create or modify these records. Equal values may
have a refreshed trusted envelope; different values are always rejected, even
when the incoming envelope is older and would otherwise lose generic merge
ordering. Replacement snapshots cannot omit an existing permanent value.

The EVM source verifier remains in `indexer/indexer/evmsource`. It validates
confirmed canonical deployment data, source compilation and the source signer
through the existing verifier hook. Its storage slot keeps its original
per-contract collection and immutability rules.

Opaque authority contracts use their parent directory as the DKVS collection.
For RGB that directory is `/contract/rgb11`. Signature checks, record commits,
PathMeta, change delivery and P2P synchronization are ordinary DKVS operations;
none parse business fields.

## Internal storage API

`POST /v3/dkvs/internal/contract` accepts one signed generic DKVS record through
the existing local-only administration boundary. It delegates to
`IndexerMgr.PutDKVSInternalContract` / `Indexer.PutInternalContract`.

This is a trusted storage API, not an RGB registration API. A caller must validate
its business operation before signing the bytes. The node's former RGB-specific
registration and query routes are removed; unpublished legacy routes and
formats have no compatibility layer.

## Wallet SDK business representation

`sdk/wallet/rgb11/registry.go` owns the compact RGB value:

1. version byte `1`;
2. CompactSize-prefixed UTF-8 `provider_did`;
3. CompactSize-prefixed normalized `ticker`;
4. CompactSize `ordinal`;
5. CompactSize-prefixed standard binary RGB contract file (`RGB\0CON`).

The bytes and path have not changed. Unknown versions, nonminimal lengths,
trailing bytes, invalid Genesis, a mismatched ContractID or ticker, and noncanonical
contract encoding are rejected by the SDK. ContractID and FT/NFT type derive
from Genesis and are not duplicated in storage. The RGB engine contains no
SAT20 naming or DKVS business format.

The registered name remains `rgb11:<type>:<ticker>[_ordinal]@<provider_did>`.
FT `f` and NFT `o` share the same `(provider_did, ticker)` ordinal sequence.
Balance, proof and transfer identity remains the full ContractID. Local names
and mutable Primary DID metadata do not rename an existing registration.

Ordinary wallet reads authenticate the signed DKVS record using the locally
configured CoreNode authority, then validate its RGB contents in the SDK.
Registered-name caches retain signed records and repeat this validation during
restore. A serving endpoint cannot select the authority key. Authentication of
one record does not establish collection completeness or global ordinal order.

DKVS transport and synchronization remain owned by `wallet.dkvsManager`; RGB
business codecs and name projection do not create clients or background workers.
The account service capability bitmap is opaque to the node. The Wallet SDK owns
the meaning of the direct-RGB capability bit.

## Single registration authority

`wallet.RGB11Registrar` is instantiated once by the selected CoreNode registration
service against its local authoritative `RGB11RegistryBackend`. All registrations
must use that instance and authority key. It serializes identity validation,
reading the committed collection, ordinal allocation, signing and committing.

The provider public key must come from an authenticated service request. The
registrar verifies the DID's current active identity and signer ownership. The
calling bridge service must first validate Genesis ownership and bridge/channel
evidence; accepting an arbitrary caller-supplied public key is not authentication.

The registrar validates the full committed RGB collection before allocation:
canonical contracts, authoritative signatures, unique ContractIDs, unique
ordinals and a gap-free sequence beginning at 1. A repeated registration returns
the same result; conflicting provider or contract contents are rejected. A failed
commit consumes no ordinal. Restart reconstructs the next ordinal from the same
authoritative database. No counter, reverse index or registration log is added.

This is a single-writer operational boundary. Separate registrar instances,
processes or nodes must not concurrently allocate ordinals. Generic per-key CAS
cannot serialize allocations for different ContractIDs. DKVS replicas authenticate
opaque records and preserve their bytes; they deliberately do not enforce RGB
ordinal rules. Future support for independent concurrent authorities requires a
separate coordination design.

## Validation and remaining integration

Node tests cover opaque bytes, authority and local administration boundaries,
wire size limits, ordinary-write rejection, immutable values, atomic rejection,
snapshot omissions, P2P state and database reopen behavior.

SDK tests cover the unchanged compact codec, contract and ticker identity,
untrusted/modified responses, full collection gaps/collisions, mixed FT/NFT
allocation, concurrent requests through one registrar, failed commits, retries,
restart, current DID ownership and registered-name recovery.

The existing SDK and PWA RGB E2E suites continue to exercise actual wallet,
proof, balance, transfer and backup behavior. Execution status is recorded in
`sat20wallet/docs/rgb11-mainline-integration-2026-10-08.md`.

Transcend does not yet call this registrar in a production RGB deposit/withdraw
flow. Custody, ingress proof validation, channel splicing and L2 RGB balance
credit/debit remain deferred. This refactor provides the SDK authority operation
and generic node storage; it does not claim those bridge flows are implemented.
