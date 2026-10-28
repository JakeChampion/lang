"""Qualify exact decoder records and complete fresh-worker protocols."""
import argparse
import gzip
import hashlib
import importlib.util
import json
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('wide_decoder_reference', ROOT / 'scripts/gen-large-push-wide-decoder-reference.py')
GEN = importlib.util.module_from_spec(spec)
spec.loader.exec_module(GEN)
FIELDS = GEN.FIELDS


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def integer(text, bits=32):
    if not re.fullmatch(r'0|-?[1-9][0-9]*', text, re.ASCII):
        raise ValueError('noncanonical worker integer')
    n = int(text)
    if not -(1 << (bits - 1)) <= n < 1 << (bits - 1):
        raise ValueError('worker integer range')
    return n


def unhex(text):
    if text == '-':
        return ''
    if not re.fullmatch(r'(?:[0-9a-f]{2})+', text, re.ASCII):
        raise ValueError('worker text encoding')
    return bytes.fromhex(text).decode('utf8')


def validate_case(case):
    if set(case) != {'id', 'name', 'source', 'expected', 'payload_sha256'} or type(case['id']) is not int or not 0 <= case['id'] < 1 << 31 or type(case['name']) is not str or not case['name'] or type(case['source']) is not str:
        raise ValueError('reference case shape')
    expected = case['expected']
    if set(expected) != {'ok', 'ops'} or type(expected['ok']) is not bool or type(expected['ops']) is not list or not expected['ok'] and expected['ops']:
        raise ValueError('reference status shape')
    for op in expected['ops']:
        if set(op) != set(FIELDS):
            raise ValueError('reference operation shape')
        for field in FIELDS:
            v = op[field]
            if field in ['condition', 'symbol']:
                if type(v) is not str:
                    raise ValueError('reference text type')
            else:
                bits = 64 if field == 'immediate' else 32
                if type(v) is not int or not -(1 << (bits - 1)) <= v < 1 << (bits - 1):
                    raise ValueError('reference integer range')
    payload = GEN.packet(case)
    if hashlib.sha256(payload).hexdigest() != case['payload_sha256']:
        raise ValueError('reference packet identity')
    return payload


def check_output(stdout, stderr, cases, returncode=0):
    if returncode != 0:
        raise ValueError('worker exit ' + str(returncode))
    m = re.fullmatch(r'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\s*', stderr)
    if not m or 'fern-sanitizer:' in stderr:
        raise ValueError('one exact worker heap census required')
    allocs, frees, live = map(int, m.groups())
    if allocs == 0 or allocs != frees or live != 0:
        raise ValueError('nonzero balanced zero-live worker required')
    if not cases or len({c['id'] for c in cases}) != len(cases):
        raise ValueError('worker case identity')
    lines = stdout.splitlines()
    at = 0
    records = []
    for case in cases:
        validate_case(case)
        expected = case['expected']
        if at >= len(lines):
            raise ValueError('missing worker case')
        prefix = 'wid ' + str(case['id']) + ' '
        if expected['ok']:
            if lines[at] != prefix + 'ok ' + str(len(expected['ops'])):
                raise ValueError('success or operation count mismatch: ' + case['name'])
            at += 1
            ops = []
            for index, want in enumerate(expected['ops']):
                if at >= len(lines):
                    raise ValueError('missing worker operation')
                parts = lines[at].split(' ')
                if len(parts) != 16 or parts[:3] != ['op', str(case['id']), str(index)]:
                    raise ValueError('worker operation ordinal or shape')
                got = {field: integer(parts[3 + i], 64 if field == 'immediate' else 32) for i, field in enumerate(FIELDS[:10])}
                got.update(condition=unhex(parts[13]), target=integer(parts[14]), symbol=unhex(parts[15]))
                if got != want:
                    raise ValueError('exact operation mismatch: ' + case['name'] + '/' + str(index))
                ops.append(got)
                at += 1
            records.append(dict(id=case['id'], ok=True, ops=ops))
        else:
            fields = lines[at].split(' ')
            if len(fields) != 5 or fields[:4] != ['wid', str(case['id']), 'error', '0'] or not unhex(fields[4]):
                raise ValueError('error result, partial operations, or error text mismatch')
            records.append(dict(id=case['id'], ok=False, ops=[]))
            at += 1
        if at >= len(lines) or lines[at] != 'end ' + str(case['id']):
            raise ValueError('missing or incorrect case end')
        at += 1
    if at != len(lines):
        raise ValueError('extra worker output')
    return records, dict(allocs=allocs, frees=frees, live_bytes=live)


def inventory():
    paths = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', 'compiler', 'internal/stdlib', 'internal/testing/e2ecompiler', 'scripts', 'Makefile'], cwd=ROOT, text=True).splitlines()
    paths += ['compiler/ssalargepushwideisa.fern', 'compiler/drivers/ssalargepushwideisa_worker.fern', 'compiler/drivers/ssalargepushwideisacases.fern', 'scripts/gen-large-push-wide-decoder-reference.py', 'scripts/qualify_large_push_wide_decoder.py', 'scripts/test_large_push_wide_decoder.py', 'internal/testing/e2ecompiler/testdata/large_push_wide_decoder_reference.json.gz']
    return {p: sha(ROOT / p) for p in sorted(set(paths)) if (ROOT / p).is_file()}


