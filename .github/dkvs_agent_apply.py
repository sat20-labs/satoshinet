#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import re
import textwrap

ROOT = Path(__file__).resolve().parents[1]


def read(rel: str) -> str:
    return (ROOT / rel).read_text()


def write(rel: str, text: str) -> None:
    (ROOT / rel).write_text(text)


def go_brace_end(text: str, open_index: int) -> int:
    depth = 0
    i = open_index
    state = "code"
    while i < len(text):
        c = text[i]
        n = text[i + 1] if i + 1 < len(text) else ""
        if state == "code":
            if c == '"':
                state = "string"
            elif c == "'":
                state = "rune"
            elif c == "`":
                state = "raw"
            elif c == "/" and n == "/":
                state = "line_comment"
                i += 1
            elif c == "/" and n == "*":
                state = "block_comment"
                i += 1
            elif c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
                if depth == 0:
                    return i
        elif state in ("string", "rune"):
            if c == "\\":
                i += 1
            elif (state == "string" and c == '"') or (state == "rune" and c == "'"):
                state = "code"
        elif state == "raw":
            if c == "`":
                state = "code"
        elif state == "line_comment":
            if c == "\n":
                state = "code"
        elif state == "block_comment":
            if c == "*" and n == "/":
                state = "code"
                i += 1
        i += 1
    raise RuntimeError("unmatched Go brace")


def replace_func(rel: str, signature: str, replacement: str) -> None:
    text = read(rel)
    start = text.find(signature)
    if start < 0:
        raise RuntimeError(f"function signature not found in {rel}: {signature}")
    open_index = text.find("{", start + len(signature) - 1)
    end = go_brace_end(text, open_index)
    new = textwrap.dedent(replacement).strip() + "\n"
    write(rel, text[:start] + new + text[end + 1 :].lstrip("\n"))


def insert_struct_fields(rel: str, declaration: str, fields: str) -> None:
    text = read(rel)
    start = text.find(declaration)
    if start < 0:
        raise RuntimeError(f"struct not found: {declaration}")
    open_index = text.find("{", start)
    end = go_brace_end(text, open_index)
    normalized = textwrap.dedent(fields).strip("\n") + "\n"
    if normalized.strip() in text[open_index:end]:
        return
    write(rel, text[:end] + "\t" + normalized.replace("\n", "\n\t").rstrip("\t") + text[end:])


def replace_once(rel: str, old: str, new: str) -> None:
    text = read(rel)
    if old not in text:
        raise RuntimeError(f"text not found in {rel}: {old[:100]!r}")
    write(rel, text.replace(old, new, 1))


def regex_replace_once(rel: str, pattern: str, repl: str) -> None:
    text = read(rel)
    new, count = re.subn(pattern, repl, text, count=1, flags=re.MULTILINE)
    if count != 1:
        raise RuntimeError(f"pattern count={count} in {rel}: {pattern}")
    write(rel, new)


# Fix imports in the newly added path sync implementation.
path_sync = read("indexer/indexer/dkvs/path_sync.go")
path_sync = path_sync.replace('"sort"\n\t"time"\n\n\tindexercommon "github.com/sat20-labs/indexer/common"', '"sort"\n\t"sync/atomic"\n\t"time"')
path_sync = path_sync.replace("\nvar _ indexercommon.WriteBatch\n", "\n")
write("indexer/indexer/dkvs/path_sync.go", path_sync)

# Indexer state required by optimistic writes and buffered path sync.
insert_struct_fields(
    "indexer/indexer/dkvs/indexer.go",
    "type Indexer struct {",
    """
policyGeneration uint64
pathSyncMutex    sync.Mutex
pathSyncs         map[uint64]*pathSyncSession
""",
)
regex_replace_once(
    "indexer/indexer/dkvs/indexer.go",
    r"(sourceNode:\s+cfg\.SourceNode,\n)(\s*})",
    r"\1\t\tpathSyncs:   make(map[uint64]*pathSyncSession),\n\2",
)

replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) SetResolver(resolver DIDResolver) {",
    r'''
func (i *Indexer) SetResolver(resolver DIDResolver) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if resolver == nil {
		resolver = defaultResolver{}
	}
	i.resolver = resolver
	atomic.AddUint64(&i.policyGeneration, 1)
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) SetFeeVerifier(verifier FeeVerifier) {",
    r'''
func (i *Indexer) SetFeeVerifier(verifier FeeVerifier) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if verifier == nil {
		verifier = defaultFeeVerifier{}
	}
	i.feeVerifier = verifier
	i.resetFeeUsageLocked()
	atomic.AddUint64(&i.policyGeneration, 1)
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) SetSystemVerifier(verifier SystemVerifier) {",
    r'''
func (i *Indexer) SetSystemVerifier(verifier SystemVerifier) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if verifier == nil {
		verifier = defaultSystemVerifier{}
	}
	i.system = verifier
	atomic.AddUint64(&i.policyGeneration, 1)
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) PutLocal(record *wire.DKVSRecord) (bool, error) {",
    r'''
func (i *Indexer) PutLocal(record *wire.DKVSRecord) (bool, error) {
	updated, eventType, hash, err := i.put(record, false)
	if err != nil {
		return false, err
	}
	if updated {
		i.trackPathSyncChange(record)
		i.emit(eventType, record, hash)
	}
	return updated, nil
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) PutRemote(record *wire.DKVSRecord) (bool, error) {",
    r'''
func (i *Indexer) PutRemote(record *wire.DKVSRecord) (bool, error) {
	updated, _, _, err := i.put(record, true)
	if err == nil && updated {
		i.trackPathSyncChange(record)
	}
	return updated, err
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) GetByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {",
    r'''
func (i *Indexer) GetByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	if keyBytes, err := i.db.Read(hashDBKey(hash)); err == nil {
		record, err := i.getRaw(string(keyBytes))
		if err == nil && RecordHash(record) == hash &&
			i.activeError(record, i.currentHeight(), currentUnixMilli()) == nil {
			return record, nil
		}
	}
	keyBytes, err := i.db.Read(deleteHashDBKey(hash))
	if err != nil {
		return nil, ErrRecordNotFound
	}
	state, err := i.getDeleteStateRaw(string(keyBytes))
	if err != nil {
		return nil, ErrRecordNotFound
	}
	record, err := deleteCommandFromState(state)
	if err != nil || RecordHash(record) != hash {
		return nil, ErrRecordNotFound
	}
	return record, nil
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) ListPrefix(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {",
    r'''
func (i *Indexer) ListPrefix(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, 0, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, 0, err
	}
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = 100
	}
	normalized := strings.TrimSuffix(prefix, "/")
	height := i.currentHeight()
	now := currentUnixMilli()
	if path, ok := normalizeCollectionPath(normalized); ok && path == normalized {
		i.mutex.Lock()
		defer i.mutex.Unlock()
		return i.listPrefixWithMetaLocked(normalized, start, limit, height, now)
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.listPrefixLocked(normalized, start, limit, height, now)
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) Usage(prefix string) (*Usage, error) {",
    r'''
func (i *Indexer) Usage(prefix string) (*Usage, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, err
	}
	normalized := strings.TrimSuffix(prefix, "/")
	if path, ok := normalizeCollectionPath(normalized); ok && path == normalized {
		meta, err := i.GetPathMeta(path)
		if err != nil {
			return nil, err
		}
		return &Usage{
			Prefix:          path,
			ActiveRecords:   meta.ActiveCount,
			ActiveTotalSize: meta.ActiveBytes,
		}, nil
	}
	records, _, _, err := i.scan(prefix, nil, 0, true)
	if err != nil {
		return nil, err
	}
	usage := &Usage{Prefix: normalized}
	for _, record := range records {
		usage.ActiveRecords++
		usage.ActiveTotalSize += uint64(RecordSize(record))
	}
	return usage, nil
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) ApplySnapshot(snapshot *Snapshot) (int, error) {",
    r'''
func (i *Indexer) ApplySnapshot(snapshot *Snapshot) (int, error) {
	if err := ValidateSnapshot(snapshot); err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if record == nil {
			return 0, ErrInvalidSnapshot
		}
		if _, ok := seen[record.Key]; ok {
			return 0, ErrInvalidSnapshot
		}
		seen[record.Key] = struct{}{}
		if err := i.validate(record); err != nil {
			return 0, err
		}
	}
	applied := 0
	for _, record := range snapshot.Records {
		updated, err := i.PutRemote(record)
		if err != nil {
			return applied, err
		}
		if updated {
			applied++
		}
	}
	return applied, nil
}
''',
)

replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) put(record *wire.DKVSRecord, remote bool) (bool, uint32, chainhash.Hash, error) {",
    r'''
func (i *Indexer) put(record *wire.DKVSRecord, remote bool) (bool, uint32, chainhash.Hash, error) {
	height := i.currentHeight()
	now := currentUnixMilli()
	parsed, err := validateRecordEnvelope(record, height, now, remote)
	if err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	candidateHash := RecordHash(record)

	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := i.captureWriteSnapshot(parsed, record.Key)
		if err != nil {
			return false, 0, chainhash.Hash{}, err
		}

		// A delete for a key this node has never stored is an idempotent no-op.
		// In particular, it must not allocate a permanent delete journal entry.
		if IsTombstone(record.Flags) && snapshot.existing == nil && snapshot.deleteMaxSeq == 0 {
			return false, 0, candidateHash, nil
		}
		if !IsTombstone(record.Flags) && snapshot.deleteMaxSeq != 0 &&
			record.Seq <= snapshot.deleteMaxSeq {
			return false, 0, chainhash.Hash{}, ErrStaleRecord
		}

		feeExemptDelete := remote && IsTombstone(record.Flags) &&
			IsExpired(record, height, now)
		if !feeExemptDelete {
			if err := verifyFeeWith(snapshot.feeVerifier, record, parsed); err != nil {
				return false, 0, chainhash.Hash{}, err
			}
		}

		forceReplace, err := validateWritePermissionWith(i, parsed, record,
			snapshot.existing, snapshot.resolver, snapshot.systemVerifier,
			snapshot.nameTransferDirty)
		if err != nil {
			return false, 0, chainhash.Hash{}, err
		}
		if IsTombstone(record.Flags) && snapshot.existing != nil &&
			record.Seq <= snapshot.existing.Seq {
			return false, 0, chainhash.Hash{}, ErrStaleRecord
		}

		candidateNoop := snapshot.existing != nil && !IsTombstone(record.Flags) &&
			!forceReplace && i.activeError(snapshot.existing, height, now) == nil &&
			CompareRecords(snapshot.existing, record) >= 0
		var prepared preparedFeeCapacity
		if !candidateNoop && !IsTombstone(record.Flags) {
			prepared, err = i.prepareFeeCapacity(snapshot.feeVerifier, record,
				parsed, snapshot.existing, height, now, snapshot.dataGeneration)
			if err != nil {
				return false, 0, chainhash.Hash{}, err
			}
		}

		i.mutex.Lock()
		latest, latestErr := i.getRaw(record.Key)
		if errors.Is(latestErr, ErrRecordNotFound) {
			latest = nil
			latestErr = nil
		}
		if latestErr != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, latestErr
		}
		latestDeleteMax := uint64(0)
		if state, stateErr := i.getDeleteStateRaw(record.Key); stateErr == nil {
			latestDeleteMax = state.MaxSeq
		} else if !errors.Is(stateErr, ErrRecordNotFound) {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, stateErr
		}
		latestDirty := false
		if parsed.Namespace == "name" {
			latestDirty, err = i.nameTransferDirty(parsed.Segments[0])
			if err != nil {
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
		}
		if !sameStoredRecord(latest, snapshot.existing) ||
			latestDeleteMax != snapshot.deleteMaxSeq ||
			latestDirty != snapshot.nameTransferDirty ||
			atomic.LoadUint64(&i.policyGeneration) != snapshot.policyGeneration {
			i.mutex.Unlock()
			continue
		}

		if candidateNoop {
			if latestDirty {
				if err := i.clearNameTransferDirty(parsed.Segments[0]); err != nil {
					i.mutex.Unlock()
					return false, 0, chainhash.Hash{}, err
				}
			}
			hash := RecordHash(latest)
			i.mutex.Unlock()
			return false, 0, hash, nil
		}

		if IsTombstone(record.Flags) {
			if latest == nil {
				i.mutex.Unlock()
				return false, 0, candidateHash, nil
			}
			if record.Seq <= latest.Seq {
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, ErrStaleRecord
			}
			if err := i.validateStatefulLocked(record, parsed, latest, height, now); err != nil {
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
			batch := i.db.NewWriteBatch()
			if err := batch.Delete(recordDBKey(record.Key)); err != nil {
				batch.Close()
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
			if err := batch.Delete(hashDBKey(RecordHash(latest))); err != nil {
				batch.Close()
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
			if err := i.writeDeleteStateBatchLocked(batch, record, now); err != nil {
				batch.Close()
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
			if err := i.applyPathMetaMutationLocked(batch, parsed, latest, nil, height, now); err != nil {
				batch.Close()
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
			if latestDirty {
				if err := batch.Delete(nameTransferDBKey(parsed.Segments[0])); err != nil {
					batch.Close()
					i.mutex.Unlock()
					return false, 0, chainhash.Hash{}, err
				}
			}
			if err := batch.Flush(); err != nil {
				batch.Close()
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
			batch.Close()
			atomic.AddUint64(&i.generation, 1)
			i.removeFeeUsageLocked(record.Key)
			if i.recordExpiryInitialized {
				i.replaceRecordExpiryLocked(record)
			}
			i.mutex.Unlock()
			return true, EventRecordTombstone, candidateHash, nil
		}

		if latestDeleteMax != 0 && record.Seq <= latestDeleteMax {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, ErrStaleRecord
		}
		if err := i.validateStatefulLocked(record, parsed, latest, height, now); err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if latest != nil && !forceReplace && i.activeError(latest, height, now) == nil &&
			CompareRecords(latest, record) >= 0 {
			hash := RecordHash(latest)
			i.mutex.Unlock()
			return false, 0, hash, nil
		}
		if err := i.validatePreparedFeeCapacityLocked(prepared, record, height, now); err != nil {
			i.mutex.Unlock()
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return false, 0, chainhash.Hash{}, err
		}
		data, err := MarshalRecord(record)
		if err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		batch := i.db.NewWriteBatch()
		if err := batch.Put(recordDBKey(record.Key), data); err != nil {
			batch.Close()
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if latest != nil {
			latestHash := RecordHash(latest)
			if latestHash != candidateHash {
				if err := batch.Delete(hashDBKey(latestHash)); err != nil {
					batch.Close()
					i.mutex.Unlock()
					return false, 0, chainhash.Hash{}, err
				}
			}
		}
		if err := batch.Put(hashDBKey(candidateHash), []byte(record.Key)); err != nil {
			batch.Close()
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if err := i.removeDeleteStateBatchLocked(batch, record.Key); err != nil {
			batch.Close()
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if err := i.applyPathMetaMutationLocked(batch, parsed, latest, record, height, now); err != nil {
			batch.Close()
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if latestDirty {
			if err := batch.Delete(nameTransferDBKey(parsed.Segments[0])); err != nil {
				batch.Close()
				i.mutex.Unlock()
				return false, 0, chainhash.Hash{}, err
			}
		}
		if err := batch.Flush(); err != nil {
			batch.Close()
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		batch.Close()
		atomic.AddUint64(&i.generation, 1)
		if indexed, ok := snapshot.feeVerifier.(IndexedFeeCapacityVerifier); ok && i.feeUsageInitialized {
			i.replaceFeeUsageLocked(indexed, record)
		}
		if i.recordExpiryInitialized {
			i.replaceRecordExpiryLocked(record)
		}
		eventType := notifyEventType(parsed, record, latest)
		i.mutex.Unlock()
		return true, eventType, candidateHash, nil
	}
	return false, 0, chainhash.Hash{}, ErrConcurrentUpdate
}
''',
)

replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) validateAt(record *wire.DKVSRecord, height, now uint64) error {",
    r'''
func (i *Indexer) validateAt(record *wire.DKVSRecord, height, now uint64) error {
	parsed, err := validateRecordEnvelope(record, height, now, true)
	if err != nil {
		return err
	}
	if IsTombstone(record.Flags) {
		return ErrRecordNotFound
	}
	return i.validateStoredPermission(parsed, record)
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) activeError(record *wire.DKVSRecord, height, now uint64) error {",
    r'''
func (i *Indexer) activeError(record *wire.DKVSRecord, height, now uint64) error {
	if record == nil || IsTombstone(record.Flags) {
		return ErrRecordNotFound
	}
	return i.validateAt(record, height, now)
}
''',
)
replace_func(
    "indexer/indexer/dkvs/indexer.go",
    "func (i *Indexer) validateStoredPermission(parsed ParsedKey, record *wire.DKVSRecord) error {",
    r'''
func (i *Indexer) validateStoredPermission(parsed ParsedKey, record *wire.DKVSRecord) error {
	if record == nil {
		return ErrInvalidRecord
	}
	// External authorities are checked when a record is accepted. Reads and
	// scans must not call HTTP/RPC verifiers while holding the indexer lock.
	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != personalAccountID(record.PubKey) {
			return ErrPermissionDenied
		}
	case "blob":
		if len(parsed.Segments) < 3 || parsed.Segments[0] != personalAccountID(record.PubKey) {
			return ErrPermissionDenied
		}
	case "mail":
		if len(parsed.Segments) >= 2 && parsed.Segments[1] == "share" &&
			parsed.Segments[0] != personalAccountID(record.PubKey) {
			return ErrPermissionDenied
		}
	}
	return nil
}
''',
)

# Mailbox TTL and quota use the shared path metadata instead of a full scan.
replace_func(
    "indexer/indexer/dkvs/mailbox.go",
    "func (i *Indexer) validateMailboxLocked(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, height, now uint64) error {",
    r'''
