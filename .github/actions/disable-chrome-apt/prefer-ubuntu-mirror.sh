#!/usr/bin/env bash
set -euo pipefail

# Accept a fixture directory; CI uses the runner's apt configuration.
python3 - "${1:-/etc/apt}" <<'PY'
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])
sources = [root / "sources.list", root / "apt-mirrors.txt"]
sources += sorted((root / "sources.list.d").glob("*.list"))
sources += sorted((root / "sources.list.d").glob("*.sources"))
azure = re.compile(r"(?<!\S)https?://azure\.archive\.ubuntu\.com/ubuntu(/?)(?=\s|$)")
for source in sources:
    if not source.is_file():
        continue
    original = source.read_text()
    lines = []
    for line in original.splitlines(keepends=True):
        active, marker, comment = line.partition("#")
        lines.append(azure.sub(r"https://archive.ubuntu.com/ubuntu\1", active) + marker + comment)
    updated = "".join(lines)
    if updated != original:
        source.write_text(updated)
        print(f"Prefer the official Ubuntu archive in {source}")
PY
