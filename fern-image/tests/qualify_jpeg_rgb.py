"""Qualify finite JFIF RGB assembly against independent rational and C observations."""
import argparse
import fractions
import gzip
import hashlib
import json
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile

from jpeg_rgb_reference import assemble

MAX_COMPONENT_ERROR = 1
MAX_COLOR_ERROR = 3
MAX_CODEC_ERROR = 4


def sha(data):
    return hashlib.sha256(data).hexdigest()


def errors(actual, expected):
    if len(actual) != len(expected):
        raise ValueError('sample storage length mismatch')
    values = [abs(a - b) for a, b in zip(actual, expected, strict=True)]
    return max(values, default=0), sum(value != 0 for value in values)


def check_output(stdout, stderr, cases, names, directory):
    if len(cases) != len(names):
        raise ValueError('worker case count mismatch')
    census = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if len(census) != 1:
        raise ValueError('expected exactly one heap census')
    allocations, frees, live = map(int, census[0])
    if allocations == 0 or allocations != frees or live != 0:
        raise ValueError('unbalanced or empty worker heap census')
    lines = iter(stdout.splitlines())
    samples = []
    allowed = set(names) | {'stdout.txt', 'stderr.txt'}
    for case, name in zip(cases, names, strict=True):
        if case['outcome'] != 'accepted':
            for index in range(2):
                if next(lines, None) != f"reject {name} {index} {case['outcome']}":
                    raise ValueError('unsupported profile status mismatch')
            if next(lines, None) != 'end ' + name:
                raise ValueError('missing rejected observation terminator')
            samples.append({'name': case['name'], 'input_sha256': case['input_sha256'], 'outcome': case['outcome']})
            continue
        reference = case['raw_reference']
        width, height = reference['width'], reference['height']
        total = sum(p['width'] * p['height'] for p in reference['planes'])
        input_bytes = len(bytes.fromhex(case['encoded_hex']))
        output_bytes, row_bytes = width * height * 3, width * 6
        planned = input_bytes + total + output_bytes + row_bytes
        rgb = []
        for index in range(2):
            expected = f'rgb {name} {index} {width} {height} {width * 3} {output_bytes} {input_bytes} {total} {row_bytes} {planned}'
            if next(lines, None) != expected:
                raise ValueError('RGB metadata or logical plan mismatch')
            filename = name + f'.rgb-{index}.raw'
            allowed.add(filename)
            path = directory / filename
            if not path.is_file():
                raise ValueError('RGB output missing')
            actual = path.read_bytes()
            if len(actual) != output_bytes:
                raise ValueError('RGB storage length mismatch')
            rgb.append(actual)
        if next(lines, None) != 'planes ' + name + ' 3':
            raise ValueError('raw component count mismatch')
        actual_planes, observations = [], []
        for index, plane in enumerate(reference['planes']):
            component, horizontal, vertical = plane['id'], plane['horizontal'], plane['vertical']
            w, h = plane['width'], plane['height']
            if next(lines, None) != f'plane {index} {component} {horizontal} {vertical} {w} {h} {w} {w * h}':
                raise ValueError('component metadata mismatch')
            filename = name + f'.plane-{index}.raw'
            allowed.add(filename)
            path = directory / filename
            if not path.is_file():
                raise ValueError('component output missing')
            actual = path.read_bytes()
            maximum, different = errors(actual, bytes(plane['pixels']))
            if len(actual) != w * h or maximum > MAX_COMPONENT_ERROR:
                raise ValueError('component reference mismatch')
            actual_planes.append({**plane, 'pixels': list(actual)})
            observations.append({'id': component, 'horizontal': horizontal, 'vertical': vertical, 'width': w, 'height': h,
                                 'sha256': sha(actual), 'reference_sha256': sha(bytes(plane['pixels'])),
                                 'max_sample_error': maximum, 'different_samples': different})
        filters = []
        for index, actual in enumerate(rgb):
            exact = assemble({**reference, 'planes': actual_planes}, bool(index))
            if actual != exact:
                raise ValueError('exact rational RGB composition mismatch')
            key = 'bilinear_hex' if index else 'nearest_hex'
            expected = bytes.fromhex(case[key])
            maximum, different = errors(actual, expected)
            if maximum > MAX_COLOR_ERROR:
                raise ValueError('independent raw-plane RGB reference mismatch')
            observation = {'filter': 'bilinear' if index else 'nearest', 'sha256': sha(actual),
                           'exact_composition_sha256': sha(exact), 'reference_sha256': sha(expected),
                           'max_sample_error': maximum, 'different_samples': different}
            if index == 0 and 'codec_reference' in case:
                expected_codec = bytes(case['codec_reference']['pixels'])
                maximum_codec, different_codec = errors(actual, expected_codec)
                if maximum_codec > MAX_CODEC_ERROR:
                    raise ValueError('supplemental C RGB reference mismatch')
                observation.update(codec_sha256=sha(expected_codec), codec_max_sample_error=maximum_codec,
                                   codec_different_samples=different_codec)
            filters.append(observation)
        if next(lines, None) != 'end ' + name:
            raise ValueError('missing RGB observation terminator')
        samples.append({'name': case['name'], 'input_sha256': case['input_sha256'], 'outcome': 'accepted',
                        'width': width, 'height': height, 'stride': width * 3, 'bytes': output_bytes,
                        'plane_bytes': total, 'row_bytes': row_bytes, 'planned_bytes': planned,
                        'planes': observations, 'filters': filters})
    if next(lines, None) is not None:
        raise ValueError('extra worker output')
    if any(path.name not in allowed for path in directory.iterdir()):
        raise ValueError('unexpected worker file')
    return samples, {'allocs': allocations, 'frees': frees, 'live_bytes': live}


