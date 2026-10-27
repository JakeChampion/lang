"""Construct finite Exif payloads and independent primary-orientation observations."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import platform
import random
import struct
import warnings

from PIL import ExifTags, Image, TiffImagePlugin, TiffTags
import PIL


WIDTHS = {1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 7: 1, 9: 4, 10: 8, 129: 1}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def field(tag, kind, count, value, padding=b"\xa5\x5a\xc3\x3c"):
    assert len(value) == WIDTHS[kind] * count
    return dict(tag=tag, kind=kind, count=count, value=value, padding=padding)


def oriented(value, order):
    return field(274, 3, 1, struct.pack(order + "H", value))


def value_bytes(kind, count, order, rng):
    if kind in (1, 7):
        return bytes(rng.randrange(256) for _ in range(count))
    if kind in (2, 129):
        return b"x" * (count - 1) + b"\0"
    if kind in (3, 4, 9):
        code = {3: "H", 4: "I", 9: "i"}[kind]
        values = [rng.randrange(65536) * (-1 if kind == 9 else 1) for _ in range(count)]
        return struct.pack(order + str(count) + code, *values)
    code = "II" if kind == 5 else "ii"
    return b"".join(struct.pack(order + code, i + 1, 2) for i in range(count))


def build(first, order, second=None, first_at=8, second_at=None, trailing=b""):
    tables = [(first_at, first)]
    if second is not None:
        if second_at is None:
            second_at = first_at + 6 + 12 * len(first)
        tables.append((second_at, second))
    intervals = [(at, at + 6 + 12 * len(entries)) for at, entries in tables]
    if len(intervals) == 2:
        a, b = intervals
        assert a[1] <= b[0] or b[1] <= a[0]
    data = bytearray(max(end for _, end in intervals))
    data[:8] = (b"II" if order == "<" else b"MM") + struct.pack(order + "HI", 42, first_at)
    for index, (at, entries) in enumerate(tables):
        struct.pack_into(order + "H", data, at, len(entries))
        for i, entry in enumerate(entries):
            pos = at + 2 + 12 * i
            struct.pack_into(order + "HHI", data, pos, entry["tag"], entry["kind"], entry["count"])
            value = entry["value"]
            if len(value) <= 4:
                data[pos + 8:pos + 12] = value + entry["padding"][:4 - len(value)]
            else:
                if len(data) % 2:
                    data.append(0)
                struct.pack_into(order + "I", data, pos + 8, len(data))
                data.extend(value)
        next_at = tables[index + 1][0] if index + 1 < len(tables) else 0
        struct.pack_into(order + "I", data, at + 2 + 12 * len(entries), next_at)
    primary = next((int.from_bytes(e["value"], "little" if order == "<" else "big")
                    for e in first if e["tag"] == 274), None)
    semantic = dict(orientation=primary, order="II" if order == "<" else "MM",
                    directories=len(tables), entries=sum(len(e) for _, e in tables))
    pillow = all(e["kind"] in TiffTags.TYPES for _, entries in tables for e in entries)
    return b"Exif\0\0" + bytes(data) + trailing, semantic, pillow


def pillow_orientation(payload):
    with warnings.catch_warnings():
        warnings.simplefilter("error")
        exif = Image.Exif()
        exif.load(payload)
        value = exif.get(274)
    return None if value is None else int(value)


def generate():
    assert PIL.__version__ == "12.3.0", "Use the pinned offline Pillow reference"
    cases = []
    rng = random.Random(13638)

    def add(name, payload, expected, semantic=None, pillow=False, max_bytes=65533,
            max_entries=128, cancel_at=0):
        assert not any(c["name"] == name for c in cases), name
        reference = dict(applicable=bool(semantic is not None and pillow))
        if reference["applicable"]:
            observed = pillow_orientation(payload)
            assert observed == semantic["orientation"], (name, observed, semantic)
            reference["orientation"] = observed
        else:
            reference["reason"] = "wire type 129 is unavailable in Pillow" if semantic else "strict policy mutation, without a Pillow validity claim"
        cases.append(dict(name=name, payload_hex=payload.hex(), payload_sha256=sha(payload),
                          max_bytes=max_bytes, max_entries=max_entries, cancel_at=cancel_at,
                          expected=expected, construction=semantic, pillow=reference))

    def valid(name, built, **options):
        payload, semantic, pillow = built
        expected = dict(status="ok", **semantic, polls=semantic["entries"] + semantic["directories"] + 1)
        add(name, payload, expected, semantic, pillow, **options)

    def error(name, payload, kind, polls, **options):
        add(name, payload, dict(status="error", error=kind, polls=polls), **options)

    for order, label in [("<", "II"), (">", "MM")]:
        for value in range(1, 9):
            first = [oriented(value, order)]
            for offset in [8, 9, 128]:
                valid(f"{label}-orientation-{value}-offset-{offset}", build(first, order, first_at=offset))
            second = [oriented(9 - value, order)]
            valid(f"{label}-primary-{value}-thumbnail", build(first, order, second))
            valid(f"{label}-primary-{value}-reverse", build(first, order, second, first_at=128, second_at=8))
        valid(label + "-absent", build([], order))
        valid(label + "-primary-absent-thumbnail-present", build([], order, [oriented(8, order)]))
        for kind in WIDTHS:
            counts = [1, 4, 6] if WIDTHS[kind] == 1 else [1, 2, 3]
            for count in counts:
                unknown = field(60000, kind, count, value_bytes(kind, count, order, rng))
                valid(f"{label}-wire-{kind}-count-{count}", build([oriented(7, order), unknown], order))
        for value in [None, *range(1, 9)]:
            payload, _, _ = build([], order)
            exif = Image.Exif()
            exif.load(payload)
            exif[270] = "independent Pillow writer"
            exif[256] = 17
            if value is not None:
                exif[274] = value
            encoded = exif.tobytes()
            semantic = dict(orientation=value, order=label, directories=1, entries=2 + int(value is not None))
            valid(f"{label}-Pillow-writer-{value}", (encoded, semantic, True))

        payload, semantic, pillow = build([oriented(6, order)], order)
        for length in range(len(payload)):
            kind = "Directory" if length in (14, 15) else "Truncated"
            error(f"{label}-prefix-{length}", payload[:length], kind, 1)
        for index in range(6):
            changed = bytearray(payload); changed[index] ^= 1
            error(f"{label}-signature-{index}", bytes(changed), "Signature", 1)
        for marker in [b"IM", b"MI", b"\0\0"]:
            changed = bytearray(payload); changed[6:8] = marker
            error(f"{label}-order-{marker.hex()}", bytes(changed), "ByteOrder", 1)
        for version in [0, 43, 65535]:
            changed = bytearray(payload); struct.pack_into(order + "H", changed, 8, version)
            error(f"{label}-version-{version}", bytes(changed), "Version", 1)
        for offset in [0, 1, 7, 26, 2147483648, 4294967295]:
            changed = bytearray(payload); struct.pack_into(order + "I", changed, 10, offset)
            error(f"{label}-directory-offset-{offset}", bytes(changed), "Directory", 1)
        for count, kind in [(2, "Truncated"), (129, "Limit"), (65535, "Limit")]:
            changed = bytearray(payload); struct.pack_into(order + "H", changed, 14, count)
            error(f"{label}-directory-count-{count}", bytes(changed), kind, 1)
        for value in [0, 9, 65535]:
            bad, _, _ = build([oriented(value, order)], order)
            error(f"{label}-reserved-orientation-{value}", bad, "Value", 2)
        for count in [0, 2, 2147483648, 4294967295]:
            changed = bytearray(payload); struct.pack_into(order + "I", changed, 20, count)
            error(f"{label}-orientation-count-{count}", bytes(changed), "Value", 2)
        for kind in [1, 2, 4, 5, 7, 9, 10, 129]:
            changed = bytearray(payload); struct.pack_into(order + "H", changed, 18, kind)
            error(f"{label}-orientation-type-{kind}", bytes(changed), "Value", 2)
        for kind in [0, 6, 8, 11, 12, 13, 16, 128, 130, 65535]:
            changed = bytearray(payload); struct.pack_into(order + "H", changed, 18, kind)
            error(f"{label}-unsupported-type-{kind}", bytes(changed), "Type", 2)
        one = oriented(1, order)
        bad, _, _ = build([one, one], order)
        error(label + "-duplicate-tag", bad, "Duplicate", 3)
        low = field(270, 1, 1, b"x")
        bad, _, _ = build([one, low], order)
        error(label + "-unsorted-tag", bad, "Directory", 3)
        bad, _, _ = build([low, one, low], order)
        error(label + "-nonadjacent-duplicate-tag", bad, "Directory", 4)
        for offset in [0, 7, 26, 2147483648, 4294967295]:
            changed = bytearray(payload)
            struct.pack_into(order + "HHII", changed, 16, 60000, 2, 6, offset)
            error(f"{label}-value-offset-{offset}", bytes(changed), "Value", 2)
        for count in [0, 2147483648, 4294967295]:
            changed = bytearray(payload)
            struct.pack_into(order + "HHII", changed, 16, 60000, 5, count, 8)
            error(f"{label}-value-count-{count}", bytes(changed), "Value", 2)
        for offset in [8, 9, 25, 26, 4294967295]:
            changed = bytearray(payload); struct.pack_into(order + "I", changed, 28, offset)
            error(f"{label}-next-directory-{offset}", bytes(changed), "Directory", 3)
        two, sem2, pil2 = build([one], order, [oriented(8, order)])
        for offset in [8, 26, 44, 4294967295]:
            changed = bytearray(two); struct.pack_into(order + "I", changed, 46, offset)
            error(f"{label}-third-directory-{offset}", bytes(changed), "Directory", 4)
        overlap = bytearray(18)
        overlap[:8] = (b"II" if order == "<" else b"MM") + struct.pack(order + "HI", 42, 12)
        struct.pack_into(order + "I", overlap, 14, 8)
        error(label + "-overlapping-empty-tables", b"Exif\0\0" + overlap, "Directory", 2)

        for cap in [-2147483648, -1, 0, len(payload) - 1]:
            error(f"{label}-byte-budget-{cap}", payload, "Limit", 0,
                  semantic=semantic, pillow=pillow, max_bytes=cap)
        for cap in [-2147483648, -1, 0]:
            error(f"{label}-entry-budget-{cap}", payload, "Limit", 0,
                  semantic=semantic, pillow=pillow, max_entries=cap)
        valid(label + "-exact-byte-entry-budget", (payload, semantic, pillow),
              max_bytes=len(payload), max_entries=1)
        error(label + "-second-directory-global-budget", two, "Limit", 3,
              semantic=sem2, pillow=pil2, max_entries=1)
        for tag, built in [("one", (payload, semantic, pillow)), ("two", (two, sem2, pil2))]:
            raw, sem, pil = built
            for stop in range(1, sem["entries"] + sem["directories"] + 2):
                error(f"{label}-cancel-{tag}-{stop}", raw, "Cancelled", stop,
                      semantic=sem, pillow=pil, cancel_at=stop)
            valid(f"{label}-cancel-after-{tag}", built,
                  cancel_at=sem["entries"] + sem["directories"] + 2)

    for index in range(128):
        order = "<" if index % 2 == 0 else ">"
        value = rng.choice([None, *range(1, 9)])
        entries = [] if value is None else [oriented(value, order)]
        for tag in range(1000, 1000 + rng.randrange(17)):
            kind = rng.choice(list(WIDTHS))
            count = rng.randrange(1, 7)
            entries.append(field(tag, kind, count, value_bytes(kind, count, order, rng)))
        second = [oriented(rng.randrange(1, 9), order)] if index % 3 == 0 else None
        valid(f"seed13638-{index}", build(entries, order, second,
                                          first_at=[8, 10, 127][index % 3],
                                          trailing=rng.randbytes(index % 19)))

    for count in [128, 4096, 4097]:
        fields = [field(10000 + i, 1, 1, b"x") for i in range(count)]
        built = build(fields, "<")
        if count <= 4096:
            valid(f"entry-ceiling-{count}", built, max_entries=count)
            if count == 4096:
                for stop in [1, 2, 4096, 4097, 4098]:
                    error(f"entry-ceiling-cancel-{stop}", built[0], "Cancelled", stop,
                          semantic=built[1], pillow=built[2], max_entries=count, cancel_at=stop)
                valid("entry-ceiling-cancel-after", built, max_entries=count, cancel_at=4099)
        else:
            error("absolute-entry-ceiling", built[0], "Limit", 1,
                  semantic=built[1], pillow=built[2], max_entries=2147483647)
    built = build([oriented(3, "<")], "<")
    valid("exact-absolute-byte-ceiling", (built[0].ljust(65533, b"\0"), *built[1:]), max_bytes=2147483647)
    error("absolute-byte-ceiling", built[0].ljust(65534, b"\0"), "Limit", 0,
          semantic=built[1], pillow=built[2], max_bytes=2147483647)
    nested = build([oriented(1, "<"), field(34665, 4, 1, struct.pack("<I", 4294967295))], "<")
    valid("nested-pointer-unfollowed", nested)
    reference = {"specification": "CIPA DC-008-Translation-2026 Exif 3.1",
                 "specification_sha256": "9cc36399a46ab7aa4a65473bb8a4945d3b044da739b8f16a01b0be11886dcff9",
                 "Python": platform.python_version(), "Pillow": PIL.__version__,
                 "seed": 13638, "expected": "declarative struct construction and explicit strict-policy mutations",
                 "pillow_scope": "primary orientation on applicable payloads, without complete validity or compatibility claims",
                 "sha256": {"generator": sha(Path(__file__).read_bytes()),
                            **{module.__name__: sha(Path(module.__file__).read_bytes())
                               for module in [Image, TiffImagePlugin, TiffTags, ExifTags]}}}
    return dict(schema=1, reference=reference, cases=cases)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path,
                        default=Path(__file__).parent / "fixtures/jpeg-exif-reference.json.gz")
    options = parser.parse_args()
    document = generate()
    data = gzip.compress(json.dumps(document, sort_keys=True, separators=(",", ":")).encode(), mtime=0)
    options.output.write_bytes(data)
    print(json.dumps(dict(cases=len(document["cases"]),
                          successful=sum(c["expected"]["status"] == "ok" for c in document["cases"]),
                          Pillow_observations=sum(c["pillow"]["applicable"] for c in document["cases"]),
                          bundle_sha256=sha(data)), sort_keys=True))


if __name__ == "__main__":
    main()
