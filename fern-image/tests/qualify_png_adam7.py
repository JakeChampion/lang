"""Check bounded Adam7 input against independent equations and offline Pillow."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import time
import zlib

import PIL
from fixtures.generate_png_adam7 import MODES, malformed, vector


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def cases():
    sizes = [(1, 1), (1, 31), (31, 1), (2, 3), (3, 2), (7, 9), (8, 8), (9, 17), (17, 9), (31, 29), (129, 5), (257, 7)]
    for variant, (width, height) in enumerate(sizes):
        for depth, color in MODES:
            value = vector(depth, color, width, height, variant)
            yield dict(name=f'{width}-{height}-{depth}-{color}', data=value['data'],
                       pixels=value['expected'], shape=(width, height, value['channels']))
    for index, (data, error) in enumerate(malformed()):
        yield dict(name=f'invalid-{index}', data=data, error=error)


def check_worker(stdout, stderr, selected):
    lines = stdout.splitlines()
    if len(lines) != len(selected):
        raise ValueError('worker result count differs')
    census = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if not census or not all(int(a) > 0 and a == b and c == '0' for a, b, c in census):
        raise ValueError('worker heap report is absent or unbalanced')
    for index, (line, case) in enumerate(zip(lines, selected, strict=True)):
        parts = line.split(' ', 5)
        if 'error' in case:
            if parts != [str(index), 'error', case['error']]:
                raise ValueError('worker error category differs')
        else:
            if len(parts) != 6 or parts[:2] != [str(index), 'ok']:
                raise ValueError('worker image result differs')
            if tuple(map(int, parts[2:5])) != case['shape'] or bytes.fromhex(parts[5]) != case['pixels']:
                raise ValueError('worker pixels or dimensions differ')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fern', type=Path, required=True)
    parser.add_argument('--target', choices=['arm64-darwin', 'arm64-linux', 'x86-64-linux', 'wasm32-wasi'], required=True)
    parser.add_argument('--runner', action='append', default=[])
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--max-cases', type=int)
    args = parser.parse_args()
    if args.max_cases is not None and args.max_cases <= 0:
        parser.error('max-cases must be positive')
    repo = Path(__file__).resolve().parents[2]
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    compiler = args.fern.resolve()
    paths = [*sorted((repo / 'fern-image/packages/codecs/png').glob('*.fern')),
             repo / 'fern-image/packages/pixels/pixels.fern', Path(__file__).resolve(),
             repo / 'fern-image/tests/fixtures/generate_png_adam7.py', repo / 'fern-image/tests/png_adam7_corpus.fern']
    frozen = {str(p.relative_to(repo)): digest(p) for p in paths}
    values = list(cases())
    if args.max_cases is not None:
        values = values[:args.max_cases]
    record = dict(complete=False, target=args.target, compiler_sha256=digest(compiler),
                  source_inventory=frozen, reference='PNG third edition sections 7.2, 8.1 and 9; independent tile extraction and Pillow pixel checks',
                  pillow=PIL.__version__, zlib=zlib.ZLIB_RUNTIME_VERSION, host=platform.platform(),
                  cases=len(values), commands=[], observations=[])
    env = dict(os.environ, FERN_LEAKCHECK='1')

    def save():
        (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')

    def run(label, command, cwd):
        started = time.monotonic()
        result = subprocess.run(list(map(str, command)), cwd=cwd, env=env, capture_output=True, timeout=120)
        stdout = out / (label + '.stdout')
        stderr = out / (label + '.stderr')
        stdout.write_bytes(result.stdout)
        stderr.write_bytes(result.stderr)
        record['commands'].append(dict(label=label, command=list(map(str, command)), exit_code=result.returncode,
                                       seconds=time.monotonic() - started, stdout_sha256=digest(stdout), stderr_sha256=digest(stderr)))
        save()
        result.check_returncode()
        return result

    binary = out / ('worker.wasm' if args.target == 'wasm32-wasi' else 'worker')
    run('build', [compiler, '-target', args.target, '-sanitize', '-o', binary, repo / 'fern-image/tests/png_adam7_corpus.fern', repo / 'internal/stdlib'], repo)
    record['binary_sha256'] = digest(binary)
    for first in range(0, len(values), 16):
        batch = values[first:first + 16]
        names = []
        for index, case in enumerate(batch, first):
            path = out / f'input-{index}.png'
            path.write_bytes(case['data'])
            names.append(path.name)
            observation = dict(name=case['name'], input_sha256=digest(path))
            if 'error' in case:
                observation['expected_error'] = case['error']
            else:
                observation.update(shape=case['shape'], pixels_sha256=hashlib.sha256(case['pixels']).hexdigest())
            record['observations'].append(observation)
        result = run('batch-' + str(first), [*args.runner, binary, *names], out)
        check_worker(result.stdout.decode(), result.stderr.decode(), batch)
    if frozen != {str(p.relative_to(repo)): digest(p) for p in paths} or digest(compiler) != record['compiler_sha256']:
        raise ValueError('source or compiler changed during qualification')
    record['complete'] = True
    save()
    print(json.dumps(dict(complete=True, cases=len(values), target=args.target)))


if __name__ == '__main__':
    main()
