"""Measure glibc allocation calls separately from the timed libFLAC trials."""
import argparse
import json
import os
from pathlib import Path
import re
import runpy
import struct
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("binary", type=Path, help="flac_bench.c executable used for timing")
parser.add_argument("campaign", type=Path, help="bench-flac-encode JSON for this LPC setting")
args = parser.parse_args()
campaign = json.loads(args.campaign.read_text())
bench = runpy.run_path(str(Path(__file__).resolve().parents[3] / "scripts/bench-flac-encode"))
binary = args.binary.resolve()
assert bench["sha"](binary) == campaign["reference_binary_sha256"]
order = str(campaign["settings"]["max_lpc_order"] or "fixed")
env = dict(os.environ, LD_PRELOAD="libmemusage.so", LC_ALL="C")
rounds = campaign["rounds"]
result = {}
with tempfile.TemporaryDirectory() as directory:
    raw = Path(directory) / "input.pcm"
    output = Path(directory) / "output.flac"
    for name, case in campaign["cases"].items():
        bits, channels = case["bits"], case["channels"]
        samples = bench["signal"](name, bits, channels)
        raw.write_bytes(struct.pack(f"<{len(samples)}i", *samples))
        assert bench["sha"](raw) == case["input_sha256"]
        counts = []
        for n in (rounds, 2 * rounds):
            command = [str(binary), str(raw), str(output), str(bits), str(channels), str(n), order]
            run = subprocess.run(command, env=env, capture_output=True, check=True, text=True)
            assert bench["sha"](output) == case["modes"]["libflac"]["encoded_sha256"]
            report = re.sub(r"\x1b\[[0-9;]*m", "", run.stderr)
            calls = dict(re.findall(r"^\s*(malloc|realloc|calloc)\|\s*(\d+)", report, re.M))
            assert len(calls) == 3, report
            counts.append({k: int(v) for k, v in calls.items()})
        result[name] = {"process_calls": counts, "calls_for_extra_rounds": {
            k: counts[1][k] - counts[0][k] for k in counts[0]}}
print(json.dumps({"rounds": [rounds, 2 * rounds], "cases": result}, indent=2))
