"""Compare complete-MCU entropy with bit strings and offline libjpeg coefficients."""

import argparse
import copy
import hashlib
import itertools
import json
from pathlib import Path
import random
import re
import struct
import subprocess
import sys
import tempfile

from qualify_jpeg_entropy_writer import canonical, permutation, Rejected, table


def sha(data):
    return hashlib.sha256(data).hexdigest()


def membership(case):
    components = case["components"]
    if case["mcus"] <= 0 or not 1 <= len(components) <= 4 or not 1 <= len(case["dc"]) <= 2 or not 1 <= len(case["ac"]) <= 2:
        raise Rejected("Shape")
    if any(len(t["counts"]) != 16 or len(t["symbols"]) > 256 for t in case["dc"] + case["ac"]):
        raise Rejected("Shape")
    members = []
    for i, (h, v, dc, ac) in enumerate(components):
        if not (1 <= h <= 4 and 1 <= v <= 4):
            raise Rejected("Shape")
        if not (0 <= dc < len(case["dc"]) and 0 <= ac < len(case["ac"])):
            raise Rejected("Table")
        members.extend([i] * (h * v if len(components) > 1 else 1))
    if len(members) > 10:
        raise Rejected("Shape")
    if not 0 <= case["restart"] <= 65535 or case["limit"] < 0 or len(members) * case["mcus"] > 2147483647:
        raise Rejected("Limit")
    return members


