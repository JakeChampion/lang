"""Prove the declared ABI with independent whole bit-vector expressions."""
import bisect
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('independent_instruction_reference', ROOT / 'scripts/gen-large-push-wide-decoder-reference.py')
DECODER = importlib.util.module_from_spec(spec)
spec.loader.exec_module(DECODER)
LIMIT = 1 << 34
BOUND = (1 << 35) - 1
WORD = 1 << 32
CAPACITIES = sorted({q * (1 << (bit - 2)) for bit in range(19, 35) for q in range(4, 8) if q * (1 << (bit - 2)) <= LIMIT})
CUTS = sorted({0, LIMIT + 1, *[n + 1 for n in CAPACITIES if n < LIMIT], *[1 << bit for bit in range(19, 35)]})
INTERVALS = list(zip(CUTS[:-1], [n - 1 for n in CUTS[1:]]))
assert len(CAPACITIES) == 61 and len(INTERVALS) == 77


class Unproved(Exception):
    pass


def is_number(expr):
    return isinstance(expr, tuple) and len(expr) == 2 and all(type(n) is int for n in expr)


def endpoints(expr, interval):
    if not is_number(expr):
        raise Unproved('non-numeric word')
    a, b = expr
    return a * interval[0] + b, a * interval[1] + b


def numeric(a, b, interval):
    if a not in [0, 1] or not -BOUND <= b <= BOUND:
        raise Unproved('affine representation bound')
    result = (a, b)
    if not all(-BOUND <= n <= BOUND for n in endpoints(result, interval)):
        raise Unproved('affine endpoint bound')
    return result


def scalar(n, interval):
    return numeric(0, n, interval)


def low_word(expr, interval):
    if not is_number(expr):
        if isinstance(expr, tuple) and expr[0] == 'concat':
            return expr[1]
        if isinstance(expr, tuple) and expr[0] == 'slice':
            return expr
        return ('slice', expr, 0)
    low, high = endpoints(expr, interval)
    quotient = low // WORD
    if quotient != high // WORD:
        raise Unproved('nonuniform low-word projection')
    return numeric(expr[0], expr[1] - quotient * WORD, interval)


