#!/usr/bin/env python3
"""Generate exact integer reference facts for the wide proof helpers."""
import argparse
import bisect
import json
import random
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LIMIT = 1 << 34
BOUND = (1 << 35) - 1
WORD = 1 << 32
UNKNOWN = (-1, 0)
FIELDS = ['id', 'op', 'low', 'high', 'factor', 'offset', 'other_factor', 'other_offset', 'scalar', 'expected_factor', 'expected_offset', 'expected_scalar', 'less', 'equal', 'less_equal']
OPS = ['range_valid', 'class', 'rounded', 'number', 'original', 'valid', 'add', 'negate', 'zero32', 'high32', 'join32', 'power', 'bit_scan', 'mask', 'compare']

def capacities():
    result = []
    cap, step, boundary = 1 << 19, 1 << 17, 1 << 20
    while cap <= LIMIT:
        result.append(cap)
        cap += step
        if cap == boundary:
            step *= 2
            boundary *= 2
    assert len(result) == 61 and result[-1] == LIMIT
    return result

CAPS = capacities()
CUTS = sorted({0, LIMIT + 1, *[x + 1 for x in CAPS if x < LIMIT], *[1 << bit for bit in range(19, 35)]})
PARTS = list(zip(CUTS[:-1], [n - 1 for n in CUTS[1:]]))
assert len(PARTS) == 77 and sum(hi - lo + 1 for lo, hi in PARTS) == LIMIT + 1

def range_valid(r):
    return 0 <= r[0] <= r[1] <= LIMIT

def valid(r, v):
    return range_valid(r) and v[0] in (0, 1) and abs(v[1]) <= BOUND and all(abs(v[0] * n + v[1]) <= BOUND for n in r)

def checked(r, v):
    return v if valid(r, v) else UNKNOWN

def image(r, v):
    return [v[0] * n + v[1] for n in r]

