#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def read(path: str) -> str:
    return (ROOT / path).read_text()


def write(path: str, data: str) -> None:
    (ROOT / path).write_text(data)


indexer_path = "indexer/indexer/dkvs/indexer.go"
if "candidateNoop :=" not in read(indexer_path):
    subprocess.run(
        ["python3", ".github/dkvs_agent_apply.py"],
        cwd=ROOT,
        check=True,
    )

# Normalize generated PathMeta integer constants without depending on the
# platform-specific math.MaxInt symbols.
path = "indexer/indexer/dkvs/pathmeta.go"
s = read(path)
s = s.replace('\t"math"\n', "")
s = s.replace("math.MaxUint64", "^uint64(0)")
s = s.replace(
    "total := int(meta.ActiveCount)\n"
    "\tif meta.ActiveCount > uint64(math.MaxInt) {\n"
    "\t\ttotal = math.MaxInt\n"
    "\t}",
    "maxInt := int(^uint(0) >> 1)\n"
    "\ttotal := int(meta.ActiveCount)\n"
    "\tif meta.ActiveCount > uint64(maxInt) {\n"
    "\t\ttotal = maxInt\n"
    "\t}",
)
write(path, s)

# The mailbox quota rewrite uses errors.Is.
path = "indexer/indexer/dkvs/mailbox.go"
s = read(path)
s = s.replace(
    'import "github.com/sat20-labs/satoshinet/wire"',
    'import (\n'
    '\t"errors"\n\n'
    '\t"github.com/sat20-labs/satoshinet/wire"\n'
    ')',
    1,
)
write(path, s)

# Remove the temporary compile sentinel and add the actual atomic import.
path = "indexer/indexer/dkvs/path_sync.go"
s = read(path)
if '"sync/atomic"' not in s:
    s = s.replace('\t"sort"\n', '\t"sort"\n\t"sync/atomic"\n', 1)
s = s.replace(
    '\n\tindexercommon "github.com/sat20-labs/indexer/common"',
    "",
)
s = s.replace("\nvar _ indexercommon.WriteBatch\n", "\n")
write(path, s)

print("DKVS finalization completed")
