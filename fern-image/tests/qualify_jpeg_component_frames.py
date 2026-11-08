"""Check explicit component frames with independent markers and libjpeg grids."""

import argparse
import copy
import hashlib
import itertools
import json
from pathlib import Path
import re
import struct
import subprocess
import sys
import tempfile

import qualify_jpeg_mcu_entropy as scan


def sha(data):
    return hashlib.sha256(data).hexdigest()


def admit(case):
    if not (1 <= case['width'] <= 65535 and 1 <= case['height'] <= 65535):
        raise scan.Rejected('Dimensions')
    if not (1 <= len(case['quantization']) <= 4 and 1 <= len(case['dc']) <= 2 and 1 <= len(case['ac']) <= 2):
        raise scan.Rejected('Shape')
    for values in case['quantization']:
        if len(values) != 64:
            raise scan.Rejected('Shape')
        if 0 in values:
            raise scan.Rejected('Table')
    if any(len(t['counts']) != 16 or len(t['symbols']) > 256 for t in case['dc'] + case['ac']):
        raise scan.Rejected('Shape')
    components = case['components']
    if not 1 <= len(components) <= 4:
        raise scan.Rejected('Shape')
    seen = set()
    units = 0
    for identifier, h, v, q, dc, ac in components:
        if not 0 <= identifier <= 255:
            raise scan.Rejected('Shape')
        if not 0 <= q < len(case['quantization']) or not 0 <= dc < len(case['dc']) or not 0 <= ac < len(case['ac']):
            raise scan.Rejected('Table')
        if not (1 <= h <= 4 and 1 <= v <= 4) or identifier in seen:
            raise scan.Rejected('Shape')
        seen.add(identifier)
        units += h * v
    if len(components) > 1 and units > 10:
        raise scan.Rejected('Shape')
    if not 0 <= case['restart'] <= 65535 or case['limit'] < 0:
        raise scan.Rejected('Limit')


def geometry(case):
    components = case['components']
    hmax, vmax = max(c[1] for c in components), max(c[2] for c in components)
    span_x, span_y = (8, 8) if len(components) == 1 else (8 * hmax, 8 * vmax)
    columns = (case['width'] + span_x - 1) // span_x
    rows = (case['height'] + span_y - 1) // span_y
    units = 1 if len(components) == 1 else sum(c[1] * c[2] for c in components)
    return columns, rows, units, hmax, vmax


def prefix(case):
    output = b'\xff\xd8'
    for destination, values in enumerate(case['quantization']):
        output += scan.segment(219, bytes([destination]) + bytes(values))
    sof = struct.pack('>BHHB', 8, case['height'], case['width'], len(case['components']))
    sof += b''.join(bytes([identifier, h * 16 + v, q]) for identifier, h, v, q, _, _ in case['components'])
    output += scan.segment(192, sof)
    for kind, field in enumerate(['dc', 'ac']):
        for destination, table in enumerate(case[field]):
            output += scan.segment(196, bytes([kind * 16 + destination]) + bytes(table['counts']) + bytes(table['symbols']))
    if case['restart']:
        output += scan.segment(221, struct.pack('>H', case['restart']))
    sos = bytes([len(case['components'])])
    sos += b''.join(bytes([identifier, dc * 16 + ac]) for identifier, _, _, _, dc, ac in case['components'])
    return output + scan.segment(218, sos + bytes([0, 63, 0]))


def reference(case):
    admit(case)
    beginning = prefix(case)
    if len(beginning) + 2 > case['limit']:
        raise scan.Rejected('Limit')
    columns, rows, _, _, _ = geometry(case)
    config = {'mcus': columns * rows, 'components': [(h, v, dc, ac) for _, h, v, _, dc, ac in case['components']],
              'dc': case['dc'], 'ac': case['ac'], 'restart': case['restart'],
              'limit': case['limit'] - len(beginning) - 2, 'values': case['values']}
    try:
        entropy = scan.reference(config)
    except scan.Rejected as error:
        name = str(error)
        raise scan.Rejected(name if name in ['Limit', 'Cancelled'] else 'Entropy.' + name) from error
    return beginning + entropy + b'\xff\xd9'


def make(pattern, width, height, restart=0, variant=0):
    components = [((0, 255, 42, 128)[i], h, v, (3 - i) if variant else 0,
                   i % 2 if variant else 0, (i + 1) % 2 if variant else 0) for i, (h, v) in enumerate(pattern)]
    dc, ac = [scan.table(False, 4), scan.table(False, 8)], [scan.table(True), scan.table(True, 16)]
    dc[1]['symbols'].reverse()
    ac[1]['symbols'].reverse()
    case = {'width': width, 'height': height, 'restart': restart, 'limit': 10000000, 'components': components,
            'quantization': [[1 + (j * (i + 1) + i * 31) % 255 for j in range(64)] for i in range(4)],
            'dc': dc, 'ac': ac, 'values': []}
    columns, rows, units, _, _ = geometry(case)
    for index in range(columns * rows * units):
        block = [0] * 64
        block[0] = [-1024, -511, 0, 511, 1023][index % 5]
        if variant:
            for k in [1, 16, 32, 48, 63]:
                block[scan.permutation()[k]] = [-1023, -127, -1, 1, 127, 1023][(index + k) % 6]
        case['values'].extend(block)
    return case


