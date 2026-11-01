import argparse
import hashlib
import json
from pathlib import Path
import re
import shlex
import subprocess


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def invoke(command):
    result = subprocess.run(command, capture_output=True, text=True, timeout=110)
    record = dict(command=command, exit_code=result.returncode, stdout=result.stdout, stderr=result.stderr)
    if result.returncode:
        raise RuntimeError(json.dumps(record))
    return record


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--compiler', type=Path, required=True)
    parser.add_argument('--target', required=True)
    parser.add_argument('--runner', default='')
    parser.add_argument('--work', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[3]
    compiler, work = args.compiler.resolve(), args.work.resolve()
    work.mkdir(parents=True, exist_ok=True)
    sources = [('fern-image/experiments/essentials.fern', 1), ('fern-image/tests/pixels_test.fern', 7),
               ('fern-image/tests/color_test.fern', 6)]
    bindings = [name for name, _ in sources] + ['fern-image/packages/color/color.fern',
                'fern-image/packages/pixels/pixels.fern', 'fern-image/docs/color.md']
    record = dict(source_head=invoke(['git', '-C', str(root), 'rev-parse', 'HEAD'])['stdout'].strip(),
                  compiler_sha256=digest(compiler), target=args.target,
                  collector_sha256=digest(__file__), source_sha256={name: digest(root / name) for name in bindings}, cases=[])
    for name, count in sources:
        binary = work / (Path(name).stem + ('.wasm' if args.target == 'wasm32-wasi' else ''))
        check = invoke([str(compiler), '-check', str(root / name), str(root / 'internal/stdlib')])
        build = invoke([str(compiler), '-target', args.target, '-sanitize', '-o', str(binary), str(root / name), str(root / 'internal/stdlib')])
        run = invoke(shlex.split(args.runner) + [str(binary)])
        for field, value in [('tests', count), ('pass', count), ('fail', 0)]:
            assert re.search(r'^# ' + field + ' ' + str(value) + '$', run['stdout'], re.M)
        census = re.search(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)', run['stderr'])
        assert census and int(census[1]) > 0 and census[1] == census[2] and census[3] == '0'
        record['cases'].append(dict(source=name, tests=count, check=check, build=build, run=run,
                                    binary_sha256=digest(binary), allocs=int(census[1]), frees=int(census[2]), live_bytes=0))
        (work / 'qualification.json').write_text(json.dumps(record, indent=2) + '\n')
        print(json.dumps(dict(target=args.target, source=name, tests=count, allocs=int(census[1]))), flush=True)
    assert record['source_sha256'] == {name: digest(root / name) for name in bindings}
    assert record['compiler_sha256'] == digest(compiler) and record['collector_sha256'] == digest(__file__)


if __name__ == '__main__':
    main()
