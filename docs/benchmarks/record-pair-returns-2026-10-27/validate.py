from pathlib import Path
import subprocess, json, os, hashlib, sys

p = Path(__file__).parent
root = p.parent
kind = sys.argv[1] if len(sys.argv) > 1 else 'stage2'
assert kind in ['stage2', 'stage3']
record_file = p / ('validation.json' if kind == 'stage2' else 'validation-stage3.json')
records = json.loads(record_file.read_text()) if record_file.exists() else []
versions = [('before', root / 'pair-phi-candidate/post-merge/stage4'), ('after', p / 'stage2')] if kind == 'stage2' else [('after', p / 'stage3')]
for version, compiler in versions:
    for subject in ['record-pair', 'guards', 'heap-alias']:
        for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
            for mode, flags, sanitize in [('default', [], False), ('optimized', ['-O'], False), ('sanitized', ['-O'], True)]:
                name = '-'.join(([kind] if kind == 'stage3' else []) + [version, subject, target, mode])
                compiler_hash = hashlib.sha256(compiler.read_bytes()).hexdigest()
                fixture_hash = hashlib.sha256((p / (subject + '.fern')).read_bytes()).hexdigest()
                saved = next((r for r in records if [r['version'], r['subject'], r['target'], r['mode']] == [version, subject, target, mode] and r['compiler_sha256'] == compiler_hash and r['fixture_sha256'] == fixture_hash), None)
                if saved is not None:
                    expected = '425\n' if version == 'before' and subject == 'record-pair' else '0\n' if version == 'after' and subject == 'heap-alias' else '100\n'
                    assert saved['stdout'] == expected
                    assert (saved['exit'] != 0 if version == 'before' and subject != 'guards' else saved['exit'] == 0)
                    assert 'live_bytes=0' in saved['stderr']
                    continue
                binary = p / name
                env = dict(os.environ, FERN_LEAKCHECK='1', FERN_STRICT_IR='1')
                if sanitize:
                    env['FERN_SANITIZE'] = '1'
                source = p / (subject + '.fern')
                cmd = [str(compiler)] + flags + ['-target', target, '-o', str(binary), str(source), str(root / 'fixed-source/internal/stdlib')]
                with (p / (name + '-build.log')).open('wb') as f:
                    r = subprocess.run(cmd, env=env, stdout=f, stderr=subprocess.STDOUT)
                assert r.returncode == 0, ('build', name, r.returncode)
                run = [str(binary)] if target == 'x86-64-linux' else ['qemu-aarch64', '-cpu', 'max', str(binary)] if target == 'arm64-linux' else ['wasmtime', 'run', str(binary)]
                r = subprocess.run(run, env=env, capture_output=True, text=True)
                record = {'version': version, 'subject': subject, 'target': target, 'mode': mode, 'compile_command': cmd, 'run_command': run, 'compiler_sha256': hashlib.sha256(compiler.read_bytes()).hexdigest(), 'fixture_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'exit': r.returncode, 'stdout': r.stdout, 'stderr': r.stderr}
                records.append(record)
                record_file.write_text(json.dumps(records, indent=2) + '\n')
                print(name, r.returncode, repr(r.stdout), repr(r.stderr), flush=True)
                expected = '425\n' if version == 'before' and subject == 'record-pair' else '0\n' if version == 'after' and subject == 'heap-alias' else '100\n'
                assert r.stdout == expected, (name, 'allocations', r.stdout, expected)
                assert (r.returncode != 0 if version == 'before' and subject != 'guards' else r.returncode == 0), (name, r.returncode)
                assert 'live_bytes=0' in r.stderr, (name, 'leak', r.stderr)