func (i *Indexer) validateMailboxLocked(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, height, now uint64) error {
	if len(parsed.Segments) < 2 {
		return ErrInvalidKey
	}
	if IsTombstone(record.Flags) {
		return nil
	}

	switch parsed.Segments[1] {
	case "msg":
		if RecordSize(record) > i.mailbox.MaxMsgSize {
			return ErrRecordTooLarge
		}
		if record.TTL == 0 || (i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL) {
			return ErrInvalidRecord
		}
		usedBytes, usedCount, err := i.mailboxUsageLocked(parsed.Segments[0], "msg", record.Key, height, now)
		if err != nil {
			return err
		}
		if usedCount+1 > i.mailbox.MaxMessages || usedBytes+uint64(RecordSize(record)) > i.mailbox.MaxMsgBytes {
			return ErrMailboxFull
		}
	case "share":
		if RecordSize(record) > i.mailbox.MaxShareSize {
			return ErrRecordTooLarge
		}
		if record.TTL == 0 || (i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL) {
			return ErrInvalidRecord
		}
		usedBytes, usedCount, err := i.mailboxUsageLocked(parsed.Segments[0], "share", record.Key, height, now)
		if err != nil {
			return err
		}
		if usedCount+1 > i.mailbox.MaxShares || usedBytes+uint64(RecordSize(record)) > i.mailbox.MaxShareBytes {
			return ErrMailboxFull
		}
	default:
		return ErrInvalidKey
	}
	return nil
}
''',
)
replace_func(
    "indexer/indexer/dkvs/mailbox.go",
    "func (i *Indexer) mailboxUsageLocked(mailboxID, kind, excludeKey string, height, now uint64) (uint64, uint64, error) {",
    r'''