def value_result(op, r, v, m, scalar):
    if op == 3:
        return checked(r, (0, scalar))
    if op == 4:
        return checked(r, (1, 0))
    if not valid(r, v):
        return UNKNOWN
    if op == 6:
        return checked(r, (v[0], v[1] + scalar)) if abs(scalar) <= BOUND else UNKNOWN
    if op == 7:
        return checked(r, (0, -v[1])) if v[0] == 0 else UNKNOWN
    if op in (8, 9):
        quotients = [n // WORD for n in image(r, v)]
        if quotients[0] != quotients[1]:
            return UNKNOWN
        if op == 8:
            return checked(r, (v[0], v[1] - quotients[0] * WORD))
        return checked(r, (0, quotients[0] % WORD))
    if op == 10:
        if not valid(r, m) or m[0] != 0 or not 0 <= m[1] < WORD or not all(0 <= n < WORD for n in image(r, v)):
            return UNKNOWN
        full = [(n | (m[1] << 32)) for n in image(r, v)]
        signed = [n - (1 << 64) if n >= (1 << 63) else n for n in full]
        result = (v[0], signed[0] - v[0] * r[0])
        assert result[0] * r[1] + result[1] == signed[1]
        return checked(r, result)
    if op == 13:
        if not valid(r, m) or m[0] != 0 or m[1] >= 0 or (-m[1]) & (-m[1] - 1) or image(r, v)[0] < 0:
            return UNKNOWN
        masked = [n & m[1] for n in image(r, v)]
        return checked(r, (0, masked[0])) if masked[0] == masked[1] else UNKNOWN
    raise AssertionError(op)

def scalar_result(op, r, v, scalar):
    if op == 0:
        return int(range_valid(r))
    if op in (1, 2):
        if not range_valid(r):
            return -1
        indexes = [bisect.bisect_left(CAPS, max(n, 1 << 19)) for n in r]
        if indexes[0] != indexes[1]:
            return -1
        if op == 2 and max(r[0], 1 << 19).bit_length() != max(r[1], 1 << 19).bit_length():
            return -1
        return indexes[0] if op == 1 else CAPS[indexes[0]]
    if op == 5:
        return int(valid(r, v))
    if op == 11:
        return 1 << scalar if 0 <= scalar <= 34 else 0
    if op == 12:
        if not valid(r, v):
            return -1
        points = image(r, v)
        return points[0].bit_length() - 1 if points[0] > 0 and points[0].bit_length() == points[1].bit_length() else -1
    raise AssertionError(op)

def comparison(r, v, scalar):
    if not valid(r, v) or abs(scalar) > BOUND:
        return (-1, -1, -1)
    points = {r[0], r[1], (r[0] + r[1]) // 2}
    if v[0] == 1:
        root = scalar - v[1]
        points.update(n for n in [root - 1, root, root + 1] if r[0] <= n <= r[1])
    values = [v[0] * n + v[1] for n in points]
    def fact(predicate):
        outcomes = {predicate(n, scalar) for n in values}
        return int(outcomes.pop()) if len(outcomes) == 1 else -1
    return (fact(lambda a, b: a < b), fact(lambda a, b: a == b), fact(lambda a, b: a <= b))

def catalogue():
    rows = []
    def add(op, r=(0, 0), v=(0, 0), m=(0, 0), scalar=0):
        row = dict(zip(FIELDS, [len(rows), op, *r, *v, *m, scalar, -1, 0, 0, -1, -1, -1]))
        if op in (3, 4, 6, 7, 8, 9, 10, 13):
            row['expected_factor'], row['expected_offset'] = value_result(op, r, v, m, scalar)
        elif op == 14:
            row['less'], row['equal'], row['less_equal'] = comparison(r, v, scalar)
        else:
            row['expected_scalar'] = scalar_result(op, r, v, scalar)
        assert all(-(1 << 63) <= n < (1 << 63) for n in row.values())
        rows.append(row)
    for r in PARTS:
        for op in (0, 1, 2, 4, 8, 9, 12, 14):
            add(op, r, (1, 0), scalar=LIMIT)
        low, high = value_result(8, r, (1, 0), (0, 0), 0), value_result(9, r, (1, 0), (0, 0), 0)
        assert low != UNKNOWN and high != UNKNOWN
        add(10, r, low, high)
        v = (0, 1 << 19) if r[1] < (1 << 19) else (1, 0)
        quantum = 1 << ((v[0] * r[0] + v[1]).bit_length() - 3)
        add(6, r, v, scalar=quantum - 1)
        add(13, r, (v[0], v[1] + quantum - 1), (0, -quantum))
    numbers = [-(1 << 63), -BOUND - 1, -BOUND, -WORD - 1, -WORD, -1, 0, 1, WORD - 1, WORD, LIMIT, BOUND, BOUND + 1, (1 << 63) - 1]
    regions = [(0, 0), (0, LIMIT), (WORD - 1, WORD), (WORD, WORD + 9), (LIMIT, LIMIT), (-1, 0), (2, 1), (0, LIMIT + 1), (-(1 << 63), (1 << 63) - 1)]
    for r in regions:
        for n in numbers:
            for op in (3, 5, 6, 7, 8, 9, 12, 13, 14):
                add(op, r, (0, n), (0, -8), n)
        for factor in (-1, 0, 1, 2):
            for op in (4, 5, 6, 7, 8, 9, 10, 12, 13, 14):
                add(op, r, (factor, 0), (factor, 0), 1)
    for bit in [-(1 << 31), -1, *range(36), (1 << 31) - 1]:
        add(11, scalar=bit)
    for high in [0, 1, 4, 7, 8, (1 << 31) - 1, 1 << 31, WORD - 8, WORD - 1, WORD]:
        for low in [0, 1, WORD - 1, WORD]:
            add(10, v=(0, low), m=(0, high))
    rng = random.Random(0x13725)
    for index in range(2048):
        if index % 4:
            a, b = rng.choice(PARTS)
            x, y = rng.randint(a, b), rng.randint(a, b)
            r = (min(x, y), max(x, y))
        else:
            r = rng.choice(regions)
        factor = rng.choice([0, 0, 1, 1, 2, -1])
        offset = rng.choice(numbers + [rng.randint(-BOUND, BOUND)])
        other = (rng.choice([0, 0, 1]), rng.choice(numbers + [-(1 << rng.randrange(35))]))
        op = rng.randrange(15)
        scalar = rng.choice([-(1 << 31), -1, *range(36), (1 << 31) - 1]) if op == 11 else rng.choice(numbers)
        add(op, r, (factor, offset), other, scalar)
    assert {r['op'] for r in rows} == set(range(15))
    return rows

def literal(n):
    return '-(9223372036854775807 as i64) - (1 as i64)' if n == -(1 << 63) else str(n)

def outputs():
    rows = catalogue()
    document = dict(schema='wide-large-push-integer-facts-v1',limit=LIMIT,affine_bound=BOUND,seed=0x13725,operations=OPS,capacities=CAPS,partitions=PARTS,case_count=len(rows),cases=rows)
    source = '// Generated by scripts/gen-large-push-wide-reference.py.\n\npub struct Case { '+', '.join(n+': '+('i32' if n in ['id','op','factor','other_factor','expected_factor','less','equal','less_equal'] else 'i64') for n in FIELDS)+' }\n\npub function count(): i32 {\n  return '+str(len(rows))+';\n}\n\npub function rows(): Case[] {\n  return [\n'
    source += ''.join('    Case { '+', '.join(n+': '+literal(row[n]) for n in FIELDS)+' },\n' for row in rows)
    source += '  ];\n}\n'
    return {ROOT/'compiler/drivers/ssalargepushwidecases.fern':source.encode(),ROOT/'internal/testing/e2ecompiler/testdata/large_push_wide_reference.json':(json.dumps(document,indent=2)+'\n').encode()}

def main():
    p=argparse.ArgumentParser();p.add_argument('--write',action='store_true');p.add_argument('--check',action='store_true');args=p.parse_args();assert args.write != args.check
    for path, data in outputs().items():
        if args.write:
            relative=str(path.relative_to(ROOT));assert not path.exists()
            assert subprocess.run(['git','cat-file','-e','HEAD:'+relative],cwd=ROOT,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
            assert subprocess.run(['git','ls-files','--error-unmatch',relative],cwd=ROOT,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode != 0
            path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(data)
        else:
            assert path.read_bytes()==data,str(path)
    print('exact wide reference: '+str(len(catalogue()))+' cases, 77 partitions, 61 capacities')

if __name__=='__main__':main()