def part(expr, offset, interval):
    if offset == 0:
        return low_word(expr, interval)
    if is_number(expr):
        lo, hi = endpoints(expr, interval)
        if lo // WORD != hi // WORD:
            raise Unproved('nonuniform high-word projection')
        return scalar((lo // WORD) % WORD, interval)
    if expr[0] in ['concat', 'slice']:
        return (0, 0)
    return ('slice', expr, 1)


def narrow(expr, width, interval):
    if width == 64:
        return expr
    low = low_word(expr, interval)
    return low if is_number(low) else ('concat', low, (0, 0))


def num(expr, width, interval):
    result = narrow(expr, width, interval)
    endpoints(result, interval)
    return result


def constant(expr, width, interval):
    value = num(expr, width, interval)
    if value[0] != 0:
        raise Unproved('nonconstant operand')
    return value[1]


def truth(always, never):
    return True if always else False if never else None


def compare(expr, n, width, interval):
    if width == 64 and expr == ('oversized',):
        low, high = LIMIT + 1, (1 << 64) - 1
    else:
        low, high = endpoints(num(expr, width, interval), interval)
    if low < 0 or not 0 <= n <= BOUND:
        raise Unproved('unsupported unsigned comparison')
    return (truth(high < n, low >= n), truth(low == high == n, n < low or n > high), truth(high <= n, low > n))


def decision(flags, condition):
    if condition == 'jmp':
        return True
    aliases = {'je': (1, False), 'jz': (1, False), 'jne': (1, True), 'jnz': (1, True), 'jb': (0, False), 'jc': (0, False), 'jnae': (0, False), 'jae': (0, True), 'jnb': (0, True), 'jnc': (0, True), 'jbe': (2, False), 'jna': (2, False), 'ja': (2, True), 'jnbe': (2, True)}
    if condition not in aliases:
        raise Unproved('unsupported condition')
    at, invert = aliases[condition]
    result = flags[at]
    if result is None:
        raise Unproved('unknown branch outcome')
    return not result if invert else result


def path(ops, interval, oversized):
    regs = [('reg', at) for at in range(16)]
    regs[4], regs[8], regs[6] = ('stack',), ('block',), ('oversized',) if oversized else (1, 0)
    initial = regs.copy()
    old_head, old_block, old_return = ('old-head',), ('old-block',), ('old-return',)
    cells = {'block': old_block, 'slot': old_head, 'stack': old_return}
    writes = {'block': 0, 'slot': 0}
    flags = (None, None, None)
    pc = 0
    seen = set()
    low_class, high_class = [bisect.bisect_left(CAPACITIES, n) for n in interval]
    if low_class != high_class:
        raise Unproved('nonuniform expected class')
    cls = low_class

    def address(op):
        width, offset = op['width'], op['offset']
        if width not in [32, 64] or not 0 <= offset <= 8 - width // 8 or offset % 4:
            raise Unproved('invalid cell extent')
        base = regs[op['base']]
        if op['index'] == -1 and base in [('stack',), ('block',)]:
            return base[0], offset // 4
        if oversized or base != ('freelist',) or op['index'] < 0 or op['scale'] != 8:
            raise Unproved('invalid selected slot')
        if constant(regs[op['index']], 64, interval) != cls or not 0 <= cls <= 60:
            raise Unproved('wrong selected slot')
        return 'slot', offset // 4

    def finish():
        expected = initial.copy()
        expected[0] = (0, LIMIT if oversized else cls)
        if not oversized:
            expected[1], expected[2] = old_head, ('freelist',)
        return regs == expected and cells == {'block': old_block if oversized else old_head, 'slot': old_head if oversized else ('block',), 'stack': old_return} and writes == {'block': 0 if oversized else 1, 'slot': 0 if oversized else 1}

    for _ in range(len(ops)):
        if not 0 <= pc < len(ops) or pc in seen:
            raise Unproved('cycle or absent return')
        seen.add(pc)
        op = ops[pc]
        kind, width, src, dst = [op[n] for n in ['kind', 'width', 'source', 'destination']]
        following = pc + 1
        if kind == 18:
            return finish()
        if kind == 17:
            following = op['target'] if decision(flags, op['condition']) else following
        elif kind == 1:
            pass
        elif kind in [5, 41]:
            regs[dst] = narrow(scalar(op['immediate'], interval), width, interval)
        elif kind == 2:
            regs[dst] = narrow(regs[src], width, interval)
        elif kind in [3, 4]:
            cell, half = address(op)
            if kind == 3:
                regs[dst] = cells[cell] if width == 64 else narrow(part(cells[cell], half, interval), 32, interval)
            else:
                if oversized or width != 64 or half != 0 or cell not in writes or writes[cell] != 0:
                    raise Unproved('forbidden or repeated store')
                cells[cell] = regs[src]
                writes[cell] += 1
        elif kind == 19:
            if width != 64 or op['symbol'] != '__fern_large_freelist':
                raise Unproved('wrong table identity')
            regs[dst] = ('freelist',)
        elif kind in [16, 28]:
            operand = regs[src] if kind == 16 else regs[dst]
            n = op['immediate'] if kind == 16 else constant(regs[src], width, interval)
            flags = compare(operand, n, width, interval)
        elif kind == 15:
            base = num(regs[op['base']], 64, interval)
            index = 0 if op['index'] == -1 else constant(regs[op['index']], 64, interval)
            regs[dst] = numeric(base[0], base[1] + index * op['scale'] + op['offset'], interval)
        elif kind in [8, 9, 10, 11, 12, 13, 14, 20, 21]:
            if kind in [13, 14, 20, 21]:
                count = op['immediate'] if kind in [20, 21] else constant(regs[1], 32, interval)
                if count < 0:
                    raise Unproved('negative shift count')
                count %= width
                if count == 0:
                    regs[dst] = narrow(regs[dst], width, interval)
                    pc = following
                    continue
                n = constant(regs[dst], width, interval)
                if n < 0:
                    raise Unproved('negative shifted value')
                result = scalar(n << count if kind in [13, 20] else n >> count, interval)
            elif kind == 8:
                value = num(regs[src], width, interval)
                low, high = endpoints(value, interval)
                if low <= 0 or low.bit_length() != high.bit_length():
                    raise Unproved('zero or nonuniform bit scan')
                result = scalar(low.bit_length() - 1, interval)
            elif kind == 12:
                result = scalar(-constant(regs[src], width, interval), interval)
            else:
                value = num(regs[dst], width, interval)
                rhs = op['immediate'] if src == -1 else constant(regs[src], width, interval)
                if kind == 11:
                    low, high = endpoints(value, interval)
                    step = -rhs
                    if width != 64 or step <= 0 or step & (step - 1) or low < 0 or low & rhs != high & rhs:
                        raise Unproved('nonuniform bit mask')
                    result = scalar(low & rhs, interval)
                else:
                    result = numeric(value[0], value[1] + (-rhs if kind == 9 else rhs), interval)
                    if width == 32 and endpoints(result, interval)[0] < 0:
                        raise Unproved('unsupported negative arithmetic result')
            regs[dst] = narrow(result, width, interval)
            flags = (None, None, None)
        else:
            raise Unproved('unsupported instruction')
        pc = following
    raise Unproved('no proved return')


def proof(body):
    ops = DECODER.decode_body(body)
    if ops is None:
        return False
    try:
        return all(path(ops, interval, False) for interval in INTERVALS) and path(ops, (0, 0), True)
    except Unproved:
        return False
