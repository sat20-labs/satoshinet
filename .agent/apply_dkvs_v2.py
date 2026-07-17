#!/usr/bin/env python3
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def read(path: str) -> str:
    return (ROOT / path).read_text()


def write(path: str, content: str) -> None:
    (ROOT / path).write_text(content)


def replace_once(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise RuntimeError(f"{label}: expected exactly one match, got {count}")
    return text.replace(old, new, 1)


def replace_section(text: str, start: str, end: str | None, replacement: str, label: str) -> str:
    a = text.find(start)
    if a < 0:
        raise RuntimeError(f"{label}: start marker not found")
    b = len(text) if end is None else text.find(end, a)
    if b < 0:
        raise RuntimeError(f"{label}: end marker not found")
    suffix = "" if b == len(text) else "\n\n"
    return text[:a] + replacement.rstrip() + suffix + text[b:]


path = "indexer/indexer/dkvs/indexer.go"
s = read(path)
s = replace_once(
    s,
    "\tmutex                   sync.RWMutex\n\tgeneration              uint64\n\tcheckpointMutex         sync.Mutex\n",
    "\tmutex                   sync.RWMutex\n\tgeneration              uint64\n\tpolicyGeneration        uint64\n\tpathSyncs               *pathSyncTracker\n\tcheckpointMutex         sync.Mutex\n",
    "Indexer fields",
)
s = replace_once(
    s,
    "\t\tsubs:        newSubscriptionSet(),\n\t\tnotify:      cfg.Notify,\n",
    "\t\tsubs:        newSubscriptionSet(),\n\t\tpathSyncs:   newPathSyncTracker(),\n\t\tnotify:      cfg.Notify,\n",
    "Indexer initialization",
)
s = replace_once(s, "\ti.resolver = resolver\n}", "\ti.resolver = resolver\n\ti.policyGeneration++\n}", "SetResolver")
s = replace_once(
    s,
    "\ti.feeVerifier = verifier\n\ti.resetFeeUsageLocked()\n}",
    "\ti.feeVerifier = verifier\n\ti.policyGeneration++\n\ti.resetFeeUsageLocked()\n}",
    "SetFeeVerifier",
)
s = replace_once(s, "\ti.system = verifier\n}", "\ti.system = verifier\n\ti.policyGeneration++\n}", "SetSystemVerifier")
s = replace_section(
    s,
    "func (i *Indexer) NotifyNameTransfers(",
    "func (i *Indexer) Get(",
    """func (i *Indexer) NotifyNameTransfers(names []string) error {
\ti.mutex.Lock()
\tdefer i.mutex.Unlock()
\treturn i.notifyNameTransfersLocked(names)
}""",
    "NotifyNameTransfers",
)
s = replace_section(
    s,
    "func (i *Indexer) ListPrefix(",
    "func (i *Indexer) Usage(",
    """func (i *Indexer) ListPrefix(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
\treturn i.listPrefixV2(prefix, start, limit)
}""",
    "ListPrefix",
)
s = replace_section(
    s,
    "func (i *Indexer) Usage(",
    "func (i *Indexer) Sync(",
    """func (i *Indexer) Usage(prefix string) (*Usage, error) {
\treturn i.usageV2(prefix)
}""",
    "Usage",
)
s = replace_section(
    s,
    "func (i *Indexer) SyncFiltered(",
    "func (i *Indexer) Subscribe(",
    """func (i *Indexer) SyncFiltered(cursor []byte, limit uint32, filters []Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
\treturn i.syncFilteredV2(cursor, limit, filters)
}""",
    "SyncFiltered",
)
s = replace_section(
    s,
    "func (i *Indexer) put(",
    "func (i *Indexer) validate(",
    """func (i *Indexer) put(record *wire.DKVSRecord, remote bool) (bool, uint32, chainhash.Hash, error) {
\treturn i.putOptimistic(record, remote)
}""",
    "put",
)
s = replace_once(
    s,
    """\tfor _, expired := range expiredRecords {
\t\ti.emit(EventExpired, expired.record, expired.hash)
\t}
\treturn pruned, nil
}""",
    """\tfor _, expired := range expiredRecords {
\t\ti.emit(EventExpired, expired.record, expired.hash)
\t}
\tif _, err := i.pruneDeletePayloads(now); err != nil {
\t\treturn pruned, err
\t}
\treturn pruned, nil
}""",
    "Prune delete payloads",
)
s = replace_section(
    s,
    "func (i *Indexer) validateStoredPermission(",
    "func (i *Indexer) validateStatefulLocked(",
    """func (i *Indexer) validateStoredPermission(parsed ParsedKey, record *wire.DKVSRecord) error {
\tswitch parsed.Namespace {
\tcase "name", "svc":
\t\tdirty, err := i.authorityDirty(parsed)
\t\tif err != nil {
\t\t\treturn err
\t\t}
\t\tif dirty {
\t\t\treturn ErrPermissionDenied
\t\t}
\t\treturn nil
\tcase "sys":
\t\treturn nil
\tdefault:
\t\treturn i.validatePermission(parsed, record.PubKey)
\t}
}""",
    "validateStoredPermission",
)
s = replace_section(
    s,
    "func (i *Indexer) requiresNameResolve(",
    "func (i *Indexer) nameTransferDirty(",
    """func (i *Indexer) requiresNameResolve(parsed ParsedKey) (bool, error) {
\tif parsed.Namespace != "name" && parsed.Namespace != "svc" {
\t\treturn false, nil
\t}
\treturn i.authorityDirty(parsed)
}""",
    "requiresNameResolve",
)
write(path, s)

path = "indexer/indexer/dkvs/types.go"
s = read(path)
s = replace_once(
    s,
    '\tErrTooManySubscriptions   = errors.New("too many dkvs subscriptions")\n',
    '\tErrTooManySubscriptions   = errors.New("too many dkvs subscriptions")\n\tErrStaleRecord              = errors.New("stale dkvs record")\n\tErrConcurrentUpdate         = errors.New("concurrent dkvs update")\n',
    "DKVS errors",
)
write(path, s)

path = "indexer/indexer/dkvs/mailbox.go"
s = read(path)
s = replace_once(
    s,
    'import "github.com/sat20-labs/satoshinet/wire"',
    'import (\n\t"errors"\n\n\t"github.com/sat20-labs/satoshinet/wire"\n)',
    "mailbox imports",
)
s = replace_once(
    s,
    "if i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL {",
    "if record.TTL == 0 || (i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL) {",
    "mail message TTL",
)
s = replace_once(
    s,
    "if i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL {",
    "if record.TTL == 0 || (i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL) {",
    "mail share TTL",
)
s = replace_section(
    s,
    "func (i *Indexer) mailboxUsageLocked(",
    None,
    """func (i *Indexer) mailboxUsageLocked(mailboxID, kind, excludeKey string, height, now uint64) (uint64, uint64, error) {
\tpath := "/mail/" + mailboxID + "/" + kind
\tmeta, err := i.ensurePathMetaLocked(path, height, now)
\tif err != nil {
\t\treturn 0, 0, err
\t}
\tusedBytes := meta.ActiveBytes
\tusedCount := meta.ActiveCount
\tif excludeKey != "" {
\t\texisting, err := i.getRaw(excludeKey)
\t\tif err == nil && i.activeError(existing, height, now) == nil && !IsTombstone(existing.Flags) {
\t\t\tif usedCount > 0 {
\t\t\t\tusedCount--
\t\t\t}
\t\t\tsize := uint64(RecordSize(existing))
\t\t\tif usedBytes >= size {
\t\t\t\tusedBytes -= size
\t\t\t} else {
\t\t\t\tusedBytes = 0
\t\t\t}
\t\t} else if err != nil && !errors.Is(err, ErrRecordNotFound) {
\t\t\treturn 0, 0, err
\t\t}
\t}
\treturn usedBytes, usedCount, nil
}
""",
    "mailboxUsageLocked",
)
write(path, s)

path = "indexer/rpcserver/indexer/router.go"
s = read(path)
s = replace_once(
    s,
    """\tif remote == "" {
\t\tc.Next()
\t\treturn
\t}""",
    """\tif remote == "" {
\t\tc.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "dkvs local administration only"})
\t\treturn
\t}""",
    "dkvsLocalOnly fail closed",
)
write(path, s)

path = "indexer/indexer/dkvs/indexer_test.go"
s = read(path)
s = replace_once(
    s,
    """\tgot, err = idx.Get(old.Key)
\tif err != nil {
\t\tt.Fatalf("get tombstone: %v", err)
\t}
\tif !IsTombstone(got.Flags) {
\t\tt.Fatalf("expected tombstone")
\t}""",
    """\tgot, err = idx.Get(old.Key)
\tif !errors.Is(err, ErrRecordNotFound) || got != nil {
\t\tt.Fatalf("deleted key should not remain active, record=%#v err=%v", got, err)
\t}
\trelayDelete, err := idx.GetForSync(old.Key)
\tif err != nil || !IsTombstone(relayDelete.Flags) {
\t\tt.Fatalf("expected relay delete record=%#v err=%v", relayDelete, err)
\t}""",
    "existing tombstone test",
)
write(path, s)

path = "server.go"
s = read(path)
s = replace_once(
    s,
    "\tdkvsSyncActive  bool\n\tdkvsSyncUpdated time.Time\n",
    "\tdkvsSyncActive        bool\n\tdkvsSyncUpdated       time.Time\n\tdkvsSyncFilters       []wire.DKVSSyncFilter\n\tdkvsSyncFilterIndex   int\n\tdkvsSyncCurrentFilter wire.DKVSSyncFilter\n\tdkvsSyncMirrorKeys    map[string]struct{}\n",
    "serverPeer DKVS fields",
)
s = replace_section(
    s,
    "func (sp *serverPeer) OnDKVSGet(",
    "func (sp *serverPeer) OnDKVSData(",
    """func (sp *serverPeer) OnDKVSGet(_ *peer.Peer, msg *wire.MsgDKVSGet) {
\tsp.onDKVSGetV2(msg)
}""",
    "OnDKVSGet",
)
s = replace_section(
    s,
    "func (sp *serverPeer) OnDKVSData(",
    "func (sp *serverPeer) OnDKVSSyncRequest(",
    """func (sp *serverPeer) OnDKVSData(_ *peer.Peer, msg *wire.MsgDKVSData) {
\tsp.onDKVSDataV2(msg)
}""",
    "OnDKVSData",
)
s = replace_section(
    s,
    "func (sp *serverPeer) OnDKVSSyncRequest(",
    "func (sp *serverPeer) OnDKVSSyncResponse(",
    """func (sp *serverPeer) OnDKVSSyncRequest(_ *peer.Peer, msg *wire.MsgDKVSSyncRequest) {
\tsp.onDKVSSyncRequestV2(msg)
}""",
    "OnDKVSSyncRequest",
)
s = replace_section(
    s,
    "func (sp *serverPeer) OnDKVSSyncResponse(",
    "func (sp *serverPeer) isLocalMiner(",
    """func (sp *serverPeer) OnDKVSSyncResponse(_ *peer.Peer, msg *wire.MsgDKVSSyncResponse) {
\tsp.onDKVSSyncResponseV2(msg)
}""",
    "OnDKVSSyncResponse",
)
s = replace_section(
    s,
    "func (sp *serverPeer) needsDKVSRecord(",
    "func (sp *serverPeer) queueDKVSData(",
    """func (sp *serverPeer) needsDKVSRecord(key string, hash chainhash.Hash) bool {
\treturn sp.needsDKVSRecordV2(key, hash)
}""",
    "needsDKVSRecord",
)
s = replace_section(
    s,
    "func (sp *serverPeer) queueDKVSSyncRequest(",
    "func dkvsSyncSessionExpired(",
    """func (sp *serverPeer) queueDKVSSyncRequest(cursor []byte) {
\tsp.queueDKVSSyncRequestV2(cursor)
}""",
    "queueDKVSSyncRequest",
)
s = replace_section(
    s,
    "func (sp *serverPeer) dkvsSyncRequest(",
    "func dkvsSyncRequestPayloadLen(",
    """func (sp *serverPeer) dkvsSyncRequest(cursor []byte) *wire.MsgDKVSSyncRequest {
\treturn sp.dkvsSyncRequestV2(cursor)
}""",
    "dkvsSyncRequest",
)
write(path, s)

modified = [
    "indexer/indexer/dkvs/indexer.go",
    "indexer/indexer/dkvs/types.go",
    "indexer/indexer/dkvs/mailbox.go",
    "indexer/rpcserver/indexer/router.go",
    "indexer/indexer/dkvs/indexer_test.go",
    "server.go",
]
subprocess.run(["gofmt", "-w", *modified], cwd=ROOT, check=True)
print("applied DKVS v2 wiring")
