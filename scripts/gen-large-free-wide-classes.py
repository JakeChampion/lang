"""Freeze integer classifications and set-based refinements for issue 13750."""
import argparse
import bisect
import gzip
import hashlib
import json
from pathlib import Path
import struct

ROOT = Path(__file__).resolve().parent.parent
REFERENCE = ROOT / 'internal/testing/e2ecompiler/testdata/large_free_wide_classes.json.gz'
DRIVER = ROOT / 'compiler/drivers/ssalargefreewideclassescases.fern'
CAPS = [(4 + n % 4) * 2 ** (17 + n // 4) for n in range(61)]
LEGACY = ['compiler/ssalargearrayvalue.fern', 'compiler/ssastringvalue.fern', 'compiler/ssacapacityvalue.fern', 'compiler/ssalargepushranges.fern']


def packet(c):
    return struct.pack('<4sIIqqii', b'FWC1', c['id'], *c['input'])


def small_grid():
    cuts = {1, 257, 65536}
    for bit in range(8, 16):
        cuts.add(2 ** bit)
        cuts.update(range(2 ** bit + 1, 2 ** (bit + 1), 2 ** (bit - 2)))
    points = sorted(p for p in cuts if p == 1 or p > 256)
    return [(a, b - 1) for a, b in zip(points, points[1:]) if a <= b - 1]


def old_grid(mode):
    if mode == 0:
        rows = []
        lo = 65533
        for cap in CAPS[:45]:
            hi = cap // 8 - 3
            rows.append([lo, hi, 0, 0])
            lo = hi + 1
        for k in range(1, 9):
            hi = k * 2 ** 29 - 3
            rows.append([lo, hi - 1, 0, 0])
            lo = hi
        return rows + [[lo, 2 ** 32 - 1, 0, 0]]
    extra = 3 if mode == 2 else 0
    rows = [[0, 0, 0, 0]] if mode == 2 else []
    rows += [[max(a - extra, 1), b - extra, 0, 7] for a, b in small_grid() if max(a - extra, 1) <= b - extra]
    lo = 65536 - extra
    for cap in CAPS[:45]:
        hi = cap // 8 - extra
        rows.append([lo, hi, 0, 7])
        lo = hi + 1
    if mode == 2:
        return rows + [[lo, 2 ** 61 - 4, 0, 7]]
    return rows + [[lo, 2 ** 60 - 1, 0, 7], [2 ** 60, 2 ** 60, 1, 7]]


def refined(mode):
    extra = 0 if mode == 1 else 3
    boundaries = {cap // 8 - extra + 1 for cap in CAPS}
    rows = []
    for lo, hi, mn, mx in old_grid(mode):
        points = sorted({lo, hi + 1} | {p for p in boundaries if lo < p <= hi})
        rows.extend([[a, b - 1, mn, mx] for a, b in zip(points, points[1:])])
    assert all(a <= b for a, b, _, _ in rows)
    assert sum(b - a + 1 for a, b, _, _ in rows) == sum(b - a + 1 for a, b, _, _ in old_grid(mode))
    for old in old_grid(mode):
        pieces = [r for r in rows if old[0] <= r[0] <= r[1] <= old[1] and r[2:] == old[2:]]
        assert pieces and pieces[0][0] == old[0] and pieces[-1][1] == old[1]
        assert all(a[1] + 1 == b[0] for a, b in zip(pieces, pieces[1:]))
    return rows


def classify(mode, lo, hi, mn, mx, caps):
    if mode == 0:
        valid = 65533 <= lo <= hi <= 2 ** 32 - 1
    else:
        valid = 0 <= lo <= hi <= 2 ** 61 - 4 and 0 <= mn <= mx <= 7 and (lo > 0 or mx == 0)
    if not valid:
        return -1

    def scalar(n):
        size = 8 * (n + (0 if mode == 1 else 3))
        if mode != 0 and size <= 8 * 65535:
            return -3
        index = bisect.bisect_left(caps, size)
        return index if index < len(caps) else -2

    a, b = scalar(lo), scalar(hi)
    return a if a == b else -1


def response(values):
    mode, lo, hi, mn, mx = values
    if mode < 3:
        return [classify(mode, lo, hi, mn, mx, CAPS), classify(mode, lo, hi, mn, mx, CAPS[:45]), 0, 0, 0, 0]
    rows = refined(mode - 3)
    if not 0 <= lo < len(rows):
        return [-1, len(old_grid(mode - 3)), 0, 0, 0, 0]
    return [len(rows), len(old_grid(mode - 3)), *rows[lo]]


def catalog():
    cases = []

    def add(name, values):
        c = dict(id=len(cases), name=name, input=values, expected=response(values))
        c['payload_sha256'] = hashlib.sha256(packet(c)).hexdigest()
        cases.append(c)

    for mode in range(3):
        for kind, grid in [('legacy', old_grid(mode)), ('current', refined(mode))]:
            for at, row in enumerate(grid):
                add(f'{mode}-{kind}-class-{at}', [mode, *row])
        for at in range(-1, len(refined(mode)) + 1):
            add(f'{mode}-grid-{at}', [mode + 3, at, 0, 0, 0])
        extra = 0 if mode == 1 else 3
        for at, cap in enumerate(CAPS):
            cut = cap // 8 - extra
            for d in [-1, 0, 1]:
                add(f'{mode}-boundary-{at}-{d}', [mode, cut + d, cut + d, 0, 0 if mode == 0 else 7])
            add(f'{mode}-mixed-{at}', [mode, cut, cut + 1, 0, 0 if mode == 0 else 7])
        for at, row in enumerate([[-1, 0, 0, 0], [8, 7, 0, 0], [2 ** 63 - 1, 2 ** 63 - 1, 0, 0], [2 ** 61 - 4, 2 ** 61 - 4, 0, 7], [2 ** 61 - 3, 2 ** 61 - 3, 0, 7], [0, 0, 0, 0], [0, 1, 0, 7], [1, 3, -1, 7], [1, 3, 0, 8], [1, 3, 7, 0], [2 ** 60, 2 ** 60, 1, 7], [65533, 2 ** 32 - 1, 0, 7], [536870909, 536870909, 0, 7], [536870912, 536870912, 0, 7], [2147483646, 2147483646, 0, 7], [2147483649, 2147483649, 0, 7]]):
            add(f'{mode}-domain-{at}', [mode, *row])
    provenance = {p: hashlib.sha256((ROOT / p).read_bytes()).hexdigest() for p in LEGACY}
    grids = {str(m): dict(old=old_grid(m), current=refined(m)) for m in range(3)}
    assert [len(old_grid(m)) for m in range(3)] == [54, 87, 87]
    assert all(classify(m, *row, CAPS) != -1 for m in range(3) for row in refined(m))
    return dict(schema=1, issue=13750, legacy_sources=provenance, grids=grids, cases=cases)


def driver(bundle):
    out = ['import "./ssalargefreewideclasses" as classes;', '', 'pub struct Case { mode: i32, low: i64, high: i64, minimum: i32, maximum: i32, expected: i64[] }', '']
    cases = bundle['cases']
    for start in range(0, len(cases), 48):
        out.extend([f'function batch_{start}(): Case[] {{', '  return ['])
        for c in cases[start:start + 48]:
            mode, lo, hi, mn, mx = c['input']
            def n(v): return f'-({-v} as i64)' if v < 0 else f'{v} as i64'
            out.append('    Case { mode: %d, low: %s, high: %s, minimum: %d, maximum: %d, expected: [%s] },' % (mode, n(lo), n(hi), mn, mx, ', '.join(n(v) for v in c['expected'])))
        out.extend(['  ];', '}', ''])
    out += ['function check(cases: Case[]): boolean {', '  for c in cases {', '    let actual = classes.result(c.mode, c.low, c.high, c.minimum, c.maximum);', '    if (actual.len() != c.expected.len()) {', '      return false;', '    }', '    for at in 0..actual.len() {', '      if (actual[at] != c.expected[at]) {', '        return false;', '      }', '    }', '  }', '  return true;', '}', '', 'pub function run(): boolean {']
    out += [f'  if (!check(batch_{start}())) {{\n    return false;\n  }}' for start in range(0, len(cases), 48)]
    out += ['  return true;', '}', '']
    return '\n'.join(out)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    bundle = catalog()
    raw = (json.dumps(bundle, sort_keys=True, indent=2) + '\n').encode()
    expected = {REFERENCE: gzip.compress(raw, mtime=0), DRIVER: driver(bundle).encode()}
    if args.check:
        assert all(p.read_bytes() == value for p, value in expected.items()), 'reference or generated source differs'
    else:
        assert all(not p.exists() for p in expected), 'refuse existing output'
        for path, value in expected.items():
            path.write_bytes(value)
    print(json.dumps(dict(cases=len(bundle['cases']), grids=[len(refined(m)) for m in range(3)], sha256=hashlib.sha256(raw).hexdigest())))


if __name__ == '__main__':
    main()
