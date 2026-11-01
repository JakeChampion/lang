import argparse
import hashlib
import json
from pathlib import Path
import re
import shlex
import subprocess
import time
import zlib


def digest(path):
    with Path(path).open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def invoke(command):
    started = time.monotonic()
    result = subprocess.run(command, capture_output=True, text=True, timeout=110)
    row = dict(command=command, exit_code=result.returncode,
               seconds=time.monotonic() - started,
               stdout=result.stdout, stderr=result.stderr)
    if result.returncode:
        raise RuntimeError(json.dumps(row))
    return row


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--compiler', type=Path, required=True)
    parser.add_argument('--target', required=True)
    parser.add_argument('--runner', default='')
    parser.add_argument('--work', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[3]
    compiler = args.compiler.resolve()
    work = args.work.resolve()
    work.mkdir(parents=True, exist_ok=True)
    sources = ['fern-image/experiments/zlib.fern',
               'fern-image/tests/png_compression_test.fern',
               'fern-image/tests/png_test.fern']
    bindings = sources + ['fern-image/packages/codecs/png/compression.fern',
                          'fern-image/packages/codecs/png/png.fern',
                          'fern-image/tests/fixtures/png.fern',
                          'internal/stdlib/std/deflate.fern.md']
    report = dict(source_head=invoke(['git', '-C', str(root), 'rev-parse', 'HEAD'])['stdout'].strip(),
                  compiler=str(compiler), compiler_sha256=digest(compiler),
                  target=args.target, runner=shlex.split(args.runner),
                  source_sha256={name: digest(root / name) for name in bindings},
                  reference=dict(zlib_version=zlib.ZLIB_VERSION,
                                 zlib_runtime_version=zlib.ZLIB_RUNTIME_VERSION),
                  cases=[])
    text = (root / sources[0]).read_text()
    for name, block_type, copies in [('stored', 0, 1), ('fixed', 1, 128), ('dynamic', 2, 128)]:
        literal = re.search(r'function ' + name + r'\(\): u8\[\] \{\s*return \[([^]]+)\]', text)[1]
        stream = bytes(map(int, re.findall(r'(\d+) as u8', literal)))
        assert (stream[2] >> 1) & 3 == block_type
        reference = zlib.decompressobj()
        plain = reference.decompress(stream) + reference.flush()
        assert reference.eof and not reference.unused_data and not reference.unconsumed_tail
        assert plain == b'Fern PNG compression foundation. ' * copies
        report['reference'][name] = dict(bytes=len(stream), sha256=hashlib.sha256(stream).hexdigest(),
                                        decoded_bytes=len(plain), block_type=block_type)
    frame = bytes([120, 1, 1, 1, 0, 254, 255, 65, 0, 66, 0, 66])
    assert zlib.decompress(frame) == b'A'
    assert all((cmf * 256 + flg) % 31 == 0 for cmf, flg in [(104, 5), (119, 9), (120, 32)])
    report['reference']['strict_frame_sha256'] = hashlib.sha256(frame).hexdigest()
    for name, expected in zip(sources, [3, 4, 7]):
        output = work / (Path(name).stem + ('.wasm' if args.target == 'wasm32-wasi' else ''))
        row = dict(source=name)
        row['check'] = invoke([str(compiler), '-check', str(root / name), str(root / 'internal/stdlib')])
        row['compile'] = invoke([str(compiler), '-target', args.target, '-sanitize', '-o', str(output),
                                 str(root / name), str(root / 'internal/stdlib')])
        row['binary_sha256'] = digest(output)
        row['run'] = invoke(shlex.split(args.runner) + [str(output)])
        stdout, stderr = row['run']['stdout'], row['run']['stderr']
        assert re.search(r'^# tests ' + str(expected) + r'$', stdout, re.M)
        assert re.search(r'^# pass ' + str(expected) + r'$', stdout, re.M)
        assert re.search(r'^# fail 0$', stdout, re.M)
        census = re.search(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', stderr)
        assert census, stderr
        allocs, frees, live = map(int, census.groups())
        assert allocs > 0 and allocs == frees and live == 0
        row['tests'] = expected
        row['census'] = dict(allocs=allocs, frees=frees, live_bytes=live)
        report['cases'].append(row)
        (work / 'qualification.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(dict(source=name, target=args.target, tests=expected, **row['census'])), flush=True)
    assert digest(compiler) == report['compiler_sha256']
    assert all(digest(root / name) == sha for name, sha in report['source_sha256'].items())


if __name__ == '__main__':
    main()
