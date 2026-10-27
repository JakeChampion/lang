"""Construct exact fragment bytes and compact reproducible profile recipes."""

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import random
import struct


IDENTIFIER = b"ICC_PROFILE\0"
FIXTURE = Path(__file__).with_name("fixtures") / "icc-feasibility-srgb.icc"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def profile_bytes(recipe):
    if recipe["kind"] == "frozen":
        return FIXTURE.read_bytes()
    length = recipe["length"]
    pattern = bytes.fromhex(recipe["pattern_hex"])
    return (pattern * ((length + len(pattern) - 1) // len(pattern)))[:length]


def materialize(case):
    profile = profile_bytes(case["profile"])
    payloads = []
    for part in case["chunks"]:
        payload = bytes.fromhex(part.get("identifier_hex", IDENTIFIER.hex())) + bytes([part["sequence"], part["total"]])
        payload += profile[part["start"]:part["start"] + part["length"]]
        if "truncate" in part:
            payload = payload[:part["truncate"]]
        payloads.append(payload)
    return profile, payloads


def packet(case, expected_override=None):
    profile, payloads = materialize(case)
    status = case["expected"]["status"]
    expected = -1 if status == "error" else -2 if status == "none" else len(profile)
    data = profile if status == "some" else b""
    if expected_override is not None:
        if status != "some" or len(expected_override) != len(profile):
            raise ValueError("expected-file fault shape")
        data = expected_override
    header = b"FIA1" + struct.pack("<iiiii", case["max_profile_bytes"], case["max_chunks"], case["cancel_at"], len(payloads), expected)
    return header + b"".join(struct.pack("<i", len(p)) + p for p in payloads) + data


def pillow_observation(profile, supplied_payloads=None):
    import PIL
    from PIL import Image, features
    stream = io.BytesIO()
    Image.new("RGB", (3, 2), (17, 33, 66)).save(stream, format="JPEG", quality=75, icc_profile=profile)
    jpeg = stream.getvalue()
    if supplied_payloads is not None:
        bare = io.BytesIO()
        Image.new("RGB", (3, 2), (17, 33, 66)).save(bare, format="JPEG", quality=75)
        original = bare.getvalue()
        if original[:4] != b"\xff\xd8\xff\xe0":
            raise ValueError("offline JPEG APP0 position")
        insert = 4 + struct.unpack_from(">H", original, 4)[0]
        segments = b"".join(b"\xff\xe2" + struct.pack(">H", len(p) + 2) + p for p in supplied_payloads)
        jpeg = original[:insert] + segments + original[insert:]
    with Image.open(io.BytesIO(jpeg)) as image:
        observed = image.info.get("icc_profile")
    if observed != profile:
        raise ValueError("offline Pillow metadata differs from the supplied profile")
    at = 2
    payloads = []
    while at < len(jpeg):
        if jpeg[at] != 255:
            raise ValueError("offline JPEG marker framing")
        marker = jpeg[at + 1]
        if marker == 218:
            break
        length = struct.unpack_from(">H", jpeg, at + 2)[0]
        if length < 2 or at + 2 + length > len(jpeg):
            raise ValueError("offline JPEG segment length")
        if marker == 226:
            payloads.append(jpeg[at + 4:at + 2 + length])
        at += 2 + length
    return dict(Pillow=PIL.__version__, encoder=features.version_feature("libjpeg_turbo"), JPEG_API=features.version_codec("jpg"), jpeg_sha256=sha(jpeg),
                profile_sha256=sha(observed), payload_sha256=[sha(p) for p in payloads])


def generate():
    import PIL
    from PIL import features
    rng = random.Random(13660)
    cases = []

    def recipe(length):
        return dict(kind="pattern", length=length, pattern_hex=rng.randbytes(256).hex())

    def chunks(length, count, order="forward", maximum_parts=False):
        sizes = [min(65519, max(0, length - i * 65519)) for i in range(count)] if maximum_parts else [length // count + (i < length % count) for i in range(count)]
        assert sum(sizes) == length and all(0 <= n <= 65519 for n in sizes)
        parts = []
        at = 0
        for i, size in enumerate(sizes):
            parts.append(dict(sequence=i + 1, total=count, start=at, length=size))
            at += size
        if order == "reverse":
            parts.reverse()
        elif order == "shuffle":
            rng.shuffle(parts)
        return parts

    def add(name, profile, parts, error=None, stop=0, maximum=1048576, chunk_limit=32, observed=False, constructed=False):
        case = dict(name=name, profile=profile, chunks=parts, max_profile_bytes=maximum, max_chunks=chunk_limit, cancel_at=stop)
        raw, payloads = materialize(case)
        polls = 2 if not parts else 3 + (len(raw) + 63) // 64
        if error:
            expected = dict(status="error", error=error, polls=stop if error == "Cancelled" else 0)
        elif not parts:
            expected = dict(status="none", polls=polls)
        else:
            expected = dict(status="some", polls=polls, length=len(raw), profile_sha256=sha(raw), checked_bytes=len(raw))
        case["expected"] = expected
        case["payload_sha256"] = [sha(p) for p in payloads]
        case["packet_sha256"] = sha(packet(case))
        if observed:
            case["Pillow_constructed"] = constructed
            case["Pillow_observation"] = pillow_observation(raw, payloads if constructed else None)
            assert case["Pillow_observation"]["payload_sha256"] == case["payload_sha256"]
        cases.append(case)
        return case

    add("absence", recipe(0), [])
    for length in [0, 1, 2, 63, 64, 65, 127, 128, 129, 130, 257, 1024]:
        for count in [1, 2, 3, 7, 31, 32, 255]:
            profile = recipe(length)
            for order in ["forward", "reverse", "shuffle"]:
                add(f"sequence-{length}-{count}-{order}", profile, chunks(length, count, order), chunk_limit=max(32, count))
    for length in [65519, 65520, 131039]:
        minimum = (length + 65518) // 65519
        for count in [minimum, minimum + 1, 255]:
            profile = recipe(length)
            for order in ["forward", "reverse", "shuffle"]:
                add(f"large-{length}-{count}-{order}", profile, chunks(length, count, order), chunk_limit=max(32, count))
    maximum = recipe(16707345)
    add("maximum-container-reverse", maximum, chunks(16707345, 255, "reverse"), maximum=16707345, chunk_limit=255)
    for i, length in enumerate([588, 65519, 65520, 131039, 16707345]):
        profile = dict(kind="frozen") if i == 0 else recipe(length)
        count = (length + 65518) // 65519
        add(f"Pillow-metadata-{length}", profile, chunks(length, count, maximum_parts=True), maximum=max(1048576, length), chunk_limit=max(32, count), observed=True)
    add("Pillow-constructed-reverse", dict(kind="frozen"), chunks(588, 3, "reverse"), observed=True, constructed=True)
    add("Pillow-constructed-empty-fragment", dict(kind="frozen"), [dict(sequence=3, total=3, start=1, length=587), dict(sequence=1, total=3, start=0, length=1), dict(sequence=2, total=3, start=1, length=0)], observed=True, constructed=True)

    one = recipe(3)
    for length in range(14):
        add(f"prefix-{length}", one, [dict(sequence=1, total=1, start=0, length=3, truncate=length)], "Length")
    for index in range(12):
        identifier = bytearray(IDENTIFIER)
        identifier[index] ^= 1
        add(f"identifier-{index}", one, [dict(sequence=1, total=1, start=0, length=3, identifier_hex=identifier.hex())], "Identifier")
    for sequence, total in [(0, 1), (1, 0), (2, 1), (255, 254)]:
        add(f"sequence-error-{sequence}-{total}", one, [dict(sequence=sequence, total=total, start=0, length=3)], "Sequence", chunk_limit=255)
    add("inconsistent-total", one, [dict(sequence=1, total=2, start=0, length=1), dict(sequence=2, total=3, start=1, length=2)], "Count")
    add("duplicate", one, [dict(sequence=1, total=2, start=0, length=1), dict(sequence=1, total=2, start=1, length=2)], "Duplicate")
    add("missing", one, [dict(sequence=2, total=2, start=0, length=3)], "Missing")
    add("declared-count-limit", one, [dict(sequence=1, total=33, start=0, length=3)], "Limit")
    add("over-wire-payload", recipe(65520), [dict(sequence=1, total=1, start=0, length=65520)], "Length")
    add("over-wire-count", recipe(0), [dict(sequence=1, total=255, start=0, length=0)] * 256, "Limit", chunk_limit=2147483647)
    for maximum in [-2147483648, -1, 0, 2]:
        add(f"profile-limit-{maximum}", one, chunks(3, 1), "Limit", maximum=maximum)
    for limit in [-2147483648, -1, 0, 1]:
        add(f"chunk-limit-{limit}", one, chunks(3, 2), "Limit", chunk_limit=limit)
    add("exact-byte-and-count-limits", one, chunks(3, 2, "reverse"), maximum=3, chunk_limit=2)
    add("permissive-caller-cannot-raise-wire-cap", recipe(255), chunks(255, 255), maximum=2147483647, chunk_limit=2147483647)
    for length in [0, 1, 63, 64, 65, 130]:
        profile = recipe(length)
        parts = chunks(length, 3, "reverse")
        polls = 3 + (length + 63) // 64
        for stop in range(1, polls + 1):
            add(f"cancel-{length}-{stop}", profile, parts, "Cancelled", stop=stop)
        add(f"late-cancel-{length}", profile, parts, stop=polls + 1)
    for stop in [1, 2]:
        add(f"cancel-absence-{stop}", recipe(0), [], "Cancelled", stop=stop)
    assert len({case["name"] for case in cases}) == len(cases)
    manifest = json.loads(FIXTURE.with_name("icc-feasibility-reference.json").read_text())
    assert sha(FIXTURE.read_bytes()) == manifest["sha256"]
    return dict(schema=1, reference=dict(sha256=dict(generator=sha(Path(__file__).read_bytes()), frozen_profile=sha(FIXTURE.read_bytes())),
                construction="Declared deterministic profile bytes and sequence partitions; exact expected files. Pillow metadata observations apply only to valid fragment framing, not strict error policy or color transforms.",
                Pillow=PIL.__version__, JPEG_encoder=features.version_feature("libjpeg_turbo"), JPEG_API=features.version_codec("jpg"), seed=13660), cases=cases)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=Path)
    options = parser.parse_args()
    document = generate()
    options.output.parent.mkdir(parents=True, exist_ok=True)
    options.output.write_bytes(gzip.compress(json.dumps(document, sort_keys=True, separators=(",", ":")).encode(), mtime=0))
    print(json.dumps(dict(cases=len(document["cases"]), sha256=sha(options.output.read_bytes()))))


if __name__ == "__main__":
    main()
