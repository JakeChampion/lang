import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import statistics
import subprocess
import time

p = argparse.ArgumentParser()
p.add_argument('--before-root', type=Path, required=True)
p.add_argument('--after-root', type=Path, required=True)
p.add_argument('--build', action='store_true')
p.add_argument('--iterations', type=int, default=128)
a = p.parse_args()
out = Path('/tmp/fern-peg-text-bench')
out.mkdir(exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
workloads = {
    'ascii_capture': ('PCap("v", PLit("abcdefghijklmnop"))', 'abcdefghijklmnop', True),
    'unicode_capture': ('PCap("v", PLit("¢€𐐀"))', '¢€𐐀', True),
    'split_capture': ('PCap("v", PAny)', 'é', False),
    'uncaptured_byte': ('PAny', 'é', True),
}
if a.build:
    builds = {}
    for name, (pattern, value, valid) in workloads.items():
        src = out / (name + '.fern')
        src.write_text('''import "core/int";
import "std/peg";
import "std/i64";
function main(): i32 {
  let argv: string[] = args();
  let Some(count) = int.parse_int_radix(argv[1], 10) else { return 2; };
  let p: peg.Pattern = PATTERN;
  let input: string = INPUT;
  let sum: i64 = 0;
  let i: i32 = 0;
  while (i < count) {
    let r: peg.PegResult = peg.peg_match_pattern(p, input);
    sum = sum + r.pos as i64 + r.caps.get_or("v", "").len() as i64;
    if (r.ok) { sum = sum + 1000 as i64; }
    i = i + 1;
  }
  print(sum.to_string());
  return 0;
}
'''.replace('PATTERN', pattern).replace('INPUT', json.dumps(value, ensure_ascii=False)))
        for label, root in [('before', a.before_root), ('after', a.after_root)]:
            compiler = root / 'build/bootstrap/stage2'
            for checked in [False, True]:
                binary = out / (name + '-' + label + ('-census' if checked else ''))
                runenv = dict(env)
                if checked:
                    runenv.update(FERN_SANITIZE='1', FERN_LEAKCHECK='1')
                subprocess.run([str(compiler), '-target', 'arm64-darwin', '-o', str(binary), str(src), str(root/'internal/stdlib')], env=runenv, check=True, capture_output=True, timeout=60)
                builds[binary.name] = {'bytes': binary.stat().st_size, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
    print(json.dumps(builds, indent=2))
else:
    result = {'iterations': a.iterations, 'warmups': 2, 'samples': 7, 'workloads': {}}
    for name, (_, value, valid) in workloads.items():
        def run(label, checked=False):
            binary = out / (name + '-' + label + ('-census' if checked else ''))
            start = time.perf_counter_ns()
            r = subprocess.run([str(binary), str(a.iterations)], env=env, capture_output=True, timeout=60)
            elapsed = (time.perf_counter_ns() - start) / 1e6
            success = valid or label == 'before'
            pos = 1 if name in ('split_capture', 'uncaptured_byte') else len(value.encode())
            caplen = 0 if name == 'uncaptured_byte' or not success else pos
            want = f'{(pos + caplen + (1000 if success else 0)) * a.iterations}\n'.encode()
            assert r.returncode == 0 and r.stdout == want, (name, label, r.returncode, r.stdout, want, r.stderr)
            return r, elapsed
        rows = {}
        for label in ['before', 'after']:
            r, _ = run(label, True)
            m = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\n', r.stderr)
            assert m and m[1] == m[2] and int(m[3]) == 0, (name, label, r.stderr)
            rows[label] = {'allocations': int(m[1]), 'samples_ms': []}
        for iteration in range(9):
            for label in (['before', 'after'] if iteration % 2 == 0 else ['after', 'before']):
                r, elapsed = run(label)
                assert not r.stderr, r.stderr
                if iteration >= 2:
                    rows[label]['samples_ms'].append(elapsed)
        for row in rows.values():
            row['median_ms'] = statistics.median(row['samples_ms'])
        result['workloads'][name] = rows
    print(json.dumps(result, indent=2))
