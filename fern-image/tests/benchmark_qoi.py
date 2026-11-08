"""Verify a finite native QOI benchmark pipeline against the offline reference."""

import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import re
import shutil
import subprocess
import time

from qualify_qoi import Descriptor, REFERENCE_REVISION, REFERENCE_SHA256


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def observation(stdout, stderr, value, iterations, wall, sanitized):
    fields = stdout.split()
    if len(fields) != 4:
        raise ValueError('incorrect timing record shape')
    elapsed, checksum, size, warmup = map(int, fields)
    contribution = len(value) + value[0] + value[-1]
    if size != len(value) or checksum != iterations * contribution or warmup != 2 * contribution:
        raise ValueError('codec loop checksum or output size differs')
    rss = re.findall(r'^\s*(\d+)\s+maximum resident set size\s*$', stderr, re.M)
    if len(rss) != 1 or int(rss[0]) <= 0 or not 0 <= elapsed < wall:
        raise ValueError('invalid native timer or RSS observation')
    census = re.findall(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
    if sanitized and (len(census) != 1 or int(census[0][0]) <= 0 or census[0][0] != census[0][1] or census[0][2] != '0'):
        raise ValueError('sanitized heap does not balance')
    return dict(output_bytes=size, inner_ns=elapsed, process_wall_ns=wall,
                peak_rss_time_l_raw=int(rss[0]), timer_resolution_limited=elapsed == 0,
                checksum=checksum, warmup_checksum=warmup)


def fixtures(side):
    rng = random.Random(20261107)
    for channels in (3, 4):
        for color in (0, 1):
            for pattern in ("black", "ramp", "noise"):
                size = side * side * channels
                if pattern == "black":
                    raw = bytes(size)
                elif pattern == "ramp":
                    raw = bytes((i * 17 + i // channels) % 256 for i in range(size))
                else:
                    raw = bytes(rng.randrange(256) for _ in range(size))
                yield f"{channels}-{color}-{pattern}", Descriptor(side, side, channels, color), raw


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fern", type=Path, required=True)
    parser.add_argument("--reference-header", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--side", type=int, default=16)
    parser.add_argument("--iterations", type=int, default=3)
    parser.add_argument("--cc", default="cc")
    options = parser.parse_args()
    if platform.system() != "Darwin" or platform.machine() != "arm64":
        parser.error("this pipeline currently requires native Apple Silicon macOS")
    if not 1 <= options.side <= 1024 or not 1 <= options.iterations <= 1000000:
        parser.error("side must be 1..1024 and iterations must be 1..1000000")
    if digest(options.reference_header) != REFERENCE_SHA256:
        raise ValueError("reference header hash differs")
    root = Path(__file__).resolve().parents[2]
    out = options.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    cc = shutil.which(options.cc)
    if cc is None:
        raise ValueError('C compiler is unavailable')
    paths = {"compiler": options.fern.resolve(), "reference_header": options.reference_header.resolve(),
             "reference_compiler": Path(cc).resolve(),
             "collector": Path(__file__).resolve(), "fixture_tools": Path(__file__).with_name("qualify_qoi.py"),
             "collector_tests": Path(__file__).with_name("benchmark_qoi_test.py"),
             "timer": Path('/usr/bin/time')}
    for folder in ['compiler', 'internal/stdlib', 'internal/syntax/diag/explanations', 'fern-image/packages']:
        for path in sorted((root / folder).rglob('*')):
            if path.is_file():
                paths[str(path.relative_to(root))] = path
    for relative in ['fern-image/benchmarks/qoi_worker.fern', 'fern-image/benchmarks/qoi_reference_worker.c']:
        paths[relative] = root / relative
    frozen = {name: digest(path) for name, path in paths.items()}
    record = dict(status='running', scope='Pipeline smoke observations only. Host load is uncontrolled. No performance ranking, physical memory bound or fastest implementation claim.',
                  host=platform.platform(), machine=platform.machine(), load_before=os.getloadavg(),
                  reference_revision=REFERENCE_REVISION, reference_header_sha256=REFERENCE_SHA256,
                  side=options.side, iterations=options.iterations, warmup_iterations=2,
                  timer='ARM virtual counter with integer nanosecond conversion in both release workers; excludes file I/O, startup, preflight and warmup. Zero intervals remain explicitly resolution-limited.',
                  memory='Darwin time -l maximum resident set size, as reported without rescaling; complete process including startup and preflight',
                  sha256=frozen, commands=[], samples=[])

    def save():
        (out / 'proof.json').write_text(json.dumps(record, indent=2) + '\n')

    def run(label, command):
        started = time.monotonic_ns()
        result = subprocess.run([str(value) for value in command], cwd=root, capture_output=True,
                                env=dict(os.environ, LC_ALL='C'), timeout=60)
        wall = time.monotonic_ns() - started
        (out / (label + '.stdout')).write_bytes(result.stdout)
        (out / (label + '.stderr')).write_bytes(result.stderr)
        record['commands'].append(dict(label=label, command=[str(value) for value in command], exit_code=result.returncode, process_wall_ns=wall))
        save()
        result.check_returncode()
        return result, wall

    save()
    try:
        record['compiler_version'] = subprocess.check_output([str(options.fern), '-version'], text=True).strip()
        record['cc_version'] = subprocess.check_output([options.cc, '--version'], text=True).splitlines()[0]
        record['cpu'] = subprocess.check_output(['/usr/sbin/sysctl', '-n', 'machdep.cpu.brand_string'], text=True).strip()
        fern = out / 'fern-qoi'
        sanitized = out / 'fern-qoi-sanitized'
        reference = out / 'reference-qoi'
        source = root / 'fern-image/benchmarks/qoi_worker.fern'
        for label, target, flags in [('build-fern', fern, []), ('build-sanitized', sanitized, ['-sanitize'])]:
            run(label, [options.fern.resolve(), '-target', 'arm64-darwin', *flags, '-o', target, source, root / 'internal/stdlib'])
        run('build-reference', [options.cc, '-O3', '-DNDEBUG', '-Wall', '-Wextra', '-Werror',
                                '-I', options.reference_header.resolve().parent,
                                root / 'fern-image/benchmarks/qoi_reference_worker.c', '-o', reference])
        wrapper = out / 'reference.c'
        wrapper.write_text('#define QOI_IMPLEMENTATION\n#define QOI_NO_STDIO\n#include ' + json.dumps(str(options.reference_header.resolve())) + '\nvoid reference_free(void *p) { free(p); }\n')
        library = out / 'reference.so'
        run('build-fixture-tools', [options.cc, '-shared', '-fPIC', '-O2', wrapper, '-o', library])
        codec = ctypes.CDLL(str(library))
        codec.qoi_encode.argtypes = [ctypes.c_void_p, ctypes.POINTER(Descriptor), ctypes.POINTER(ctypes.c_int)]
        codec.qoi_encode.restype = ctypes.c_void_p
        codec.qoi_decode.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.POINTER(Descriptor), ctypes.c_int]
        codec.qoi_decode.restype = ctypes.c_void_p
        codec.reference_free.argtypes = [ctypes.c_void_p]
        record['binaries'] = {path.name: digest(path) for path in [fern, sanitized, reference, library]}
        for fixture_index, (name, description, raw) in enumerate(fixtures(options.side)):
            raw_path, input_path = out / (name + '.raw'), out / (name + '.qoi')
            raw_path.write_bytes(raw)
            length = ctypes.c_int()
            pointer = codec.qoi_encode(ctypes.c_char_p(raw), ctypes.byref(description), ctypes.byref(length))
            if not pointer or length.value <= 22:
                raise ValueError('reference fixture encode failed')
            encoded = ctypes.string_at(pointer, length.value)
            codec.reference_free(pointer)
            input_path.write_bytes(encoded)
            for mode_index, mode in enumerate(['encode', 'decode']):
                implementations = [('fern', fern), ('reference', reference)]
                if (fixture_index + mode_index) % 2:
                    implementations.reverse()
                implementations.append(('sanitized-correctness', sanitized))
                for implementation, binary in implementations:
                    label = f'{name}-{mode}-{implementation}'
                    prefix = out / label
                    result, wall = run(label, ['/usr/bin/time', '-l', binary, mode, input_path, raw_path, prefix, options.iterations])
                    saved = prefix.with_name(prefix.name + '.qoi').read_bytes()
                    pixels = prefix.with_name(prefix.name + '.raw').read_bytes()
                    actual = Descriptor()
                    pointer = codec.qoi_decode(ctypes.c_char_p(saved), len(saved), ctypes.byref(actual), 0)
                    if not pointer:
                        raise ValueError('reference rejects worker output')
                    actual_raw = ctypes.string_at(pointer, actual.width * actual.height * actual.channels)
                    codec.reference_free(pointer)
                    if pixels != raw or actual_raw != raw or (actual.width, actual.height, actual.channels, actual.colorspace) != (description.width, description.height, description.channels, description.colorspace):
                        raise ValueError('worker pixels or declarations differ')
                    value = saved if mode == 'encode' else raw
                    observed = observation(result.stdout.decode(), result.stderr.decode(), value,
                                           options.iterations, wall, implementation == 'sanitized-correctness')
                    record['samples'].append(dict(name=name, mode=mode, implementation=implementation,
                                                  input_sha256=digest(input_path), raw_sha256=digest(raw_path),
                                                  encoded_sha256=hashlib.sha256(saved).hexdigest(), **observed))
                    save()
        if frozen != {name: digest(path) for name, path in paths.items()}:
            raise ValueError('source or tool changed during observations')
        record['load_after'] = os.getloadavg()
        record['status'] = 'smoke_passed'
    except BaseException:
        record['status'] = 'failed'
        raise
    finally:
        save()
    print(json.dumps(dict(status=record['status'], samples=len(record['samples']), side=options.side, iterations=options.iterations)))


if __name__ == '__main__':
    main()