def reference(case):
    members = membership(case)
    dc_codes = [canonical(t, False) for t in case["dc"]]
    ac_codes = [canonical(t, True) for t in case["ac"]]
    previous = [0] * len(case["components"])
    output, bits = bytearray(), ""
    marker = 0

    def raw(byte):
        if len(output) >= case["limit"]:
            raise Rejected("Limit")
        output.append(byte)

    def append(text):
        nonlocal bits
        bits += text
        while len(bits) >= 8:
            byte = int(bits[:8], 2)
            bits = bits[8:]
            raw(byte)
            if byte == 255:
                raw(0)

    def symbol(codes, number, value=0):
        if number not in codes:
            raise Rejected("MissingSymbol")
        append(codes[number])
        size = abs(value).bit_length()
        if size:
            append(format(value if value > 0 else value + 2 ** size - 1, f"0{size}b"))

    values = case["values"]
    count = 0
    for at in range(0, len(values), 64):
        block = values[at:at + 64]
        if len(block) != 64 or count >= case["mcus"] * len(members):
            raise Rejected("Shape")
        if case["restart"] and count and count % len(members) == 0 and count // len(members) % case["restart"] == 0:
            append("1" * (-len(bits) % 8))
            raw(255)
            raw(208 + marker)
            marker = (marker + 1) % 8
            previous = [0] * len(previous)
        if not -1024 <= block[0] <= 1023 or any(not -1023 <= value <= 1023 for value in block[1:]):
            raise Rejected("Coefficient")
        component = members[count % len(members)]
        _, _, dc, ac = case["components"][component]
        delta = block[0] - previous[component]
        symbol(dc_codes[dc], abs(delta).bit_length(), delta)
        previous[component] = block[0]
        run = 0
        for natural in permutation()[1:]:
            value = block[natural]
            if not value:
                run += 1
            else:
                for _ in range(run // 16):
                    symbol(ac_codes[ac], 240)
                symbol(ac_codes[ac], run % 16 * 16 + abs(value).bit_length(), value)
                run = 0
        if run:
            symbol(ac_codes[ac], 0)
        count += 1
    if count != case["mcus"] * len(members):
        raise Rejected("Shape")
    append("1" * (-len(bits) % 8))
    return bytes(output)


def cases():
    rng = random.Random(14479)
    patterns = [((4, 4),), ((1, 1),) * 3, ((2, 2), (1, 1), (1, 1)), ((2, 1), (1, 1), (1, 1)),
                ((1, 2), (1, 1), (1, 1)), ((3, 1), (2, 2)), ((4, 1), (1, 2), (2, 2)), ((2, 2), (1, 1), (1, 1), (1, 1))]
    for pattern, mcus, restart, variant in itertools.product(patterns, [1, 2, 3, 19], [0, 1, 2, 7, 65535], range(2)):
        dc = [table(False, 4), table(False, 8)]
        ac = [table(True), table(True, 16)]
        dc[1]["symbols"].reverse()
        ac[1]["symbols"].reverse()
        components = [(h, v, i % 2 if variant else 0, (i + 1) % 2 if variant else 0) for i, (h, v) in enumerate(pattern)]
        case = {"mcus": mcus, "restart": restart, "limit": 1000000, "components": components, "dc": dc, "ac": ac, "values": []}
        members = membership(case)
        for mcu, component in itertools.product(range(mcus), members):
            block = [0] * 64
            block[0] = [-1024, -511, 0, 511, 1023][(mcu + component * 2) % 5]
            if variant:
                for k in [1, 16, 32, 48, 63]:
                    block[permutation()[k]] = rng.choice([-1023, -127, -1, 1, 127, 1023])
            case["values"].extend(block)
        yield case
    base = {"mcus": 2, "restart": 1, "limit": 1000000, "components": [(1, 1, 0, 0)] * 3,
            "dc": [table(False, 4)], "ac": [table(True)], "values": [0] * (6 * 64)}
    size = len(reference(base))
    for limit in [0, 1, size - 1, size, size + 1]:
        yield {**copy.deepcopy(base), "limit": limit}
    for length in [0, 1, 63, 64, 6 * 64 - 1, 6 * 64 + 1, 7 * 64]:
        yield {**copy.deepcopy(base), "values": [0] * length}
    for field, values in [("mcus", [-1, 0, 2147483647]), ("restart", [-1, 65536]), ("limit", [-1])]:
        for value in values:
            yield {**copy.deepcopy(base), field: value}
    for components in [[], [(0, 1, 0, 0)], [(1, 5, 0, 0)], [(1, 1, -1, 0)], [(1, 1, 0, 1)],
                       [(4, 2, 0, 0), (3, 1, 0, 0)], [(1, 1, 0, 0)] * 5]:
        yield {**copy.deepcopy(base), "components": components}
    for field in ["dc", "ac"]:
        for items in [[], [table(field == "ac")] * 3, [{"counts": [0] * 16, "symbols": []}]]:
            yield {**copy.deepcopy(base), field: items}
    for at, value in [(0, -1025), (0, 1024), (1, -1024), (63, 1024)]:
        case = copy.deepcopy(base)
        case["values"][at] = value
        yield case
    case = copy.deepcopy(base)
    case["dc"] = [{"counts": [1] + [0] * 15, "symbols": [0]}]
    case["values"][0] = 1
    yield case


def encoded(case):
    result = struct.pack(">6i", case["mcus"], case["restart"], case["limit"], len(case["components"]), len(case["dc"]), len(case["ac"]))
    result += b"".join(struct.pack(">4i", *c) for c in case["components"])
    for t in case["dc"] + case["ac"]:
        result += bytes(t["counts"]) + struct.pack(">H", len(t["symbols"])) + bytes(t["symbols"])
    return result + struct.pack(">" + "i" * len(case["values"]), *case["values"])


def segment(marker, payload):
    return bytes([255, marker]) + struct.pack(">H", len(payload) + 2) + payload


def frame(case, entropy):
    components = case["components"]
    hmax, vmax = max(c[0] for c in components), max(c[1] for c in components)
    width, height = (case["mcus"] * 8, 8) if len(components) == 1 else (case["mcus"] * hmax * 8, vmax * 8)
    output = b"\xff\xd8" + segment(219, b"\0" + bytes([1]) * 64)
    sof = struct.pack(">BHHB", 8, height, width, len(components))
    sof += b"".join(bytes([i + 1, h * 16 + v, 0]) for i, (h, v, _, _) in enumerate(components))
    output += segment(192, sof)
    for kind in [0, 1]:
        for i, t in enumerate(case["dc"] if kind == 0 else case["ac"]):
            output += segment(196, bytes([kind * 16 + i]) + bytes(t["counts"]) + bytes(t["symbols"]))
    if case["restart"]:
        output += segment(221, struct.pack(">H", case["restart"]))
    sos = bytes([len(components)]) + b"".join(bytes([i + 1, dc * 16 + ac]) for i, (_, _, dc, ac) in enumerate(components)) + bytes([0, 63, 0])
    return output + segment(218, sos) + entropy + b"\xff\xd9"


def coefficient_lines(case):
    components = case["components"]
    members = membership(case)
    if len(components) == 1:
        width, height = case["mcus"] * 8, 8
    else:
        width = case["mcus"] * max(c[0] for c in components) * 8
        height = max(c[1] for c in components) * 8
    result = [f"frame {width} {height} {len(components)}"]
    first = 0
    for component, (h, v, _, _) in enumerate(components):
        if len(components) == 1:
            h = v = 1
        result.append(f"component {component} {case['mcus'] * h} {v}")
        for y, x in itertools.product(range(v), range(case["mcus"] * h)):
            index = x // h * len(members) + first + y * h + x % h
            values = case["values"][index * 64:(index + 1) * 64]
            result.append(f"block {component} {x} {y} " + " ".join(map(str, values)))
        first += h * v
    return result


def expected(case):
    try:
        return "ok " + reference(case).hex()
    except Rejected as error:
        return "error " + str(error)


def check_output(stdout, stderr, inputs, names):
    census = re.fullmatch(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?', stderr)
    if census is None or int(census[1]) <= 0 or census[1] != census[2]:
        raise ValueError('invalid MCU entropy heap census')
    wanted = [name + ' ' + expected(case) for name, case in zip(names, inputs)]
    if len(names) != len(inputs) or stdout.splitlines() != wanted:
        raise ValueError('MCU entropy bytes or errors differ')
    return {'allocs': int(census[1]), 'frees': int(census[2]), 'live_bytes': 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("compiler", type=Path)
    parser.add_argument("oracle", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--reference-library", type=Path, required=True)
    parser.add_argument("--wasmtime", type=Path)
    parser.add_argument("--max-cases", type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error("--max-cases must be positive")
    repo = Path(__file__).resolve().parents[2]
    paths = [Path(__file__), Path(__file__).with_name("qualify_jpeg_entropy_writer.py"), Path(__file__).with_name("jpeg_mcu_entropy.fern"),
             Path(__file__).with_name("jpeg_mcu_entropy_test.fern"), Path(__file__).with_name("qualify_jpeg_mcu_entropy_test.py"),
             Path(__file__).with_name("reference_jpeg_scan.c"), args.binary.resolve(), args.compiler.resolve(), args.oracle.resolve(),
             args.reference_library.resolve(), Path(sys.executable).resolve(), repo / "fern-image/scripts/test-jpeg-mcu-entropy.sh", repo / ".github/workflows/fern-image.yml"]
    paths += [p for folder in ["compiler", "internal/stdlib", "fern-image/packages"] for p in (repo / folder).rglob('*') if p.is_file() and p.suffix in ['.fern', '.md']]
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(p): sha(p.read_bytes()) for p in paths}
    inputs = list(itertools.islice(cases(), args.max_cases))
    runner = [str(args.wasmtime.resolve()), 'run', '--dir=.'] if args.wasmtime else []
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix='mcu-entropy-', dir=args.output.parent.resolve()))
    record = {'complete': False, 'cases': len(inputs), 'max_cases': args.max_cases, 'files_sha256': frozen, 'samples': [], 'batches': [],
              'scope': 'Finite baseline MCU entropy, independent exact bits and libjpeg coefficients. No image API, performance or physical bounds claim.'}
    try:
        for first in range(0, len(inputs), 8):
            batch = inputs[first:first + 8]
            names = [f'case-{first + i}.raw' for i in range(len(batch))]
            for name, case in zip(names, batch):
                (temporary / name).write_bytes(encoded(case))
            record['active_batch'] = first
            result = subprocess.run([*runner, str(args.binary.resolve()), *names], cwd=temporary, capture_output=True, text=True, timeout=20)
            (temporary / 'stdout.txt').write_text(result.stdout)
            (temporary / 'stderr.txt').write_text(result.stderr)
            result.check_returncode()
            census = check_output(result.stdout, result.stderr, batch, names)
            wanted = result.stdout.splitlines()
            for offset, (name, case, line) in enumerate(zip(names, batch, wanted)):
                record['active_case'] = first + offset
                if (temporary / name).read_bytes() != encoded(case):
                    raise ValueError('MCU entropy input changed')
                observation = line[len(name) + 1:]
                if observation.startswith('ok '):
                    path = temporary / 'frame.jpg'
                    path.write_bytes(frame(case, bytes.fromhex(observation[3:])))
                    oracle = subprocess.run([str(args.oracle.resolve()), str(path)], capture_output=True, text=True, timeout=5)
                    (temporary / 'oracle-stdout.txt').write_text(oracle.stdout)
                    (temporary / 'oracle-stderr.txt').write_text(oracle.stderr)
                    oracle.check_returncode()
                    if oracle.stderr or oracle.stdout.splitlines() != coefficient_lines(case):
                        raise ValueError('offline JPEG coefficients differ')
                record['samples'].append({'input_sha256': sha(encoded(case)), 'observation': observation})
                record.pop('active_case')
            record['batches'].append({'first': first, 'count': len(batch), **census})
            record.pop('active_batch')
            for name in names:
                (temporary / name).unlink()
        if frozen != {str(p): sha(p.read_bytes()) for p in paths}:
            raise ValueError('MCU entropy source or tool changed')
        record['complete'] = True
        for p in temporary.iterdir():
            p.unlink()
        temporary.rmdir()
    except BaseException as error:
        record['failure'] = str(error)
        record['failure_directory'] = str(temporary)
        if isinstance(error, subprocess.TimeoutExpired):
            prefix = 'oracle-' if 'active_case' in record else ''
            for name, value in [('stdout.txt', error.stdout), ('stderr.txt', error.stderr)]:
                if value is not None:
                    (temporary / (prefix + name)).write_bytes(value.encode() if isinstance(value, str) else value)
        raise
    finally:
        args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps({'complete': True, 'cases': len(inputs)}))


if __name__ == '__main__':
    main()
