"""Construct finite ICC envelopes and explicit policy mutations without a parser oracle."""

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import random
import struct

from PIL import ImageCms, features
import PIL


def sha(data):
    return hashlib.sha256(data).hexdigest()


def put(data, at, value):
    result = bytearray(data)
    struct.pack_into(">I", result, at, value)
    return bytes(result)


def polls(n):
    return 3 + 2 * n + sum((i + 63) // 64 for i in range(n))


def build(sizes, aliases=None, order=None, intent=0, device=b"mntr", flags=0, high=0, low=0, identifier=bytes(16)):
    aliases = list(range(len(sizes))) if aliases is None else aliases
    order = list(range(len(aliases))) if order is None else order
    start = 132 + 12 * len(aliases)
    offsets = []
    body = bytearray()
    for i, size in enumerate(sizes):
        assert size >= 8
        offsets.append(start + len(body))
        body += b"zzzz" + bytes(4) + bytes([i & 255]) * (size - 8)
        body += bytes((-size) % 4)
    header = bytearray(132)
    struct.pack_into(">I4sI4s4s4s6H4s4sI4s4sIII3I4s16s28sI", header, 0,
                     start + len(body), bytes(4), 0x04400000, device, b"RGB ", b"XYZ ",
                     2026, 10, 27, 2, 0, 0, b"acsp", bytes(4), flags, bytes(4), bytes(4),
                     high, low, intent, 63190, 65536, 54061, bytes(4), identifier, bytes(28), len(aliases))
    entries = [(0x74303030 + index, offsets[aliases[index]], sizes[aliases[index]], 0x7a7a7a7a) for index in order]
    wire = b"".join(struct.pack(">III", *entry[:3]) for entry in entries)
    fields = [start + len(body), int(device == b"mntr"), intent, flags, high, low,
              2026, 10, 27, 2, 0, 0, len(sizes), len(aliases), *identifier]
    fields += [v for entry in entries for v in entry]
    return bytes(header) + wire + body, fields


def observe(payload):
    profile = ImageCms.ImageCmsProfile(io.BytesIO(payload))
    p = profile.profile
    return dict(device_class=p.device_class, color_space=p.xcolor_space, pcs=p.connection_space,
                version=p.icc_version, intent=p.rendering_intent, name=ImageCms.getProfileName(profile).strip())


def generate():
    root = Path(__file__).parent
    cases = []
    rng = random.Random(13658)

    def add(name, payload, fields=None, error=None, count=1, max_bytes=1048576, max_tags=128, stop=0, lcms=False):
        expected = dict(status="ok", fields=fields, polls=count) if error is None else dict(status="error", error=error, polls=count)
        cases.append(dict(name=name, payload_hex=payload.hex(), payload_sha256=sha(payload), max_bytes=max_bytes,
                          max_tags=max_tags, cancel_at=stop, expected=expected,
                          offline=dict(applicable=lcms, observation=observe(payload) if lcms else None)))

    def valid(name, built, **kw):
        payload, fields = built
        add(name, payload, fields, count=polls(fields[13]), **kw)

    for n in [0, 1, 2, 3, 63, 64, 65, 128, 1024]:
        valid(f"count-{n}", build([8] * n), max_tags=max(128, n))
    for residue in range(4):
        valid(f"padding-{residue}", build([8 + residue, 12 + residue]))
    for n in [2, 3, 65, 1024]:
        valid(f"alias-{n}", build([9], aliases=[0] * n), max_tags=max(128, n))
    for index in range(100):
        sizes = [rng.randrange(8, 80) for _ in range(rng.randrange(1, 8))]
        aliases = list(range(len(sizes))) + [rng.randrange(len(sizes)) for _ in range(rng.randrange(5))]
        order = list(range(len(aliases)))
        rng.shuffle(order)
        valid(f"permutation-{index}", build(sizes, aliases, order, intent=index % 4,
              device=b"mntr" if index % 2 else b"scnr", flags=(index << 16) | (index % 4),
              high=(0xffffffff - index), low=index % 16, identifier=rng.randbytes(16)))

    real = (root / "fixtures/icc-feasibility-srgb.icc").read_bytes()
    manifest = json.loads((root / "fixtures/icc-feasibility-reference.json").read_text())
    assert sha(real) == manifest["sha256"]
    real_fields = [588, 1, 0, 0, 0, 0, *struct.unpack_from(">6H", real, 24), 9, 11, *real[84:100]]
    for tag in manifest["tags"]:
        real_fields += [int.from_bytes(tag["signature"].encode(), "big"), tag["offset"], tag["size"],
                        int.from_bytes(real[tag["offset"]:tag["offset"] + 4], "big")]
    for intent in range(4):
        for reverse in [False, True]:
            payload = put(real, 64, intent)
            fields = real_fields.copy()
            fields[2] = intent
            if reverse:
                entries = [payload[i:i + 12] for i in range(132, 264, 12)]
                payload = payload[:132] + b"".join(reversed(entries)) + payload[264:]
                tag_fields = [fields[i:i + 4] for i in range(30, len(fields), 4)]
                fields = fields[:30] + [v for row in reversed(tag_fields) for v in row]
            add(f"offline-{intent}-{int(reverse)}", payload, fields, count=polls(11), lcms=True)
    for n in range(len(real)):
        add(f"prefix-{n}", real[:n], error="Truncated" if n < 132 else "Header", count=0 if n < 132 else 1)

    base, _ = build([8, 8])
    for at, values, error in [(0, [0, 171, 176, 0xffffffff], "Header"), (36, [0, 0xffffffff], "Signature"),
                              (8, [0x02000000, 0x04000000, 0x04300000, 0x04400001, 0x04500000], "Version"),
                              (12, [0, int.from_bytes(b"prtr", "big")], "DeviceClass"),
                              (16, [0, int.from_bytes(b"CMYK", "big")], "ColorSpace"),
                              (20, [0, int.from_bytes(b"Lab ", "big")], "Pcs"),
                              (44, [4, 0xffff], "Header"), (60, [16, 0xffffffff], "Header"),
                              (64, [4, 0xffffffff], "Header"),
                              (68, [0, 0xffffffff], "Header"), (72, [0, 0xffffffff], "Header"),
                              (76, [0, 0xffffffff], "Header")]:
        for value in values:
            add(f"header-{at}-{value}", put(base, at, value), error=error)
    for at in range(100, 128):
        changed = bytearray(base)
        changed[at] = 1
        add(f"reserved-{at}", bytes(changed), error="Header")
    for at, value in [(24, 0), (26, 0), (26, 13), (28, 0), (28, 32), (30, 24), (32, 60), (34, 60)]:
        changed = bytearray(base)
        struct.pack_into(">H", changed, at, value)
        add(f"date-{at}-{value}", bytes(changed), error="Header")
    for at, target in [(68, 9642), (72, 10000), (76, 8249)]:
        first = ((2 * target - 1) * 65536 + 19999) // 20000
        last = ((2 * target + 1) * 65536 - 1) // 20000
        for value in range(first - 1, last + 2):
            changed = put(base, at, value)
            if first <= value <= last:
                fields = build([8, 8])[1]
                add(f"D50-{at}-{value}", changed, fields, count=polls(2))
            else:
                add(f"D50-{at}-{value}", changed, error="Header")
    add("truncated-directory", put(put(base, 0, 132), 128, 1)[:132], error="Truncated")
    add("truncated-large-directory", put(put(base, 0, 132), 128, 1024)[:132], error="Truncated", max_tags=1024)
    for at in [136, 140]:
        for value in [0, 7, 155, 0xffffffff]:
            add(f"extent-{at}-{value}", put(base, at, value), error="Directory", count=2)
    add("duplicate", put(base, 144, 0x74303030), error="Duplicate", count=4)
    add("partial-overlap", put(put(base, 148, 156), 152, 12), error="Overlap", count=4)
    add("shifted-partial-overlap", put(put(base, 164, 0), 148, 160), error="Overlap", count=4)
    add("same-offset-different-size", put(put(base, 148, 156), 152, 9), error="Layout", count=3)
    add("gap-at-end", put(base + bytes(4), 0, len(base) + 4), error="Layout", count=4)
    add("gap-at-start", put(put(put(base[:156] + bytes(4) + base[156:], 0, 176), 136, 160), 148, 168), error="Layout", count=4)
    padded, _ = build([9])
    add("nonzero-padding", padded[:-1] + b"\x01", error="Layout", count=2)
    add("tag-reserved", put(base, 160, 1), error="Header", count=2)
    for max_bytes, max_tags in [(0, 128), (-1, 128), (171, 128), (1048576, 0), (1048576, -1), (1048576, 1)]:
        add(f"limits-{max_bytes}-{max_tags}", base, error="Limit", count=0, max_bytes=max_bytes, max_tags=max_tags)
    add("tag-limit-uint32", put(base, 128, 0xffffffff), error="Limit", count=0)
    add("tag-limit-hard", put(base, 128, 1025), error="Limit", count=0, max_tags=2147483647)
    valid("exact-caller-limits", build([8, 8]), max_bytes=172, max_tags=2)
    large = build([16777216 - 144])
    valid("exact-hard-byte-limit", large, max_bytes=2147483647)
    add("over-hard-byte-limit", put(large[0] + bytes(4), 0, 16777220), error="Limit", count=0, max_bytes=2147483647)
    for n in [0, 1, 3, 65]:
        payload, fields = build([8] * n)
        # Exhaust all polls on small profiles and the inner-loop boundary on 65 tags.
        stops = range(1, polls(n) + 1) if n < 65 else [1, 2, 130, polls(n) - 1, polls(n)]
        for stop in stops:
            add(f"cancel-{n}-{stop}", payload, error="Cancelled", count=stop, stop=stop)
    assert len({c["name"] for c in cases}) == len(cases)
    return dict(schema=1, reference=dict(Pillow=PIL.__version__, LittleCMS=features.version_module("littlecms2"),
                sha256=dict(generator=sha(Path(__file__).read_bytes()), frozen_profile=sha(real)),
                construction="Explicit layouts, table permutations, shared spans, policy mutations, and closed-form poll counts; no Fern parser reference."), cases=cases)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=Path)
    options = parser.parse_args()
    data = json.dumps(generate(), sort_keys=True, separators=(",", ":")).encode()
    options.output.parent.mkdir(parents=True, exist_ok=True)
    options.output.write_bytes(gzip.compress(data, mtime=0))
    print(json.dumps(dict(cases=len(json.loads(data)["cases"]), sha256=sha(options.output.read_bytes()))))


if __name__ == "__main__":
    main()
