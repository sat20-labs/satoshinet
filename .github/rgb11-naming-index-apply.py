#!/usr/bin/env python3
"""Temporary branch-only integration helper. Removed after source commits."""
from pathlib import Path
import subprocess

expected = {
    "indexer/indexer/base/base_indexer.go": "9977feb210ab14bea51a09aa79f78fa741ea601f",
    "indexer/indexer/indexermgr.go": "8f922dda0c4c455cd2a56c74507dfbbc0ee37c27",
    "indexer/rpcserver/indexer/router.go": "5d241651de4289d80940b0158f339cd5ea7cda97",
}
sources = {}
for path, sha in expected.items():
    got = subprocess.check_output(["git", "hash-object", path], text=True).strip()
    if got != sha:
        raise SystemExit(f"Source changed before reviewed edit: {path}: {got}")
    sources[path] = Path(path).read_text()

def replace(path, old, new):
    text = sources[path]
    if text.count(old) != 1:
        raise SystemExit(f"Expected one checked replacement in {path}: {old[:100]!r}")
    sources[path] = text.replace(old, new, 1)

base = "indexer/indexer/base/base_indexer.go"
replace(base, '\t"github.com/sat20-labs/satoshinet/indexer/indexer/stp"\n', '\t"github.com/sat20-labs/satoshinet/indexer/indexer/stp"\n\t"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"\n')
replace(base, '\tupdateDBCB  UpdateDBCallback\n', '\tupdateDBCB  UpdateDBCallback\n\n\trgb11Names *rgb11names.Index\n\trgb11NamingSource rgb11names.EventSource\n\trgb11NamingStarted bool\n')
replace(base, '\tb.loadSyncStatsFromDB()\n', '\tb.loadSyncStatsFromDB()\n\tb.initRGB11NamingIndex()\n')
replace(base, '\tnewInst.stats = b.stats.Clone()\n', '\tnewInst.stats = b.stats.Clone()\n\tnewInst.rgb11Names = b.rgb11Names.Clone()\n\tnewInst.rgb11NamingSource = b.rgb11NamingSource\n\tnewInst.rgb11NamingStarted = b.rgb11NamingStarted\n')
replace(base, '\t//startTime = time.Now()\n\terr = wb.Flush()\n', '\t// Naming records and SyncStats share this exact durable batch.\n\tvar namingAck func()\n\tif b.rgb11Names != nil {\n\t\tnamingAck, err = b.rgb11Names.Stage(wb)\n\t\tif err != nil {\n\t\t\tcommon.Log.Panicf("stage RGB11 naming index failed: %v", err)\n\t\t}\n\t}\n\terr = wb.Flush()\n')
replace(base, '\t\tcommon.Log.Panicf("BaseIndexer.updateBasicDB-> Error satwb flushing writes to db %v", err)\n\t}\n', '\t\tcommon.Log.Panicf("BaseIndexer.updateBasicDB-> Error satwb flushing writes to db %v", err)\n\t}\n\tif namingAck != nil { namingAck() }\n')
start = sources[base].index('func (b *BaseIndexer) syncBlock(')
end = sources[base].index('\nfunc (b *BaseIndexer) GetTickerInfo(', start)
section = sources[base][start:end]
old = '\tfunc() {\n\t\tb.mutex.Lock()\n\t\tdefer b.mutex.Unlock()\n'
assert section.count(old) == 1
section = section.replace(old, '\tvar namingErr error\n\tfunc() {\n\t\tb.mutex.Lock()\n\t\tdefer b.mutex.Unlock()\n\n\t\tnamingErr = b.applyRGB11NamingBlockLocked(block)\n\t\tif namingErr != nil { return }\n', 1)
old = '\t}()\n\n\t// localStartTime = time.Now()'
assert section.count(old) == 1
section = section.replace(old, '\t}()\n\tif namingErr != nil {\n\t\tcommon.Log.Errorf("RGB11 naming block %d rejected before base mutation: %v", block.Height, namingErr)\n\t\treturn -2\n\t}\n\n\t// localStartTime = time.Now()', 1)
sources[base] = sources[base][:start] + section + sources[base][end:]
replace(base, '\tcommon.Log.Info("BaseIndexer->checkSelf ... ")\n', '\tcommon.Log.Info("BaseIndexer->checkSelf ... ")\n\tif b.rgb11Names != nil {\n\t\tif err := b.rgb11Names.CheckSelf(); err != nil {\n\t\t\tcommon.Log.Errorf("RGB11 naming CheckSelf failed: %v", err)\n\t\t\treturn false\n\t\t}\n\t}\n')
manager = "indexer/indexer/indexermgr.go"
replace(manager, '\t"github.com/sat20-labs/satoshinet/indexer/common"\n', '\t"github.com/sat20-labs/satoshinet/indexer/common"\n\t"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"\n')
replace(manager, '\tDKVS     *DKVSIntegrationConfig\n', '\tDKVS     *DKVSIntegrationConfig\n\t// Node-internal verified block effects; nil keeps naming ingestion disabled.\n\tRGB11NamingSource rgb11names.EventSource\n')
replace(manager, '\tb.compiling.Init()\n', '\tb.compiling.Init()\n\tif b.cfg != nil {\n\t\tif err := b.compiling.ConfigureRGB11NamingSource(b.cfg.RGB11NamingSource); err != nil {\n\t\t\tcommon.Log.Panicf("configure RGB11 naming source failed: %v", err)\n\t\t}\n\t}\n')
router = "indexer/rpcserver/indexer/router.go"
replace(router, '\tr.GET(proxy+"/v3/referrer/:address", s.handle.getReferrer)\n', '\tr.GET(proxy+"/v3/referrer/:address", s.handle.getReferrer)\n\ts.initRGB11NamingRoutes(r, proxy)\n')
query = "indexer/indexer/rgb11names/query.go"
sources[query] = Path(query).read_text()
sources[query] += '\n// HasEffects distinguishes an empty, disabled registry from an activated one.\nfunc (s *Index) HasEffects() bool {\n\tif s == nil { return false }\n\ts.mu.RLock()\n\tdefer s.mu.RUnlock()\n\tfor key := range s.state { if key != cursorKey { return true } }\n\treturn false\n}\n'
index = "indexer/indexer/rgb11names/index.go"
sources[index] = Path(index).read_text()
replace(index, '\traw, err := json.Marshal(events)\n', '\tif len(events) == 0 { events = []Event{} }\n\traw, err := json.Marshal(events)\n')
# Prepare all checked replacements before overwriting any source file.
for path, content in sources.items():
    Path(path).write_text(content)
paths = set(sources)
for pattern in ["indexer/indexer/rgb11names/*.go", "indexer/indexer/base/rgb11_naming*.go", "indexer/indexer/rgb11_naming.go", "indexer/rpcserver/indexer/rgb11_naming*.go"]:
    paths.update(str(path) for path in Path('.').glob(pattern))
paths = sorted(paths)
subprocess.run(["gofmt", "-w", *paths], check=True)
Path('.github/rgb11-naming-index-paths.txt').write_text('\n'.join(paths)+'\n')
