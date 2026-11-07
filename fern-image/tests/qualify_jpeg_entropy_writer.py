"""Compare entropy bytes and recover coefficients with an offline JPEG oracle."""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import random
import re
import struct
import subprocess
import tempfile


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def permutation():
    result = []
    for diagonal in range(15):
        cells = [(y, diagonal - y) for y in range(8) if 0 <= diagonal - y < 8]
        if diagonal % 2 == 0:
            cells.reverse()
        result.extend(y * 8 + x for y, x in cells)
    return result


def table(ac, length=8):
    symbols = [x for x in range(256) if (1 <= x % 16 <= 10 or x in (0, 240))] if ac else list(range(12))
    return {"counts": [len(symbols) if i == length - 1 else 0 for i in range(16)], "symbols": symbols}


class Rejected(ValueError):
    pass


def canonical(specification, ac):
    result = {}
    offset = 0
    first = 0
    for length, count in enumerate(specification["counts"], 1):
        if first + count >= 2 ** length or offset + count > len(specification["symbols"]):
            raise Rejected("Table")
        for code in range(first, first + count):
            symbol = specification["symbols"][offset]
            legal = (1 <= symbol % 16 <= 10 or symbol in (0, 240)) if ac else symbol <= 11
            if not legal or symbol in result:
                raise Rejected("Table")
            result[symbol] = format(code, f"0{length}b")
            offset += 1
        first = (first + count) * 2
    if not offset or offset != len(specification["symbols"]):
        raise Rejected("Table")
    return result


