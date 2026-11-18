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
# The runner image names a cloud mirror: azure.archive.ubuntu.com, or an EC2
# regional mirror such as us-east-2.ec2.archive.ubuntu.com.
cloud = re.compile(r"(?<!\S)https?://(?:azure|[a-z0-9-]+\.ec2)\.archive\.ubuntu\.com/ubuntu(/?)(?=\s|$)")
for source in sources:
    if not source.is_file():
        continue
    original = source.read_text()
    lines = []
    for line in original.splitlines(keepends=True):
        active, marker, comment = line.partition("#")
        lines.append(cloud.sub(r"https://archive.ubuntu.com/ubuntu\1", active) + marker + comment)
    updated = "".join(lines)
    if updated != original:
        source.write_text(updated)
        print(f"Prefer the official Ubuntu archive in {source}")
PY