func (i *Indexer) mailboxUsageLocked(mailboxID, kind, excludeKey string, height, now uint64) (uint64, uint64, error) {
	path := "/mail/" + mailboxID + "/" + kind
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return 0, 0, err
	}
	usedBytes := meta.ActiveBytes
	usedCount := meta.ActiveCount
	if excludeKey != "" {
		existing, err := i.getRaw(excludeKey)
		if err == nil && recordActiveForMeta(existing, height, now) {
			if usedCount > 0 {
				usedCount--
			}
			size := uint64(RecordSize(existing))
			if size >= usedBytes {
				usedBytes = 0
			} else {
				usedBytes -= size
			}
		} else if err != nil && !errors.Is(err, ErrRecordNotFound) {
			return 0, 0, err
		}
	}
	return usedBytes, usedCount, nil
}
''',
)

# Add manager wrappers used by P2P sync and API consumers.
interface_path = "indexer/indexer/dkvs_interface.go"
interface_text = read(interface_path)
append_methods = textwrap.dedent(r'''

func (b *IndexerMgr) GetDKVSRecordForSync(key string) (*wire.DKVSRecord, error) {
	return b.dkvsIndexer.GetForSync(key)
}

func (b *IndexerMgr) SyncFilteredDKVSRecordsSession(sessionID uint64, cursor []byte, limit uint32, filters []dkvs_indexer.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return b.dkvsIndexer.SyncFilteredSession(sessionID, cursor, limit, filters)
}

