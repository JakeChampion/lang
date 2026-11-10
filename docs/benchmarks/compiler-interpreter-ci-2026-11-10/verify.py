from pathlib import Path
import gzip
import hashlib
import json
import re

root = Path(__file__).resolve().parent
manifest = json.loads((root / "manifest.json").read_text())["sha256"]
for name, expected in manifest.items():
    data = (root / name).read_bytes()
    assert hashlib.sha256(data).hexdigest() == expected, name

identities = json.loads((root / "identities.json").read_text())
for row in identities["original_files"]:
    data = (root / row["path"]).read_bytes()
    if row["gzip"]:
        data = gzip.decompress(data)
    assert len(data) == row["original_bytes"], row["path"]
    assert hashlib.sha256(data).hexdigest() == row["original_sha256"], row["path"]

folded = gzip.decompress((root / "profile/run.stderr.gz").read_bytes()).decode()
header = re.search(r"^# fern CPU profile samples=(\d+) dropped=(\d+)$", folded, re.M)
assert header
samples = sum(int(line.rsplit(" ", 1)[1]) for line in folded.splitlines() if line and not line.startswith("#"))
assert samples == int(header[1]) == 1063
assert int(header[2]) == 0
print(f"Verified {len(manifest)} data hashes, {len(identities['original_files'])} original byte identities, and {samples} CPU samples.")
