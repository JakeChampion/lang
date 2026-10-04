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
out = Path('/tmp/fern-timefmt-bench')
out.mkdir(exist_ok=True)
env = {k: v for k, v in os.environ.items() if not k.startswith('FERN_')}
workloads = {
    'ascii': ('%Y-%m-%d %H:%M:%S %z', '1970-01-01 00:00:00 +0000'),
    'unicode': ('é€🙂%Y|%5é|%^8é|%:é', 'é€🙂1970|  %5é|    %^8é|%:é'),
}
if a.build:
    builds = {}
    for name, (fmt, expected) in workloads.items():
        for label, root in [('before', a.before_root), ('after', a.after_root)]:
            src = out / (name + '-' + label + '.fern')
            source = '''import "core/int";
import "std/i64";
import "std/tz";
import TIMEFMT as timefmt;
function main(): i32 {
  let argv: string[] = args();
  let Some(count) = int.parse_int_radix(argv[1], 10) else { return 2; };
  let f: timefmt.Format = timefmt.compile(FORMAT);
  let z: tz.Zone = tz.utc_zone();
  let sum: i64 = 0;
  let i: i32 = 0;
  while (i < count) {
    let text: TYPE = timefmt.RENDER(f, z, 0, 0);
    if (argv.len() > 2) { EMIT }
    sum = sum + text.len() as i64 + text[0] as i64 + text[text.len() - 1] as i64;
    i = i + 1;
  }
  print(sum.to_string());
  return 0;
}
'''
            source = source.replace('TIMEFMT', json.dumps(os.path.relpath(root/'coreutils/lib/timefmt', src.parent))).replace('FORMAT', json.dumps(fmt, ensure_ascii=False))
            source = source.replace('TYPE', 'string' if label == 'before' else 'u8[]').replace('RENDER', 'format' if label == 'before' else 'format_bytes').replace('EMIT', 'write(text);' if label == 'before' else 'let w = stdout(); w.write_bytes(text);')
            src.write_text(source)
            for checked in [False, True]:
                binary = out / (name + '-' + label + ('-census' if checked else ''))
                runenv = dict(env)
                if checked:
                    runenv.update(FERN_SANITIZE='1', FERN_LEAKCHECK='1')
                subprocess.run([str(root/'build/bootstrap/stage2'), '-target', 'arm64-darwin', '-o', str(binary), str(src), str(root/'internal/stdlib')], env=runenv, check=True, capture_output=True, timeout=60)
                builds[binary.name] = {'bytes': binary.stat().st_size, 'sha256': hashlib.sha256(binary.read_bytes()).hexdigest()}
    print(json.dumps(builds, indent=2))
else:
    result = {'iterations': a.iterations, 'warmups': 2, 'samples': 7, 'workloads': {}}
    for name, (_, expected) in workloads.items():
        data = expected.encode()
        checksum = len(data) + data[0] + data[-1]
        def run(label, checked=False):
            binary = out / (name + '-' + label + ('-census' if checked else ''))
            start = time.perf_counter_ns()
            r = subprocess.run([str(binary), str(a.iterations)], env=env, capture_output=True, timeout=60)
            elapsed = (time.perf_counter_ns() - start) / 1e6
            assert r.returncode == 0 and r.stdout == f'{checksum * a.iterations}\n'.encode(), (name, label, r)
            return r, elapsed
        rows = {}
        for label in ['before', 'after']:
            binary = out / (name + '-' + label)
            exact = subprocess.run([str(binary), '1', 'check'], env=env, capture_output=True, timeout=60)
            assert exact.returncode == 0 and not exact.stderr and exact.stdout == data + f'{checksum}\n'.encode(), (name, label, exact)
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
