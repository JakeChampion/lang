"""Construct exact wire values and declared shape errors without a numerical evaluator."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import random
import struct


def sha(data):
    return hashlib.sha256(data).hexdigest()


def xyz(values):
    return b"XYZ " + bytes(4) + struct.pack(">3i", *values), [0, -1, 3, *values], 2


def curv(values):
    n = len(values)
    payload = b"curv" + bytes(4) + struct.pack(">I", n) + struct.pack(">" + str(n) + "H", *values)
    return payload, [1 if n == 0 else 2 if n == 1 else 3, -1, n, *values], 2 if n < 2 else 2 + (n + 63) // 64


def para(function, values):
    assert len(values) == [1, 3, 4, 5, 7][function]
    return b"para" + bytes(4) + struct.pack(">HH", function, 0) + struct.pack(">" + str(len(values)) + "i", *values), [4, function, len(values), *values], 2 + len(values)


def generate():
    cases = []
    rng = random.Random(13663)

    def valid(name, built, mode=1, max_bytes=1048576, max_samples=4096):
        payload, fields, polls = built
        cases.append(dict(name=name, mode=mode, payload_hex=payload.hex(), payload_sha256=sha(payload), max_bytes=max_bytes,
                          max_samples=max_samples, cancel_at=0, expected=dict(status="ok", fields=fields, polls=polls)))

    def error(name, payload, error, mode=1, max_bytes=1048576, max_samples=4096, cancel_at=0):
        cases.append(dict(name=name, mode=mode, payload_hex=payload.hex(), payload_sha256=sha(payload), max_bytes=max_bytes,
                          max_samples=max_samples, cancel_at=cancel_at, expected=dict(status="error", error=error, polls=cancel_at)))

    edges = [-2147483648, -65536, -1, 0, 1, 65536, 2147483647]
    for i, value in enumerate(edges):
        for axis in range(3):
            values = [0, 65536, -65536]
            values[axis] = value
            valid(f"XYZ-edge-{i}-{axis}", xyz(values), mode=0)
    for i in range(100):
        valid(f"XYZ-random-{i}", xyz([rng.randrange(-(2**31), 2**31) for _ in range(3)]), mode=0)
    valid("identity", curv([]))
    for value in [0, 1, 255, 256, 257, 563, 65535]:
        valid(f"gamma-{value}", curv([value]))
    for n in [2, 3, 63, 64, 65, 127, 128, 129, 4096, 65536]:
        values = [0 if i % 2 else 65535 for i in range(n)]
        valid(f"sample-count-{n}", curv(values), max_samples=max(4096, n))
    for i in range(100):
        valid(f"samples-random-{i}", curv([rng.randrange(65536) for _ in range(rng.randrange(2, 200))]))
    for function, count in enumerate([1, 3, 4, 5, 7]):
        for i, edge in enumerate(edges):
            valid(f"para-edge-{function}-{i}", para(function, [edge] * count))
        for i in range(20):
            valid(f"para-random-{function}-{i}", para(function, [rng.randrange(-(2**31), 2**31) for _ in range(count)]))
    real = Path(__file__).with_name("fixtures") / "icc-feasibility-srgb.icc"
    manifest = json.loads(real.with_name("icc-feasibility-reference.json").read_text())
    raw = real.read_bytes()
    assert sha(raw) == manifest["sha256"]
    for entry in manifest["tags"]:
        if entry["signature"] in ["rXYZ", "gXYZ", "bXYZ", "wtpt"]:
            payload = raw[entry["offset"]:entry["offset"] + entry["size"]]
            values = struct.unpack_from(">3i", payload, 8)
            valid("frozen-" + entry["signature"], (payload, [0, -1, 3, *values], 2), mode=0)
        if entry["signature"] in ["rTRC", "gTRC", "bTRC"]:
            payload = raw[entry["offset"]:entry["offset"] + entry["size"]]
            values = struct.unpack_from(">5i", payload, 12)
            assert payload[:12] == b"para" + bytes(4) + struct.pack(">HH", 3, 0)
            valid("frozen-" + entry["signature"], (payload, [4, 3, 5, *values], 7))

    representatives = [(0, xyz([1, -1, 65536])), (1, curv([])), (1, curv([256])), (1, curv([0, 65535]))]
    representatives += [(1, para(f, [65536] * n)) for f, n in enumerate([1, 3, 4, 5, 7])]
    for i, (mode, built) in enumerate(representatives):
        payload = built[0]
        for length in range(len(payload)):
            error(f"prefix-{i}-{length}", payload[:length], "Length", mode=mode)
        for suffix in [b"\0", bytes(4), b"\xff"]:
            error(f"trailing-{i}-{len(suffix)}-{suffix[0]}", payload + suffix, "Length", mode=mode)
        for at in range(4, 8):
            mutated = bytearray(payload)
            mutated[at] = 1
            error(f"reserved-{i}-{at}", bytes(mutated), "Reserved", mode=mode)
        mutated = bytearray(payload)
        mutated[0] = 0
        error(f"signature-{i}", bytes(mutated), "Signature", mode=mode)
        if payload[:4] == b"para":
            for function in [5, 255, 65535]:
                error(f"function-{i}-{function}", payload[:8] + struct.pack(">H", function) + payload[10:], "Function")
            error(f"para-reserved-{i}", payload[:10] + b"\0\x01" + payload[12:], "Reserved")
        for stop in range(1, built[2] + 1):
            error(f"cancel-{i}-{stop}", payload, "Cancelled", mode=mode, cancel_at=stop)

    for n in [63, 64, 65, 130]:
        built = curv(list(range(n)))
        for stop in range(1, built[2] + 1):
            error(f"sample-cancel-{n}-{stop}", built[0], "Cancelled", cancel_at=stop)
    sample = curv([1, 65535])
    valid("exact-caller-limits", sample, max_bytes=16, max_samples=2)
    for max_bytes, max_samples in [(0, 2), (-1, 2), (15, 2), (16, 0), (16, -1), (16, 1)]:
        error(f"limit-{max_bytes}-{max_samples}", sample[0], "Limit", max_bytes=max_bytes, max_samples=max_samples)
    for n in [4097, 65537, 0xffffffff]:
        error(f"count-limit-{n}", b"curv" + bytes(4) + struct.pack(">I", n), "Limit", max_samples=4096 if n == 4097 else 2147483647)
    valid("exact-max-samples", curv(list(range(65536))), max_samples=2147483647, max_bytes=131084)
    huge = b"curv" + bytes(1048576 - 4)
    error("exact-hard-byte-limit", huge, "Length", max_bytes=2147483647)
    error("over-hard-byte-limit", huge + b"\0", "Limit", max_bytes=2147483647)
    assert len({c["name"] for c in cases}) == len(cases)
    return dict(schema=1, reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes()), frozen_profile=sha(raw)),
                construction="Python struct pack/unpack of declared integers; exact numerators without curve evaluation or floating tolerances."), cases=cases)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=Path)
    options = parser.parse_args()
    document = generate()
    data = json.dumps(document, sort_keys=True, separators=(",", ":")).encode()
    options.output.parent.mkdir(parents=True, exist_ok=True)
    options.output.write_bytes(gzip.compress(data, mtime=0))
    print(json.dumps(dict(cases=len(document["cases"]), sha256=sha(options.output.read_bytes()))))


if __name__ == "__main__":
    main()