def cases():
    patterns = [((4, 4),), ((1, 1),) * 3, ((2, 2), (1, 1), (1, 1)), ((2, 1), (1, 1), (1, 1)),
                ((1, 2), (1, 1), (1, 1)), ((3, 1), (2, 2)), ((4, 1), (1, 2), (2, 2)), ((2, 2), (1, 1), (1, 1), (1, 1))]
    for pattern, size, restart, variant in itertools.product(patterns, [(1, 1), (9, 9), (17, 25), (35, 17), (257, 1)], [0, 1, 2, 7, 65535], range(2)):
        yield make(pattern, *size, restart, variant)
    base = make(((1, 1),), 1, 1)
    for identifier in range(256):
        yield {**copy.deepcopy(base), 'components': [(identifier, 1, 1, identifier % 4, identifier % 2, identifier // 2 % 2)]}
    for width, height in [(65535, 1), (1, 65535)]:
        yield make(((1, 1),), width, height, 7)
    base = make(((2, 2), (1, 1), (1, 1)), 17, 17, 1, 1)
    size = len(reference(base))
    for limit in [0, 1, len(prefix(base)) - 1, len(prefix(base)) + 1, size - 1, size, size + 1]:
        yield {**copy.deepcopy(base), 'limit': limit}
    for field, values in [('width', [-2147483648, -1, 0, 65536, 2147483647]), ('height', [0, 65536]), ('restart', [-1, 65536]), ('limit', [-1])]:
        for value in values:
            yield {**copy.deepcopy(base), field: value}
    for length in [0, 1, 63, 64, len(base['values']) - 1, len(base['values']) + 1]:
        yield {**copy.deepcopy(base), 'values': [0] * length}
    for components in [[], [(0, 0, 1, 0, 0, 0)], [(0, 1, 5, 0, 0, 0)], [(256, 1, 1, 0, 0, 0)], [(-1, 1, 1, 0, 0, 0)],
                       [(0, 1, 1, 0, 0, 0)] * 2, [(i, 1, 1, 0, 0, 0) for i in range(5)], [(0, 4, 2, 0, 0, 0), (1, 3, 1, 0, 0, 0)],
                       [(0, 1, 1, -1, 0, 0)], [(0, 1, 1, 4, 0, 0)], [(0, 1, 1, 0, -1, 0)], [(0, 1, 1, 0, 0, 2)]]:
        yield {**copy.deepcopy(base), 'components': components}
    for quantization in [[], [[1] * 64] * 5, [[1] * 63], [[1] * 65], [[0] * 64]]:
        yield {**copy.deepcopy(base), 'quantization': quantization}
    for field in ['dc', 'ac']:
        for tables in [[], [scan.table(field == 'ac')] * 3, [{'counts': [0] * 16, 'symbols': []}], [{'counts': [0] * 16, 'symbols': [0] * 257}]]:
            yield {**copy.deepcopy(base), field: tables}
    for at, value in [(0, -1025), (0, 1024), (1, -1024), (63, 1024)]:
        case = copy.deepcopy(base)
        case['values'][at] = value
        yield case
    case = copy.deepcopy(base)
    case['dc'] = [{'counts': [1] + [0] * 15, 'symbols': [0]}]
    case['components'] = [(identifier, h, v, q, 0, ac) for identifier, h, v, q, _, ac in case['components']]
    yield case


def encoded(case):
    result = struct.pack('>8i', case['width'], case['height'], case['restart'], case['limit'], len(case['components']), len(case['quantization']), len(case['dc']), len(case['ac']))
    result += b''.join(struct.pack('>6i', *component) for component in case['components'])
    for values in case['quantization']:
        result += struct.pack('>H', len(values)) + bytes(values)
    for table in case['dc'] + case['ac']:
        result += bytes(table['counts']) + struct.pack('>H', len(table['symbols'])) + bytes(table['symbols'])
    return result + struct.pack('>' + 'i' * len(case['values']), *case['values'])


def coefficient_lines(case):
    columns, _, units, hmax, vmax = geometry(case)
    result = [f"frame {case['width']} {case['height']} {len(case['components'])}"]
    first = 0
    for component, (_, h, v, _, _, _) in enumerate(case['components']):
        width = (case['width'] * h + hmax - 1) // hmax
        height = (case['height'] * v + vmax - 1) // vmax
        if len(case['components']) == 1:
            h = v = 1
        nx, ny = (width + 7) // 8, (height + 7) // 8
        result.append(f'component {component} {nx} {ny}')
        for y, x in itertools.product(range(ny), range(nx)):
            mcu = y // v * columns + x // h
            index = mcu * units + first + y % v * h + x % h
            values = case['values'][index * 64:(index + 1) * 64]
            result.append(f'block {component} {x} {y} ' + ' '.join(map(str, values)))
        first += h * v
    return result


def expected(case):
    try:
        return 'ok ' + reference(case).hex()
    except scan.Rejected as error:
        return 'error ' + str(error)


def check_output(stdout, stderr, inputs, names):
    census = re.fullmatch(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?', stderr)
    if census is None or int(census[1]) <= 0 or census[1] != census[2]:
        raise ValueError('invalid component frame heap census')
    wanted = [name + ' ' + expected(case) for name, case in zip(names, inputs)]
    if len(names) != len(inputs) or stdout.splitlines() != wanted:
        raise ValueError('component frame bytes or errors differ')
    return {'allocs': int(census[1]), 'frees': int(census[2]), 'live_bytes': 0}


def check_oracle(result, case):
    if result.returncode == 0:
        if result.stderr or result.stdout.splitlines() != coefficient_lines(case):
            raise ValueError('offline component frame grids differ')
        return {'status': 'coefficients'}
    limit = re.fullmatch(r'Maximum supported image dimension is (\d+) pixels\n?', result.stderr)
    if result.returncode == 1 and not result.stdout and limit and 0 < int(limit[1]) < max(case['width'], case['height']):
        return {'status': 'dimension-limit', 'maximum': int(limit[1]), 'stderr': result.stderr}
    raise ValueError('offline component frame oracle failed: ' + result.stderr)


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
    repo = Path(__file__).resolve().parents[2]
    names = ['qualify_jpeg_component_frames.py', 'qualify_jpeg_mcu_entropy.py', 'qualify_jpeg_entropy_writer.py',
             'jpeg_component_frames.fern', 'jpeg_component_frames_test.fern', 'qualify_jpeg_component_frames_test.py', 'reference_jpeg_scan.c']
    paths = [Path(__file__).with_name(n) for n in names] + [args.binary.resolve(), args.compiler.resolve(), args.oracle.resolve(), args.reference_library.resolve(),
             Path(sys.executable).resolve(), repo / 'fern-image/scripts/test-jpeg-component-frames.sh', repo / '.github/workflows/fern-image.yml']
    paths += [p for folder in ['compiler', 'internal/stdlib', 'fern-image/packages'] for p in (repo / folder).rglob('*') if p.is_file() and p.suffix in ['.fern', '.md']]
    if args.wasmtime:
        paths.append(args.wasmtime.resolve())
    frozen = {str(p): sha(p.read_bytes()) for p in paths}
    inputs = list(itertools.islice(cases(), args.max_cases))
    runner = [str(args.wasmtime.resolve()), 'run', '--dir=.'] if args.wasmtime else []
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix='component-frames-', dir=args.output.parent.resolve()))
    record = {'complete': False, 'cases': len(inputs), 'max_cases': args.max_cases, 'files_sha256': frozen, 'samples': [], 'batches': [],
              'scope': 'Finite baseline component frames, independent complete bytes and offline libjpeg physical grids. No public color, performance or physical bounds claim.'}
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
            for offset, (name, case, line) in enumerate(zip(names, batch, result.stdout.splitlines())):
                record['active_case'] = first + offset
                if (temporary / name).read_bytes() != encoded(case):
                    raise ValueError('component frame input changed')
                observation = line[len(name) + 1:]
                sample = {'input_sha256': sha(encoded(case)), 'observation': observation}
                if observation.startswith('ok '):
                    path = temporary / 'frame.jpg'
                    path.write_bytes(bytes.fromhex(observation[3:]))
                    oracle = subprocess.run([str(args.oracle.resolve()), str(path)], capture_output=True, text=True, timeout=5)
                    (temporary / 'oracle-stdout.txt').write_text(oracle.stdout)
                    (temporary / 'oracle-stderr.txt').write_text(oracle.stderr)
                    sample['oracle'] = check_oracle(oracle, case)
                record['samples'].append(sample)
                record.pop('active_case')
            record['batches'].append({'first': first, 'count': len(batch), **census})
            record.pop('active_batch')
            for name in names:
                (temporary / name).unlink()
        if frozen != {str(p): sha(p.read_bytes()) for p in paths}:
            raise ValueError('component frame source or tool changed')
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