def run(options):
    output = Path(options.output).resolve()
    work = Path(options.work).resolve()
    if output.exists() or work.exists() or output.with_suffix(output.suffix + '.checkpoint').exists():
        raise ValueError('refuse existing qualification output or work directory')
    source = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT))
    if source != options.source_commit or not re.fullmatch('[0-9a-f]{40}', options.source_commit) or dirty and not options.allow_dirty:
        raise ValueError('source identity or dirty tree')
    reference_path = Path(options.reference).resolve()
    reference = json.loads(gzip.decompress(reference_path.read_bytes()))
    if reference['schema'] != 1 or [c['id'] for c in reference['cases']] != list(range(len(reference['cases']))) or len({c['name'] for c in reference['cases']}) != len(reference['cases']):
        raise ValueError('complete reference catalog identity')
    for case in reference['cases']:
        validate_case(case)
    work.mkdir(parents=True)
    before = inventory()
    compiler = Path(options.compiler).resolve()
    compiler_hash = sha(compiler)
    binary = work / ('worker.wasm' if options.target == 'wasm32-wasi' else 'worker.elf')
    cmd = [str(compiler), '-target', options.target, '-sanitize', '-o', str(binary), str(ROOT / 'compiler/drivers/ssalargepushwideisa_worker.fern')]
    env = dict(__import__('os').environ, FERN_STRICT_IR='1', FERN_SANITIZE='1', FERN_LEAKCHECK='1')
    record = dict(source=source, feature_uncommitted=dirty, target=options.target, compiler_sha256=compiler_hash, source_hashes=before, reference_sha256=sha(reference_path), case_count=len(reference['cases']), compile_command=cmd, workers=[], records=[], qualified=False)
    checkpoint = output.with_suffix(output.suffix + '.checkpoint')
    try:
        with (work / 'compile.stdout.log').open('wb') as out, (work / 'compile.stderr.log').open('wb') as err:
            result = subprocess.run(cmd, cwd=ROOT, env=env, stdout=out, stderr=err)
        record['compile_exit'] = result.returncode
        checkpoint.write_text(json.dumps(record, indent=2) + '\n')
        if result.returncode:
            raise ValueError('worker compile exit ' + str(result.returncode))
        binary_hash = sha(binary)
        record['worker_binary_sha256_before_execution'] = binary_hash
        runner = [str(binary)]
        host = platform.machine().lower()
        qemu = 'qemu-aarch64' if options.target == 'arm64-linux' and host not in ['aarch64', 'arm64'] else 'qemu-x86_64' if options.target == 'x86-64-linux' and host not in ['x86_64', 'amd64'] else None
        if options.target == 'wasm32-wasi':
            runner = [shutil.which('wasmtime') or 'wasmtime', 'run', str(binary)]
        elif qemu:
            path = shutil.which(qemu)
            if path is None:
                raise ValueError('required target runtime missing')
            runner = [path, str(binary)]
        record['runtime_command'] = runner
        for start in range(0, len(reference['cases']), 8):
            batch = reference['cases'][start:start + 8]
            packet = b''.join(validate_case(c) for c in batch)
            stem = work / f'worker-{start:05d}'
            stem.with_suffix('.input.bin').write_bytes(packet)
            result = subprocess.run(runner, cwd=work, env=env, input=packet, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            stem.with_suffix('.stdout.log').write_bytes(result.stdout)
            stem.with_suffix('.stderr.log').write_bytes(result.stderr)
            item = dict(first_case=start, case_ids=[c['id'] for c in batch], exit=result.returncode, input_sha256=hashlib.sha256(packet).hexdigest(), stdout_sha256=hashlib.sha256(result.stdout).hexdigest(), stderr_sha256=hashlib.sha256(result.stderr).hexdigest())
            record['workers'].append(item)
            checkpoint.write_text(json.dumps(record, indent=2) + '\n')
            rows, heap = check_output(result.stdout.decode('utf8'), result.stderr.decode('utf8'), batch, result.returncode)
            item.update(heap=heap, qualified=True)
            record['records'].extend(rows)
            if sha(binary) != binary_hash or sha(compiler) != compiler_hash:
                raise ValueError('binary identity changed')
        record.update(worker_binary_sha256_after_execution=sha(binary), sources_unchanged=before == inventory(), compiler_unchanged=sha(compiler) == compiler_hash)
        if len(record['records']) != len(reference['cases']) or not record['sources_unchanged'] or not record['compiler_unchanged']:
            raise ValueError('incomplete campaign or changed source')
        record['qualified'] = True
    except Exception as error:
        record['failure'] = str(error)
        record['sources_unchanged'] = before == inventory()
        output.write_text(json.dumps(record, indent=2) + '\n')
        raise
    output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(dict(target=options.target, cases=len(record['records']), workers=len(record['workers']), qualified=record['qualified'])))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--compiler', required=True)
    parser.add_argument('--target', required=True, choices=['x86-64-linux', 'arm64-linux', 'wasm32-wasi'])
    parser.add_argument('--reference', default=str(GEN.REFERENCE))
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--work', required=True)
    parser.add_argument('--allow-dirty', action='store_true')
    run(parser.parse_args())


if __name__ == '__main__':
    main()