func (b *IndexerMgr) ReconcileDKVSMirrorSubscription(sub dkvs_indexer.Subscription, seen map[string]struct{}) (int, error) {
	return b.dkvsIndexer.ReconcileMirrorSubscription(sub, seen)
}

func (b *IndexerMgr) GetDKVSPathMeta(path string) (*dkvs_indexer.PathMeta, error) {
	return b.dkvsIndexer.GetPathMeta(path)
}
''')
if "GetDKVSRecordForSync" not in interface_text:
    write(interface_path, interface_text.rstrip() + append_methods + "\n")

# P2P client syncs one subscription path at a time and reconciles omissions only
# on ordinary mirror nodes.
insert_struct_fields(
    "server.go",
    "type serverPeer struct {",
    """
dkvsSyncFilters     []wire.DKVSSyncFilter
dkvsSyncFilterIndex int
dkvsSyncSeen        map[string]struct{}
""",
)
replace_func(
    "server.go",
    "func (sp *serverPeer) OnDKVSGet(_ *peer.Peer, msg *wire.MsgDKVSGet) {",
    r'''
func (sp *serverPeer) OnDKVSGet(_ *peer.Peer, msg *wire.MsgDKVSGet) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	records := make([]*wire.DKVSRecord, 0)
	notFound := make([]chainhash.Hash, 0)
	seen := make(map[chainhash.Hash]struct{})
	for _, key := range msg.Keys {
		record, err := sp.server.assetIndexer.GetDKVSRecordForSync(key)
		if err != nil {
			notFound = append(notFound, dkvsKeyHash(key))
			continue
		}
		hash := dkvsindexer.RecordHash(record)
		if _, ok := seen[hash]; !ok {
			seen[hash] = struct{}{}
			records = append(records, record)
		}
	}
	for _, hash := range msg.RecordHashes {
		record, err := sp.server.assetIndexer.GetDKVSRecordByHash(hash)
		if err != nil {
			notFound = append(notFound, hash)
			continue
		}
		recordHash := dkvsindexer.RecordHash(record)
		if _, ok := seen[recordHash]; !ok {
			seen[recordHash] = struct{}{}
			records = append(records, record)
		}
	}
	sp.queueDKVSData(records, notFound)
}
''',
)
replace_func(
    "server.go",
    "func (sp *serverPeer) OnDKVSSyncRequest(_ *peer.Peer, msg *wire.MsgDKVSSyncRequest) {",
    r'''