def reference(case):
    values, dc, ac = case["values"], case["dc"], case["ac"]
    if not values or len(values) % 64 or any(len(t["counts"]) != 16 or len(t["symbols"]) > 256 for t in (dc, ac)):
        raise Rejected("Shape")
    if not 0 <= case["restart"] <= 65535 or case["limit"] < 0:
        raise Rejected("Limit")
    dc_codes, ac_codes = canonical(dc, False), canonical(ac, True)
    output = bytearray()
    bits = ""
    previous = 0
    marker = 0

    def append(symbol, codes, value=0):
        nonlocal bits
        if symbol not in codes:
            raise Rejected("MissingSymbol")
        bits += codes[symbol]
        size = abs(value).bit_length()
        if size:
            bits += format(value if value > 0 else value + 2 ** size - 1, f"0{size}b")

    def flush():
        nonlocal bits
        bits += "1" * (-len(bits) % 8)
        output.extend(bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8)).replace(b"\xff", b"\xff\0"))
        bits = ""

    for block_index in range(len(values) // 64):
        block = values[block_index * 64:(block_index + 1) * 64]
        if not -1024 <= block[0] <= 1023:
            raise Rejected("Coefficient")
        delta = block[0] - previous
        append(abs(delta).bit_length(), dc_codes, delta)
        previous = block[0]
        run = 0
        for natural in permutation()[1:]:
            value = block[natural]
            if not -1023 <= value <= 1023:
                raise Rejected("Coefficient")
            if value == 0:
                run += 1
            else:
                for _ in range(run // 16):
                    append(240, ac_codes)
                append((run % 16) * 16 + abs(value).bit_length(), ac_codes, value)
                run = 0
        if run:
            append(0, ac_codes)
        if case["restart"] and (block_index + 1) % case["restart"] == 0 and (block_index + 1) * 64 < len(values):
            flush()
            output.extend((255, 208 + marker))
            marker = (marker + 1) % 8
            previous = 0
    flush()
    if len(output) > case["limit"]:
        raise Rejected("Limit")
    return bytes(output)


def cases():
    base = {"values": [0] * 64, "dc": table(False, 4), "ac": table(True), "restart": 0, "limit": 1000000}
    yield "zero", copy.deepcopy(base)
    for index in range(64):
        for size in range(1, 12 if index == 0 else 11):
            for sign in (-1, 1):
                case = copy.deepcopy(base)
                value = min((1 << size) - 1, 1024 if sign < 0 and index == 0 else 1023)
                case["values"][index] = sign * value
                yield f"impulse-{index}-{size}-{sign}", case
    rng = random.Random(14426)
    for length in (4, 8, 16):
        for restart in (0, 1, 2, 3, 9, 65535):
            for count in (1, 2, 10, 19):
                case = copy.deepcopy(base)
                case["dc"] = table(False, length)
                case["ac"] = table(True, max(length, 8))
                rng.shuffle(case["dc"]["symbols"])
                rng.shuffle(case["ac"]["symbols"])
                case["restart"] = restart
                case["values"] = [rng.choice((0, 0, 0, -1023, -1, 1, 1023)) for _ in range(64 * count)]
                for i in range(count):
                    case["values"][i * 64] = (-1024, 1023)[i % 2]
                yield f"random-{length}-{restart}-{count}", case
    for index in (0, 1, 63):
        for value in (-2147483648, -1025, 1024, 2147483647):
            case = copy.deepcopy(base)
            case["values"][index] = value
            yield f"invalid-coefficient-{index}-{value}", case
    for length in (0, 1, 63, 65):
        case = copy.deepcopy(base)
        case["values"] = [0] * length
        yield f"shape-{length}", case
    for restart in (-1, 65536):
        case = copy.deepcopy(base)
        case["restart"] = restart
        yield f"restart-{restart}", case
    for which in ("dc", "ac"):
        for defect in ("duplicate", "extra", "short", "empty", "oversubscribed", "all-ones", "illegal", "missing"):
            case = copy.deepcopy(base)
            t = case[which]
            if defect == "duplicate": t["symbols"][0] = t["symbols"][1]
            if defect == "extra": t["symbols"].append(0)
            if defect == "short": t["symbols"].pop()
            if defect == "empty": t["symbols"] = []; t["counts"] = [0] * 16
            if defect == "oversubscribed": t["counts"][0] = 3
            if defect == "all-ones": t["counts"] = [2] + [0] * 15; t["symbols"] = [0, 1]
            if defect == "illegal": t["symbols"][0] = 16 if which == "ac" else 12
            if defect == "missing": t["counts"] = [1] + [0] * 15; t["symbols"] = [1]
            yield f"{which}-{defect}", case
    for limit in (-1, 0, 1, 2, 3):
        case = copy.deepcopy(base)
        case["limit"] = limit
        yield f"limit-{limit}", case


def payload(case):
    result = struct.pack(">ii", case["restart"], case["limit"])
    for t in (case["dc"], case["ac"]):
        result += bytes(t["counts"]) + struct.pack(">H", len(t["symbols"])) + bytes(t["symbols"])
    return result + b"".join(struct.pack(">i", v) for v in case["values"])


def jpeg(case, encoded):
    def segment(tag, data):
        return bytes((255, tag)) + struct.pack(">H", len(data) + 2) + data
    quant = segment(219, bytes(1) + bytes([1] * 64))
    dht = b"".join(bytes([i * 16]) + bytes(case[k]["counts"]) + bytes(case[k]["symbols"]) for i, k in enumerate(("dc", "ac")))
    frame = segment(192, struct.pack(">BHHBBBB", 8, len(case["values"]) // 64 * 8, 8, 1, 1, 17, 0))
    restart = segment(221, struct.pack(">H", case["restart"])) if case["restart"] else b""
    return b"\xff\xd8" + quant + segment(196, dht) + frame + restart + segment(218, bytes([1, 1, 0, 0, 63, 0])) + encoded + b"\xff\xd9"


def observation(case, line):
    try:
        expected = "ok " + reference(case).hex()
    except Rejected as error:
        expected = "error " + str(error)
    if line != expected:
        raise ValueError(f"entropy mismatch: {line[:160]} != {expected[:160]}")
    return expected


def heap(stderr):
    reports = re.findall(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)", stderr)
    if len(reports) != 1 or any(int(a) <= 0 or a != b or c != "0" for a, b, c in reports):
        raise ValueError("missing or unbalanced heap report")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("oracle", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--wasmtime", type=Path)
    parser.add_argument("--reference-library", type=Path, action="append", required=True)
    parser.add_argument("--small", action="store_true")
    args = parser.parse_args()
    tracked = [Path(__file__), Path(__file__).with_name("jpeg_entropy_writer.fern"), Path(__file__).with_name("reference_jpeg_coefficients.c"), args.binary, args.compiler, args.oracle]
    tracked += args.reference_library
    if args.wasmtime: tracked.append(args.wasmtime)
    def inventory():
        paths = tracked + sorted((Path(__file__).parent.parent / "packages").rglob("*.fern"))
        return {str(p.resolve()): digest(p) for p in paths}
    frozen = inventory()
    samples = list(cases())
    if args.small: samples = samples[:8] + samples[-8:]
    rows = []
    batches = []
    prefix = [str(args.wasmtime.resolve()), "run", "--dir=.", str(args.binary.resolve())] if args.wasmtime else [str(args.binary.resolve())]
    with tempfile.TemporaryDirectory(prefix="jpeg-entropy-") as directory:
        root = Path(directory)
        for start in range(0, len(samples), 8):
            batch = samples[start:start + 8]
            paths = []
            for offset, (_, case) in enumerate(batch):
                path = root / f"{start + offset}.bin"
                path.write_bytes(payload(case))
                paths.append(path)
            run = subprocess.run(prefix + [p.name for p in paths], cwd=root, capture_output=True, text=True, timeout=60, env={**os.environ, "FERN_LEAKCHECK": "1"})
            run.check_returncode()
            heap(run.stderr)
            lines = run.stdout.splitlines()
            if len(lines) != len(batch): raise ValueError("incomplete case output")
            for (name, case), path, line in zip(batch, paths, lines):
                if not line.startswith(path.name + " "): raise ValueError("wrong case identity")
                observed = observation(case, line[len(path.name) + 1:])
                row = {"name": name, "observation": observed, "input_sha256": digest(path)}
                if observed.startswith("ok "):
                    encoded = bytes.fromhex(observed[3:])
                    image = root / "encoded.jpg"
                    image.write_bytes(jpeg(case, encoded))
                    oracle = subprocess.run([str(args.oracle.resolve()), str(image)], capture_output=True, text=True, timeout=30)
                    oracle.check_returncode()
                    if oracle.stderr: raise ValueError(oracle.stderr)
                    result = json.loads(oracle.stdout)
                    expected_blocks = [case["values"][i:i + 64] for i in range(0, len(case["values"]), 64)]
                    if result["width"] != 8 or result["height"] != len(expected_blocks) * 8 or len(result["components"]) != 1 or result["components"][0]["blocks"] != expected_blocks:
                        raise ValueError("independent coefficient mismatch")
                    row.update(oracle=result["reference"], jpeg_sha256=digest(image), oracle_stdout_sha256=hashlib.sha256(oracle.stdout.encode()).hexdigest())
                rows.append(row)
            batches.append({"cases": len(batch), "stderr_sha256": hashlib.sha256(run.stderr.encode()).hexdigest()})
    if frozen != inventory(): raise ValueError("source or tool changed")
    result = {"complete": True, "files_sha256": frozen, "cases": rows, "batches": batches}
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"cases": len(rows), "accepted": sum(r["observation"].startswith("ok ") for r in rows), "batches": len(batches)}))


if __name__ == "__main__":
    main()
