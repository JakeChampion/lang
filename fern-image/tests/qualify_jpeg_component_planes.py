"""Check component-plane output with a separate entropy reader and DCT math."""

import argparse
import copy
import decimal
import functools
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

import qualify_jpeg_component_frames as frames
from qualify_jpeg_dct import ABSOLUTE_TOLERANCE, ideal
from generate_jpeg_idct import basis


def sha(data):
    return hashlib.sha256(data).hexdigest()


def make(pattern, width, height, kind=0, edge=0, restart=0):
    case = frames.make(pattern, width, height, restart, 1)
    case['dc'] = [frames.scan.table(False)] * 2
    case['ac'] = [frames.scan.table(True)] * 2
    case['planes'] = []
    case['max_blocks'] = 1000000
    _, _, _, hmax, vmax = frames.geometry(case)
    rng = random.Random(20261107 + kind)
    for i, (_, h, v, _, _, _) in enumerate(case['components']):
        w, height_c = (width * h + hmax - 1) // hmax, (height * v + vmax - 1) // vmax
        stride = w + (i + kind) % 4
        samples = []
        for y, x in itertools.product(range(height_c), range(stride)):
            if kind == 0:
                value = (17 + i * 79) % 256
            elif kind == 1:
                value = (x * 31 + y * 47 + i * 73) % 256
            elif kind == 2:
                value = rng.randrange(256)
            else:
                value = 255 if (x, y) == (w // 2, height_c // 2) else 128
            samples.append(value if x < w else 231)
        case['planes'].append({'width': w, 'height': height_c, 'stride': stride, 'edge': edge,
                               'fill': (67 + i * 43) % 256, 'samples': samples})
    case.pop('values')
    return case


def cases():
    patterns = [((4, 4),), ((1, 1),) * 3, ((2, 2), (1, 1), (1, 1)), ((2, 1), (1, 1), (1, 1)),
                ((1, 2), (1, 1), (1, 1)), ((3, 1), (2, 2)), ((4, 1), (1, 2), (2, 2)),
                ((2, 2), (1, 1), (1, 1), (1, 1))]
    for index, (pattern, size, kind, edge) in enumerate(itertools.product(patterns, [(1, 1), (9, 17), (17, 9)], range(4), range(2))):
        yield make(pattern, *size, kind, edge, [0, 1, 7][index % 3])
    base = make(patterns[2], 17, 17, 0, 0, 1)
    exact = copy.deepcopy(base)
    exact['values'] = []
    for component, _, _, _, _, _, _ in addresses(base):
        value = base['planes'][component]['samples'][0]
        q = base['quantization'][base['components'][component][3]][0]
        dc = int((decimal.Decimal((value - 128) * 8) / q).to_integral_value(rounding=decimal.ROUND_HALF_UP))
        exact['values'].extend([dc] + [0] * 63)
    size = len(frames.reference(exact))
    for limit in [0, 1, len(frames.prefix(base)) - 1, size - 1, size, size + 1]:
        yield {**copy.deepcopy(base), 'limit': limit}
    for field, values in [('width', [-1, 0, 65536]), ('height', [0, 65536]), ('max_blocks', [-1, 0, 23]), ('restart', [-1, 65536]), ('limit', [-1])]:
        for value in values:
            yield {**copy.deepcopy(base), field: value}
    for count in [0, 1, 2, 4]:
        case = copy.deepcopy(base)
        case['planes'] = (case['planes'] * 2)[:count]
        yield case
    for field, values in [('width', [0, 8, 10]), ('height', [0, 8, 10]), ('stride', [-1, 8, 2147483647]), ('samples', [[], [0] * 80])]:
        for value in values:
            case = copy.deepcopy(base)
            case['planes'][1][field] = value
            yield case
    for field, value in [(0, -1), (0, 256), (1, 0), (2, 5), (3, -1), (3, 4), (4, 2), (5, 2)]:
        case = copy.deepcopy(base)
        item = list(case['components'][0])
        item[field] = value
        case['components'][0] = tuple(item)
        yield case
    case = copy.deepcopy(base)
    case['components'][1] = (case['components'][0][0], *case['components'][1][1:])
    yield case


def encoded(case):
    result = struct.pack('>7i', case['width'], case['height'], case['restart'], case['limit'], case['max_blocks'], len(case['components']), len(case['planes']))
    result += b''.join(struct.pack('>6i', *item) for item in case['components'])
    for plane in case['planes']:
        result += struct.pack('>6i', plane['width'], plane['height'], plane['stride'], plane['edge'], plane['fill'], len(plane['samples']))
        result += bytes(plane['samples'])
    return result


def preflight(case):
    components = case['components']
    if not 1 <= len(components) <= 4 or len(case['planes']) != len(components):
        return 'Shape'
    if not 1 <= case['width'] <= 65535 or not 1 <= case['height'] <= 65535:
        return 'Order.Dimensions'
    if any(not 1 <= c[1] <= 4 or not 1 <= c[2] <= 4 for c in components) or len(components) > 1 and sum(c[1] * c[2] for c in components) > 10:
        return 'Order.Sampling'
    columns, rows, units, hmax, vmax = frames.geometry(case)
    if case['max_blocks'] < 0 or columns * rows * units > case['max_blocks']:
        return 'Order.Limit'
    for plane, (_, h, v, _, _, _) in zip(case['planes'], components, strict=True):
        w, height = (case['width'] * h + hmax - 1) // hmax, (case['height'] * v + vmax - 1) // vmax
        if (plane['width'], plane['height']) != (w, height):
            return 'Shape'
        if plane['stride'] < w:
            return 'Block.Layout'
        if (height - 1) * plane['stride'] + w > len(plane['samples']):
            return 'Block.Storage'
    try:
        frames.admit(case)
    except frames.scan.Rejected as error:
        return 'Frame.' + str(error)
    if len(frames.prefix(case)) + 2 > case['limit']:
        return 'Frame.Limit'
    return None


def addresses(case):
    columns, rows, _, hmax, vmax = frames.geometry(case)
    for mcu in range(columns * rows):
        for c, (_, h, v, _, _, _) in enumerate(case['components']):
            w, height = (case['width'] * h + hmax - 1) // hmax, (case['height'] * v + vmax - 1) // vmax
            if len(case['components']) == 1:
                h = v = 1
            for y, x in itertools.product(range(v), range(h)):
                yield c, mcu, mcu % columns * h + x, mcu // columns * v + y, w, height, h * v


def sample_block(plane, bx, by):
    data = []
    for y, x in itertools.product(range(by * 8, by * 8 + 8), range(bx * 8, bx * 8 + 8)):
        if x >= plane['width'] or y >= plane['height']:
            if plane['edge']:
                data.append(plane['fill'])
                continue
            x, y = min(x, plane['width'] - 1), min(y, plane['height'] - 1)
        data.append(plane['samples'][y * plane['stride'] + x])
    return bytes(data)


class Bits:
    def __init__(self, data):
        self.data, self.at, self.value, self.left = data, 0, 0, 0

    def read(self, count):
        value = 0
        for _ in range(count):
            if not self.left:
                if self.at >= len(self.data):
                    raise ValueError('truncated entropy bits')
                self.value = self.data[self.at]
                self.at += 1
                if self.value == 255:
                    if self.at >= len(self.data) or self.data[self.at] != 0:
                        raise ValueError('missing entropy byte stuffing')
                    self.at += 1
                self.left = 8
            self.left -= 1
            value = value * 2 + ((self.value >> self.left) & 1)
        return value

    def align(self):
        count = self.left
        if self.read(count) != (1 << count) - 1:
            raise ValueError('invalid entropy alignment')

    def restart(self, marker):
        self.align()
        if self.data[self.at:self.at + 2] != bytes([255, 208 + marker]):
            raise ValueError('invalid restart marker')
        self.at += 2


def read_symbol(bits, table):
    code = ''
    for _ in range(16):
        code += str(bits.read(1))
        if code in table:
            return table[code]
    raise ValueError('invalid Huffman code')


def amplitude(bits, size):
    value = bits.read(size)
    return value if not size or value >= 1 << (size - 1) else value - ((1 << size) - 1)


def decode(case, output):
    prefix = frames.prefix(case)
    if not output.startswith(prefix) or not output.endswith(b'\xff\xd9') or len(output) > case['limit']:
        raise ValueError('component frame markers or length differ')
    bits = Bits(output[len(prefix):-2])
    tables = [[{code: symbol for symbol, code in frames.scan.canonical(t, bool(kind)).items()} for t in case[field]] for kind, field in enumerate(['dc', 'ac'])]
    previous = [0] * len(case['components'])
    values, last_mcu, marker = [], -1, 0
    for component, mcu, _, _, _, _, _ in addresses(case):
        if mcu != last_mcu and mcu and case['restart'] and mcu % case['restart'] == 0:
            bits.restart(marker)
            marker = (marker + 1) % 8
            previous = [0] * len(previous)
        last_mcu = mcu
        _, _, _, _, dc, ac = case['components'][component]
        size = read_symbol(bits, tables[0][dc])
        if size > 11:
            raise ValueError('invalid DC category')
        block = [previous[component] + amplitude(bits, size)] + [0] * 63
        previous[component] = block[0]
        at = 1
        while at < 64:
            symbol = read_symbol(bits, tables[1][ac])
            run, size = symbol >> 4, symbol & 15
            if not size:
                if run == 0:
                    break
                if run != 15 or at + 16 > 64:
                    raise ValueError('invalid zero run')
                at += 16
                continue
            at += run
            if size > 10 or at >= 64:
                raise ValueError('invalid AC category or position')
            block[frames.scan.permutation()[at]] = amplitude(bits, size)
            at += 1
        values.extend(block)
    bits.align()
    if bits.at != len(bits.data):
        raise ValueError('extra entropy bytes')
    if frames.reference({**case, 'values': values}) != output:
        raise ValueError('noncanonical complete frame bytes')
    return values


@functools.lru_cache(maxsize=10000)
def mathematical(data):
    if len(set(data)) == 1:
        return tuple([decimal.Decimal((data[0] - 128) * 8)] + [decimal.Decimal(0)] * 63)
    return tuple(ideal(data, basis_matrix))


def check_math(case, values):
    ambiguous = 0
    for index, (component, _, bx, by, _, _, _) in enumerate(addresses(case)):
        data = sample_block(case['planes'][component], bx, by)
        exact = len(set(data)) == 1
        tolerance = decimal.Decimal(0) if exact else ABSOLUTE_TOLERANCE
        ideal_values = mathematical(data)
        q = case['quantization'][case['components'][component][3]]
        natural_q = [0] * 64
        for at, natural in enumerate(frames.scan.permutation()):
            natural_q[natural] = q[at]
        for actual, wanted, step in zip(values[index * 64:(index + 1) * 64], ideal_values, natural_q, strict=True):
            low, high = [int(((wanted + delta) / step).to_integral_value(rounding=decimal.ROUND_HALF_UP)) for delta in [-tolerance, tolerance]]
            if not low <= actual <= high:
                raise ValueError('coefficient differs from the established mathematical bound')
            ambiguous += low != high
    return ambiguous


def check_worker(stdout, stderr, count):
    census = re.fullmatch(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?', stderr)
    lines = stdout.splitlines()
    if not census or int(census[1]) <= 0 or census[1] != census[2] or len(lines) != count:
        raise ValueError('invalid worker output or heap census')
    return lines, {'allocs': int(census[1]), 'frees': int(census[2]), 'live_bytes': 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['binary', 'compiler', 'oracle', 'output']:
        parser.add_argument(name, type=Path)
    parser.add_argument('--reference-library', type=Path, required=True)
    parser.add_argument('--wasmtime', type=Path)
    parser.add_argument('--max-cases', type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error('--max-cases must be positive')
    global basis_matrix
    decimal.getcontext().prec = 90
    basis_matrix = basis()
    repo = Path(__file__).resolve().parents[2]
    paths = [args.binary.resolve(), args.compiler.resolve(), args.oracle.resolve(), args.reference_library.resolve(), Path(sys.executable).resolve()]
    paths += [p for folder in ['compiler', 'internal/stdlib', 'fern-image/packages', 'fern-image/tests', 'fern-image/scripts'] for p in (repo / folder).rglob('*') if p.is_file() and '__pycache__' not in p.parts]
    paths.append(repo / '.github/workflows/fern-image.yml')
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(p): sha(p.read_bytes()) for p in paths}
    inputs = list(itertools.islice(cases(), args.max_cases))
    runner = [str(args.wasmtime.resolve()), 'run', '--dir=.'] if args.wasmtime else []
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix='component-planes-', dir=args.output.parent.resolve()))
    record = {'complete': False, 'cases': len(inputs), 'files_sha256': frozen, 'samples': [], 'batches': [],
              'coefficient_tolerance': str(ABSOLUTE_TOLERANCE), 'scope': 'Finite component-plane assembly, complete entropy decode, prior DCT mathematical bound and offline physical coefficient grids. No speed or physical memory claim.'}
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
            lines, census = check_worker(result.stdout, result.stderr, len(batch))
            for offset, (name, case, line) in enumerate(zip(names, batch, lines, strict=True)):
                record['active_case'] = first + offset
                if not line.startswith(name + ' ') or (temporary / name).read_bytes() != encoded(case):
                    raise ValueError('invalid worker path or changed input')
                observation = line[len(name) + 1:]
                wanted_error = preflight(case)
                sample = {'input_sha256': sha(encoded(case)), 'observation': observation}
                if observation.startswith('ok ') and wanted_error is None:
                    output = bytes.fromhex(observation[3:])
                    values = decode(case, output)
                    sample['ambiguous_coefficients'] = check_math(case, values)
                    path = temporary / 'frame.jpg'
                    path.write_bytes(output)
                    oracle = subprocess.run([str(args.oracle.resolve()), str(path)], capture_output=True, text=True, timeout=5)
                    (temporary / 'oracle-stdout.txt').write_text(oracle.stdout)
                    (temporary / 'oracle-stderr.txt').write_text(oracle.stderr)
                    sample['oracle'] = frames.check_oracle(oracle, {**case, 'values': values})
                else:
                    if wanted_error is None and case['limit'] < 1000000:
                        # Limit vectors use constant planes and exact mathematical coefficients.
                        values = []
                        for c, _, bx, by, _, _, _ in addresses(case):
                            data = sample_block(case['planes'][c], bx, by)
                            if len(set(data)) != 1:
                                raise ValueError('limit reference requires exact constant blocks')
                            q = case['quantization'][case['components'][c][3]][0]
                            dc = int((decimal.Decimal((data[0] - 128) * 8) / q).to_integral_value(rounding=decimal.ROUND_HALF_UP))
                            values.extend([dc] + [0] * 63)
                        try:
                            frames.reference({**case, 'values': values})
                        except frames.scan.Rejected as error:
                            wanted_error = 'Frame.' + str(error)
                    if observation != 'error ' + str(wanted_error):
                        raise ValueError('component-plane error differs: ' + observation + ' expected ' + str(wanted_error))
                record['samples'].append(sample)
                record.pop('active_case')
            record['batches'].append({'first': first, 'count': len(batch), **census})
            record.pop('active_batch')
            for name in names:
                (temporary / name).unlink()
        if frozen != {str(p): sha(p.read_bytes()) for p in paths}:
            raise ValueError('component-plane source or tool changed')
        record['complete'] = True
        for path in temporary.iterdir():
            path.unlink()
        temporary.rmdir()
    except BaseException as error:
        record['failure'], record['failure_directory'] = str(error), str(temporary)
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