func (sp *serverPeer) OnDKVSSyncRequest(_ *peer.Peer, msg *wire.MsgDKVSSyncRequest) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	if len(msg.Filters) == 0 && (sp.Services()&wire.SFNodeMiner == 0 || sp.ValidatorId() == "") {
		sp.addBanScore(0, 10, "unfiltered DKVS sync from non-miner")
		return
	}
	records, next, done, root, err := sp.server.assetIndexer.SyncFilteredDKVSRecordsSession(
		msg.SessionID, msg.Cursor, msg.Limit, dkvsSubscriptionsFromWireFilters(msg.Filters))
	if err != nil {
		peerLog.Debugf("dkvs sync request from %s failed: %v", sp, err)
		return
	}
	sp.QueueMessage(&wire.MsgDKVSSyncResponse{
		SessionID:      msg.SessionID,
		Records:        records,
		NextCursor:     next,
		Done:           done,
		CheckpointRoot: root,
	}, nil)
}
''',
)
replace_func(
    "server.go",
    "func (sp *serverPeer) OnDKVSSyncResponse(_ *peer.Peer, msg *wire.MsgDKVSSyncResponse) {",
    r'''
func (sp *serverPeer) OnDKVSSyncResponse(_ *peer.Peer, msg *wire.MsgDKVSSyncResponse) {
	if sp == nil || sp.server == nil || sp.server.assetIndexer == nil || msg == nil {
		return
	}
	var (
		reconcileSub *dkvsindexer.Subscription
		reconcileSeen map[string]struct{}
		nextPath bool
	)
	sp.dkvsSyncMtx.Lock()
	if !sp.dkvsSyncActive || msg.SessionID == 0 || msg.SessionID != sp.dkvsSyncSession {
		sp.dkvsSyncMtx.Unlock()
		sp.addBanScore(0, 5, "unsolicited DKVS sync response")
		return
	}
	if msg.CheckpointRoot != (chainhash.Hash{}) {
		if !sp.dkvsSyncRootSet {
			sp.dkvsSyncRoot = msg.CheckpointRoot
			sp.dkvsSyncRootSet = true
		} else if sp.dkvsSyncRoot != msg.CheckpointRoot {
			sp.dkvsSyncActive = false
			sp.dkvsSyncMtx.Unlock()
			sp.queueDKVSSyncRequest(nil)
			return
		}
	}
	if !msg.Done && (len(msg.NextCursor) == 0 || bytes.Equal(msg.NextCursor, sp.dkvsSyncCursor)) {
		sp.dkvsSyncActive = false
		sp.dkvsSyncMtx.Unlock()
		sp.addBanScore(0, 10, "non-progressing DKVS sync cursor")
		return
	}
	if !sp.isLocalMiner() {
		if sp.dkvsSyncSeen == nil {
			sp.dkvsSyncSeen = make(map[string]struct{})
		}
		for _, record := range msg.Records {
			if record == nil {
				continue
			}
			if dkvsindexer.IsTombstone(record.Flags) {
				delete(sp.dkvsSyncSeen, record.Key)
			} else {
				sp.dkvsSyncSeen[record.Key] = struct{}{}
			}
		}
	}
	if msg.Done {
		sp.dkvsSyncActive = false
		if !sp.isLocalMiner() && sp.dkvsSyncFilterIndex < len(sp.dkvsSyncFilters) {
			filter := sp.dkvsSyncFilters[sp.dkvsSyncFilterIndex]
			sub := dkvsindexer.Subscription{
				Type:   dkvsindexer.SubscriptionType(filter.Type),
				Target: filter.Target,
			}
			reconcileSub = &sub
			reconcileSeen = make(map[string]struct{}, len(sp.dkvsSyncSeen))
			for key := range sp.dkvsSyncSeen {
				reconcileSeen[key] = struct{}{}
			}
			if sp.dkvsSyncFilterIndex+1 < len(sp.dkvsSyncFilters) {
				sp.dkvsSyncFilterIndex++
				nextPath = true
			} else {
				sp.dkvsSyncFilters = nil
				sp.dkvsSyncFilterIndex = 0
			}
			sp.dkvsSyncSeen = nil
		}
	} else {
		sp.dkvsSyncUpdated = time.Now()
	}
	sp.dkvsSyncMtx.Unlock()

	for _, record := range msg.Records {
		if record == nil || !sp.shouldStoreDKVSKey(record.Key) {
			continue
		}
		if _, err := sp.server.assetIndexer.PutRemoteDKVSRecord(record); err != nil {
			peerLog.Warnf("reject synced dkvs record %s from %s: %v", record.Key, sp, err)
		}
	}
	if reconcileSub != nil {
		if _, err := sp.server.assetIndexer.ReconcileDKVSMirrorSubscription(*reconcileSub, reconcileSeen); err != nil {
			peerLog.Warnf("reconcile dkvs mirror path %s from %s: %v", reconcileSub.Target, sp, err)
		}
	}
	if !msg.Done {
		sp.queueDKVSSyncRequest(msg.NextCursor)
		return
	}
	if nextPath {
		sp.queueDKVSSyncRequest(nil)
		return
	}
	if sp.isLocalMiner() && msg.CheckpointRoot != (chainhash.Hash{}) {
		checkpoint, err := sp.server.assetIndexer.GetDKVSCheckpoint()
		if err != nil {
			peerLog.Debugf("dkvs checkpoint after sync from %s failed: %v", sp, err)
			return
		}
		if dkvsCheckpointRootMismatch(checkpoint.ActiveRecordRoot, msg.CheckpointRoot) {
			peerLog.Debugf("dkvs sync checkpoint root mismatch from %s: local=%s remote=%s", sp, checkpoint.ActiveRecordRoot, msg.CheckpointRoot.String())
		}
	}
}
''',
)
replace_func(
    "server.go",
    "func (sp *serverPeer) queueDKVSSyncRequest(cursor []byte) {",
    r'''