def source_paths(root, options):
    paths = {'compiler': options.compiler.resolve(), 'worker_binary': options.binary.resolve(),
             'Python': Path(sys.executable).resolve(), 'fraction_source': Path(fractions.__file__).resolve(),
             'reference_bundle': options.reference.resolve(), '.github/workflows/fern-image.yml': root.parent / '.github/workflows/fern-image.yml'}
    for directory in [root / 'packages', root.parent / 'compiler', root.parent / 'internal/stdlib']:
        for path in sorted(directory.rglob('*')):
            if path.is_file() and (path.suffix == '.fern' or path.name.endswith('.fern.md')):
                paths[str(path.relative_to(root.parent))] = path
    for relative in ['tests/jpeg_rgb.fern', 'tests/jpeg_rgb_test.fern', 'tests/fixtures/jpeg_rgb.fern',
                     'tests/generate_jpeg_rgb.py', 'tests/jpeg_rgb_reference.py', 'tests/reference_jpeg_rgb.c',
                     'tests/reference_jpeg_planes.c', 'tests/fixtures/jpeg-planes-reference.json.gz',
                     'tests/qualify_jpeg_rgb.py', 'tests/qualify_jpeg_rgb_test.py', 'scripts/test-jpeg-rgb.sh']:
        paths['fern-image/' + relative] = root / relative
    if options.raw_oracle:
        paths['raw_oracle'] = options.raw_oracle.resolve()
    if options.rgb_oracle:
        paths['rgb_oracle'] = options.rgb_oracle.resolve()
    return paths


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary', type=Path)
    parser.add_argument('compiler', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--reference', type=Path, default=Path(__file__).parent / 'fixtures/jpeg-rgb-reference.json.gz')
    parser.add_argument('--raw-oracle', type=Path)
    parser.add_argument('--rgb-oracle', type=Path)
    parser.add_argument('--allow-dirty', action='store_true')
    runner = parser.add_mutually_exclusive_group()
    runner.add_argument('--wasmtime', type=Path)
    runner.add_argument('--qemu', type=Path)
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    dirty = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=root, text=True).strip())
    if dirty and not options.allow_dirty:
        raise ValueError('qualification requires a clean source commit')
    paths = source_paths(root, options)
    command, runtime = [], 'native x86-64'
    if options.wasmtime:
        paths['runner'] = options.wasmtime.resolve()
        command = [str(paths['runner']), 'run', '--dir=.']
        runtime = subprocess.check_output([str(paths['runner']), '--version'], text=True).strip()
    elif options.qemu:
        paths['runner'] = options.qemu.resolve()
        command = [str(paths['runner'])]
        runtime = subprocess.check_output([str(paths['runner']), '--version'], text=True).splitlines()[0]
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    document = json.loads(gzip.decompress(options.reference.read_bytes()))
    if document['schema'] != 1 or not document['cases']:
        raise ValueError('invalid reference bundle')
    reference = document['reference']
    for key, source in [('generator', 'fern-image/tests/generate_jpeg_rgb.py'), ('math_source', 'fern-image/tests/jpeg_rgb_reference.py'),
                        ('oracle_source', 'fern-image/tests/reference_jpeg_rgb.c'), ('raw_oracle_source', 'fern-image/tests/reference_jpeg_planes.c'),
                        ('source_bundle', 'fern-image/tests/fixtures/jpeg-planes-reference.json.gz')]:
        if reference['sha256'][key] != hashes[source]:
            raise ValueError('reference source changed: ' + key)
    for key in ['raw_oracle', 'rgb_oracle']:
        if key in paths and reference['sha256'][key] != hashes[key]:
            raise ValueError('offline oracle changed')
    for case in document['cases']:
        if sha(bytes.fromhex(case['encoded_hex'])) != case['input_sha256']:
            raise ValueError('reference input changed')
        if case['outcome'] not in ('accepted', 'unsupported', 'profile'):
            raise ValueError('invalid case outcome')
        if case['outcome'] == 'accepted':
            for key, linear in [('nearest_hex', False), ('bilinear_hex', True)]:
                if assemble(case['raw_reference'], linear).hex() != case[key]:
                    raise ValueError('independent rational reference changed')
    options.output.parent.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix='jpeg-rgb-', dir=options.output.parent.resolve()))
    record = {'status': 'running', 'source_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip(),
              'worktree_dirty': dirty, 'compiler_version': subprocess.check_output([str(paths['compiler']), '-version'], text=True).strip(),
              'runtime': runtime, 'host': platform.platform(), 'host_machine': platform.machine(), 'Python': platform.python_version(),
              'reference': reference, 'sha256': hashes, 'oracle_rerun': {'raw': bool(options.raw_oracle), 'RGB': bool(options.rgb_oracle)},
              'samples': [], 'batches': [], 'limits': {'cases_per_process': 8, 'process_timeout_seconds': 20, 'concurrency': 1,
              'max_component_error': MAX_COMPONENT_ERROR, 'max_color_error': MAX_COLOR_ERROR, 'max_codec_error': MAX_CODEC_ERROR,
              'exact_composition_required': True}, 'scope': reference['scope']}
    try:
        cases = document['cases']
        for first in range(0, len(cases), 8):
            batch = cases[first:first + 8]
            names = [f'case-{i}.jpg' for i in range(len(batch))]
            record['active_batch'] = {'first': first, 'cases': [case['name'] for case in batch]}
            for case, name in zip(batch, names, strict=True):
                path = directory / name
                path.write_bytes(bytes.fromhex(case['encoded_hex']))
                for option, data_key, hash_key in [(options.raw_oracle, 'raw_reference', 'raw_stdout_sha256'), (options.rgb_oracle, 'codec_reference', 'codec_stdout_sha256')]:
                    if option and data_key in case:
                        result = subprocess.run([str(option.resolve()), str(path)], capture_output=True, timeout=20)
                        if result.returncode or result.stderr or sha(result.stdout) != case[hash_key] or json.loads(result.stdout) != case[data_key]:
                            (directory / 'oracle-stdout.txt').write_bytes(result.stdout)
                            (directory / 'oracle-stderr.txt').write_bytes(result.stderr)
                            raise ValueError('offline observation changed')
            result = subprocess.run([*command, str(paths['worker_binary']), *names], cwd=directory, capture_output=True, text=True, timeout=20)
            (directory / 'stdout.txt').write_text(result.stdout)
            (directory / 'stderr.txt').write_text(result.stderr)
            if result.returncode:
                raise ValueError(f'worker exit {result.returncode}')
            samples, census = check_output(result.stdout, result.stderr, batch, names, directory)
            record['samples'].extend(samples)
            record['batches'].append({'first': first, 'count': len(batch), 'exit_code': result.returncode, **census})
            record.pop('active_batch')
            for path in directory.iterdir():
                path.unlink()
            print(f'passed {first + len(batch)}/{len(cases)}', flush=True)
        shutil.rmtree(directory)
        record['sources_unchanged'] = hashes == {name: sha(path.read_bytes()) for name, path in paths.items()}
        if not record['sources_unchanged']:
            raise ValueError('source or tool changed during qualification')
        record['status'] = 'complete'
    except BaseException as error:
        record['status'] = 'failed'
        if isinstance(error, subprocess.TimeoutExpired):
            for name, value in [('stdout.txt', error.stdout), ('stderr.txt', error.stderr)]:
                if value is not None:
                    (directory / name).write_bytes(value.encode() if isinstance(value, str) else value)
        record['failure_directory'] = str(directory)
        record['failure'] = str(error)
        record['sources_unchanged'] = hashes == {name: sha(path.read_bytes()) for name, path in paths.items()}
        raise
    finally:
        options.output.write_text(json.dumps(record, indent=2) + '\n')


if __name__ == '__main__':
    main()
