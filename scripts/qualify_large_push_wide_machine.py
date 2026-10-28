"""Qualify exact machine proof records and complete fresh-worker protocols."""
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

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('wide_machine_reference', ROOT / 'scripts/gen-large-push-wide-machine-reference.py')
GEN = importlib.util.module_from_spec(spec)
spec.loader.exec_module(GEN)


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def validate_case(case):
    if set(case) != {'id', 'name', 'source', 'expected', 'legacy_provenance', 'payload_sha256'} or type(case['id']) is not int or not 0 <= case['id'] < 1 << 31 or type(case['name']) is not str or not case['name'] or type(case['source']) is not str or type(case['expected']) is not bool:
        raise ValueError('reference case shape')
    legacy = case['legacy_provenance']
    if legacy is not None and (type(legacy) is not dict or set(legacy) != {'source_path', 'source_sha256', 'original_assertion', 'original_expected', 'original_context'} or type(legacy['original_expected']) is not bool or not all(type(legacy[k]) is str and legacy[k] for k in legacy if k != 'original_expected')):
        raise ValueError('legacy provenance shape')
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
    records = []
    if len(lines) != len(cases) * 2:
        raise ValueError('complete worker case count required')
    for at, case in enumerate(cases):
        validate_case(case)
        wanted = 'proof ' + str(case['id']) + ' ' + str(int(case['expected']))
        if lines[at * 2] != wanted:
            raise ValueError('exact proof mismatch: ' + case['name'])
        if lines[at * 2 + 1] != 'end ' + str(case['id']):
            raise ValueError('missing or incorrect case end')
        records.append(dict(id=case['id'], proved=case['expected']))
    return records, dict(allocs=allocs, frees=frees, live_bytes=live)


def inventory():
    paths = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', 'compiler', 'internal/stdlib', 'internal/testing/e2ecompiler', 'scripts', 'Makefile'], cwd=ROOT, text=True).splitlines()
    paths += ['compiler/ssalargepushwideisa.fern', 'compiler/drivers/ssalargepushwidemachine_worker.fern', 'compiler/drivers/ssalargepushwideisacases.fern', 'scripts/gen-large-push-wide-machine-reference.py', 'scripts/qualify_large_push_wide_decoder.py', 'scripts/test_large_push_wide_decoder.py', 'internal/testing/e2ecompiler/testdata/large_push_wide_machine_reference.json.gz']
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
    cmd = [str(compiler), '-target', options.target, '-sanitize', '-o', str(binary), str(ROOT / 'compiler/drivers/ssalargepushwidemachine_worker.fern')]
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
