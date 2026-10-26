#!/usr/bin/env python3
"""Compare emitted text with fixed benchmark and standard-library inputs."""
import argparse
import hashlib
import json
import pathlib
import re
import subprocess
import tempfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--repo', type=pathlib.Path, required=True)
    p.add_argument('--baseline', type=pathlib.Path, required=True)
    p.add_argument('--baseline-repo', type=pathlib.Path, required=True)
    p.add_argument('--candidate', type=pathlib.Path, required=True)
    p.add_argument('--wasm-tools', type=pathlib.Path, required=True)
    p.add_argument('--output', type=pathlib.Path, required=True)
    args = p.parse_args()
    root = args.repo.resolve()
    base_root = args.baseline_repo.resolve()
    tools = {key: getattr(args, key).resolve() for key in ('baseline', 'candidate', 'wasm_tools')}
    inputs = sorted(set(root.glob('compiler/*.fern')) | set(root.glob('bench/*.fern')) |
                    set(root.glob('internal/stdlib/**/*.fern')) | set(root.glob('internal/stdlib/**/*.fern.md')))
    base_inputs = sorted(set(base_root.glob('compiler/*.fern')) |
                         set(base_root.glob('internal/stdlib/**/*.fern')) |
                         set(base_root.glob('internal/stdlib/**/*.fern.md')))
    tracked = inputs + base_inputs + list(tools.values()) + [pathlib.Path(__file__).resolve(), root / 'bootstrap/stage0.lock']
    hashes = {str(x): digest(x) for x in tracked}
    record = {'source_commit': run(['git', '-C', str(root), 'rev-parse', 'HEAD']).strip(),
              'baseline_source_commit': run(['git', '-C', str(base_root), 'rev-parse', 'HEAD']).strip(),
              'stage0_lock_sha256': digest(root / 'bootstrap/stage0.lock'),
              'tools': {key: {'path': str(value), 'sha256': digest(value)} for key, value in tools.items()},
              'wasm_tools_version': run([str(tools['wasm_tools']), '--version']).strip(),
              'inputs_sha256': {str(x.relative_to(root)): hashes[str(x)] for x in inputs},
              'baseline_inputs_sha256': {str(x.relative_to(base_root)): hashes[str(x)] for x in base_inputs},
              'collector_sha256': digest(pathlib.Path(__file__).resolve()), 'samples': [],
              'limits': ['Counts cover indented output lines, not CPU time or retired instructions.',
                         'WAT counts and parsed binary lengths are size observations.',
                         'These observations do not establish compatibility, throughput parity, or production safety.']}
    tid = re.compile(r'^\s*\(global \$__tid\$.*\n?', re.MULTILINE)
    with tempfile.TemporaryDirectory(prefix='wasm-typeids-') as tmp:
        work = pathlib.Path(tmp)
        for src in sorted(root.glob('bench/*.fern')):
            for target in ('x86-64-linux', 'arm64-linux', 'wasm32-wasi'):
                row = {'benchmark': src.stem, 'target': target}
                text = {}
                for mode in ('baseline', 'candidate'):
                    out = work / (mode + '.out')
                    run([str(tools[mode]), '-target', target, '-emit', 'asm', '-o', str(out),
                         str(src), str(root / 'internal/stdlib')])
                    data = out.read_bytes()
                    assert data, (src, target, mode)
                    text[mode] = data.decode()
                    sample = {'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data),
                              'indented_lines': sum(bool(x and x[0].isspace()) for x in text[mode].splitlines())}
                    if target == 'wasm32-wasi':
                        binary = work / (mode + '.wasm')
                        run([str(tools['wasm_tools']), 'parse', str(out), '-o', str(binary)])
                        run([str(tools['wasm_tools']), 'validate', str(binary)])
                        sample['binary_bytes'] = binary.stat().st_size
                        sample['binary_sha256'] = digest(binary)
                        sample['type_id_definitions'] = len(tid.findall(text[mode]))
                    row[mode] = sample
                if target == 'wasm32-wasi':
                    assert tid.sub('', text['baseline']) == tid.sub('', text['candidate']), (src, target, 'body changed')
                    if '$__tid$' in tid.sub('', text['baseline']):
                        assert text['baseline'] == text['candidate'], (src, 'needed section changed')
                    else:
                        assert not tid.search(text['candidate']), (src, 'unused globals remain')
                else:
                    assert text['baseline'] == text['candidate'], (src, target, 'native output changed')
                record['samples'].append(row)
            print(src.stem, flush=True)
    assert hashes == {str(x): digest(x) for x in tracked}, 'inputs or tools changed'
    record['sources_unchanged'] = True
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(record, indent=2) + '\n')


if __name__ == '__main__':
    main()
