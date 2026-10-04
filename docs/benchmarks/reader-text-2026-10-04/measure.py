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
p.add_argument('--root', type=Path, required=True)
p.add_argument('--before', type=Path, required=True)
p.add_argument('--after', type=Path, required=True)
p.add_argument('--out', type=Path, required=True)
p.add_argument('--build', action='store_true')
p.add_argument('--chunks', type=int, default=128)
a = p.parse_args()
a.out.mkdir(exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
workloads = {
    'ascii64': b'abcdefgh' * 8,
    'ascii4096': b'abcdefgh' * 512,
    'unicode4095': 'aé中🙂'.encode() * 409 + b'abcde',
}
builds = {}
if a.build:
    for name, chunk in workloads.items():
        source = '''import "std/i64";
function main(): i32 {
  let r = stdin();
  let sum: i64 = 0;
  let done: boolean = false;
  while (!done) {
    match (r.read_chunk(CHUNK)) {
      Err(_) => { return 1; },
      Ok(s) => {
        if (s.len() == 0) { done = true; }
        else { sum = sum + s.len() as i64 + s[0] as i64 + s[s.len() - 1] as i64; }
      }
    }
  }
  print(sum.to_string());
  return 0;
}
'''.replace('CHUNK', str(len(chunk)))
        src = a.out / (name + '.fern')
        src.write_text(source)
        for label, compiler in [('before', a.before), ('after', a.after)]:
            for checked in [False, True]:
                binary = a.out / (name + '-' + label + ('-census' if checked else ''))
                runenv = dict(env)
                if checked:
                    runenv.update(FERN_SANITIZE='1', FERN_LEAKCHECK='1')
                subprocess.run([str(compiler), '-target', 'arm64-linux', '-o', str(binary), str(src), str(a.root/'internal/stdlib')], env=runenv, check=True, capture_output=True, timeout=60)
                builds[binary.name] = {'bytes': binary.stat().st_size, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
    print(json.dumps({'compilers': {label: hashlib.sha256(path.read_bytes()).hexdigest() for label, path in [('before', a.before), ('after', a.after)]}, 'builds': builds}, indent=2))
else:
    result = {'chunks': a.chunks, 'warmups': 2, 'samples': 7, 'workloads': {}}
    for name, chunk in workloads.items():
        input_path = a.out / (name + '.input')
        input_path.write_bytes(chunk * a.chunks)
        want = f'{(len(chunk) + chunk[0] + chunk[-1]) * a.chunks}\n'.encode()
        def run(label, checked=False):
            binary = a.out / (name + '-' + label + ('-census' if checked else ''))
            with input_path.open('rb') as stdin:
                start = time.perf_counter_ns()
                r = subprocess.run([str(binary)], stdin=stdin, env=env, capture_output=True, timeout=60)
                ms = (time.perf_counter_ns() - start) / 1e6
            assert r.returncode == 0 and r.stdout == want, (name, label, r.returncode, r.stdout, want, r.stderr)
            return r, ms
        rows = {}
        for label in ['before', 'after']:
            r, _ = run(label, True)
            m = re.fullmatch(rb'leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)\n', r.stderr)
            assert m and m[1] == m[2] and int(m[3]) == 0, (name, label, r.stderr)
            rows[label] = {'allocations': int(m[1]), 'samples_ms': []}
        for iteration in range(9):
            for label in (['before', 'after'] if iteration % 2 == 0 else ['after', 'before']):
                r, ms = run(label)
                assert not r.stderr, r.stderr
                if iteration >= 2:
                    rows[label]['samples_ms'].append(ms)
        for row in rows.values():
            row['median_ms'] = statistics.median(row['samples_ms'])
        result['workloads'][name] = {'input_bytes': input_path.stat().st_size, **rows}
    print(json.dumps(result, indent=2))