func (sp *serverPeer) queueDKVSSyncRequest(cursor []byte) {
	if sp == nil {
		return
	}
	sp.dkvsSyncMtx.Lock()
	if len(cursor) == 0 {
		if sp.dkvsSyncActive && !dkvsSyncSessionExpired(sp.dkvsSyncUpdated, time.Now(), 2*time.Minute) {
			sp.dkvsSyncMtx.Unlock()
			return
		}
		if !sp.isLocalMiner() {
			if len(sp.dkvsSyncFilters) == 0 || sp.dkvsSyncFilterIndex >= len(sp.dkvsSyncFilters) {
				sp.dkvsSyncFilters = dkvsWireFiltersFromSubscriptions(sp.server.assetIndexer.ListDKVSSubscriptions())
				sp.dkvsSyncFilterIndex = 0
			}
			if len(sp.dkvsSyncFilters) == 0 {
				sp.dkvsSyncMtx.Unlock()
				return
			}
			sp.dkvsSyncSeen = make(map[string]struct{})
		}
		session, err := wire.RandomUint64()
		if err != nil || session == 0 {
			sp.dkvsSyncMtx.Unlock()
			return
		}
		sp.dkvsSyncSession = session
		sp.dkvsSyncRoot = chainhash.Hash{}
		sp.dkvsSyncRootSet = false
		sp.dkvsSyncActive = true
		sp.dkvsSyncUpdated = time.Now()
	} else if !sp.dkvsSyncActive {
		sp.dkvsSyncMtx.Unlock()
		return
	}
	sp.dkvsSyncCursor = append(sp.dkvsSyncCursor[:0], cursor...)
	msg := &wire.MsgDKVSSyncRequest{
		SessionID: sp.dkvsSyncSession,
		Cursor:    append([]byte(nil), cursor...),
		Limit:     wire.MaxDKVSRecordsPerMsg,
	}
	if !sp.isLocalMiner() && sp.dkvsSyncFilterIndex < len(sp.dkvsSyncFilters) {
		msg.Filters = []wire.DKVSSyncFilter{sp.dkvsSyncFilters[sp.dkvsSyncFilterIndex]}
	}
	sp.dkvsSyncMtx.Unlock()
	sp.QueueMessage(msg, nil)
}
''',
)

# Local-only administration must fail closed if the remote address is absent.
replace_func(
    "indexer/rpcserver/indexer/router.go",
    "func dkvsLocalOnly(c *gin.Context) {",
    r'''
