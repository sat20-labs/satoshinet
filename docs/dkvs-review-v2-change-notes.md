# DKVS Review V2 Change Notes

This development branch implements the agreed review follow-up without changing transaction, block, mempool, mining, or consensus semantics.

## Included

- Treat signed tombstones as delete commands: active records are physically removed, missing-key deletes are no-ops, and a compact max-sequence watermark prevents stale resurrection.
- Keep the signed delete record only for a bounded notify/get/data relay window.
- Add one internal `PathMeta` aggregate per business collection path for active count, bytes, and generation.
- Move external DID, system, and fee-capacity validation outside the global DKVS write lock, followed by an optimistic locked recheck and atomic batch commit.
- Sync one subscribed path at a time, coalesce changes that occur during the base scan, and send them before the final sync response.
- Use authoritative mirror cleanup only for ordinary nodes syncing from miner peers; miner-to-miner synchronization remains merge-only.
- Invalidate both `/name` and `/svc` records after a name ownership transfer until revalidated.
- Require non-zero TTL for mailbox message/share records.
- Fail closed for local administration middleware when `RemoteAddr` is unavailable.

## Validation

The temporary branch workflow runs:

- focused DKVS, wire, peer, indexer, RPC, and root-package tests;
- `go test -race ./indexer/indexer/dkvs`;
- `go test ./...`.

The workflow commits the wired changes only after all checks pass. The temporary workflow and patch script are removed after validation.

## Deliberately deferred protocol changes

Snapshot shadow-database import, immutable blob-version keys, mainnet AUTOPAY lifecycle semantics, and a versioned checkpoint/root format remain separate protocol decisions. They are not mixed into this storage/sync refactor.