func dkvsLocalOnly(c *gin.Context) {
	remote := strings.TrimSpace(c.Request.RemoteAddr)
	if remote == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "dkvs local administration only"})
		return
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.Trim(remote, "[]")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "dkvs local administration only"})
		return
	}
	c.Next()
}
''',
)

# Update the existing tombstone test to the physical-delete semantics.
replace_func(
    "indexer/indexer/dkvs/indexer_test.go",
    "func TestPutSelectAndTombstone(t *testing.T) {",
    r'''
func TestPutSelectAndTombstone(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	old := signedPersonalRecordWithKey(t, priv, 1, "old", 0)
	if updated, err := idx.PutLocal(old); err != nil || !updated {
		t.Fatalf("put old updated=%v err=%v", updated, err)
	}
	newer := signedPersonalRecordWithKey(t, priv, 2, "new", 0)
	if updated, err := idx.PutLocal(newer); err != nil || !updated {
		t.Fatalf("put newer updated=%v err=%v", updated, err)
	}
	if _, err := idx.GetByHash(RecordHash(old)); err != ErrRecordNotFound {
		t.Fatalf("old hash should be removed after update, err=%v", err)
	}
	got, err := idx.Get(old.Key)
	if err != nil || string(got.Value) != "new" {
		t.Fatalf("get updated record=%#v err=%v", got, err)
	}
	tombstone := signedPersonalRecordWithKey(t, priv, 3, "", FlagTombstone)
	if updated, err := idx.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("put tombstone updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(old.Key); err != ErrRecordNotFound {
		t.Fatalf("deleted key remains readable: %v", err)
	}
	command, err := idx.GetForSync(old.Key)
	if err != nil || !IsTombstone(command.Flags) {
		t.Fatalf("delete command=%#v err=%v", command, err)
	}
	badTombstone := signedPersonalRecordWithKey(t, priv, 4, "not-empty", FlagTombstone)
	if _, err := idx.PutLocal(badTombstone); err != ErrInvalidRecord {
		t.Fatalf("bad tombstone err=%v", err)
	}
}
''',
)

# Add focused middleware regression tests without touching the large handler test file.
admin_test = ROOT / "indexer/rpcserver/indexer/dkvs_admin_security_test.go"
if not admin_test.exists():
    admin_test.write_text(textwrap.dedent(r'''
        package indexer

        import (
        	"net/http"
        	"net/http/httptest"
        	"testing"

        	"github.com/gin-gonic/gin"
        )

        func TestDKVSLocalOnlyRejectsMissingRemoteAddress(t *testing.T) {
        	gin.SetMode(gin.TestMode)
        	router := gin.New()
        	router.GET("/admin", dkvsLocalOnly, func(c *gin.Context) {
        		c.Status(http.StatusNoContent)
        	})
        	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
        	req.RemoteAddr = ""
        	resp := httptest.NewRecorder()
        	router.ServeHTTP(resp, req)
        	if resp.Code != http.StatusForbidden {
        		t.Fatalf("status=%d", resp.Code)
        	}
        }

        func TestDKVSLocalOnlyAllowsLoopback(t *testing.T) {
        	gin.SetMode(gin.TestMode)
        	router := gin.New()
        	router.GET("/admin", dkvsLocalOnly, func(c *gin.Context) {
        		c.Status(http.StatusNoContent)
        	})
        	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
        	req.RemoteAddr = "127.0.0.1:12345"
        	resp := httptest.NewRecorder()
        	router.ServeHTTP(resp, req)
        	if resp.Code != http.StatusNoContent {
        		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
        	}
        }
    ''').lstrip())

print("DKVS patch applied")
