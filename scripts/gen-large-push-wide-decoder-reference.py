"""Generate exact instruction records for the declared large-free parser subset."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import random
import re
import struct
import subprocess

ROOT = Path(__file__).resolve().parent.parent
REFERENCE = ROOT / 'internal/testing/e2ecompiler/testdata/large_push_wide_decoder_reference.json.gz'
FERN = ROOT / 'compiler/drivers/ssalargepushwideisacases.fern'
FIXTURE = ROOT / 'internal/testing/e2ecompiler/testdata/large_push_wide_decoder_current_helper.txt'
SEED = 0x13738
REG64 = ['%rax', '%rcx', '%rdx', '%rbx', '%rsp', '%rbp', '%rsi', '%rdi'] + [f'%r{i}' for i in range(8, 16)]
REG32 = ['%eax', '%ecx', '%edx', '%ebx', '%esp', '%ebp', '%esi', '%edi'] + [f'%r{i}d' for i in range(8, 16)]
BRANCHES = ['jmp', 'je', 'jz', 'jne', 'jnz', 'js', 'jns', 'jb', 'jc', 'jnae', 'jae', 'jnb', 'jnc', 'jbe', 'jna', 'ja', 'jnbe', 'jl', 'jnge', 'jge', 'jnl', 'jg', 'jnle', 'jle', 'jng', 'jo', 'jno']
FIELDS = ['kind', 'width', 'source', 'destination', 'base', 'index', 'scale', 'offset', 'narrow_immediate', 'immediate', 'condition', 'target', 'symbol']


def sha(data):
    return hashlib.sha256(data).hexdigest()


def packet(case):
    data = case['source'].encode('utf8')
    return struct.pack('<4sII', b'FWI1', case['id'], len(data)) + data


def operation(kind, **kw):
    result = dict(kind=kind, width=0, source=-1, destination=-1, base=-1, index=-1, scale=1, offset=0, narrow_immediate=0, immediate=0, condition='', target=-1, symbol='')
    result.update(kw)
    return result


def decimal(text, bits):
    if not re.fullmatch(r'0|-?[1-9][0-9]*', text, re.ASCII):
        return None
    value = int(text)
    return value if -(1 << (bits - 1)) <= value < 1 << (bits - 1) else None


def narrow(text):
    if re.fullmatch(r'0[xX][0-9a-fA-F]+', text, re.ASCII):
        n = int(text, 16)
        return n if n <= (1 << 31) - 1 else None
    return decimal(text, 32)


def address(text):
    match = re.fullmatch(r'(-?[0-9]+)?\((%[a-z0-9]+)(?:,(%[a-z0-9]+)(?:,([1248]))?)?\)', text, re.ASCII)
    if not match:
        return None
    off, base, index, scale = match.groups()
    offset = 0 if off is None else decimal(off, 32)
    if offset is None or base not in REG64 or index is not None and (index not in REG64 or index == '%rsp'):
        return None
    return dict(base=REG64.index(base), index=-1 if index is None else REG64.index(index), scale=1 if scale is None else int(scale), offset=offset)


def decode_line(line, labels):
    if line.endswith(':'):
        return operation(1) if re.fullmatch(r'\.[A-Za-z0-9_.$]+:', line, re.ASCII) else None
    if line == 'ret':
        return operation(18)
    match = re.fullmatch(r'([a-z]+)[ \t]+(.+)', line, re.ASCII)
    if not match:
        return None
    name, rest = match.groups()
    if name in BRANCHES:
        return operation(17, condition=name, target=labels[rest]) if rest in labels else None
    # A comma in a memory operand is not an instruction operand separator.
    args = re.split(r',(?![^()]*\))', rest)
    args = [a.strip(' \t\r') for a in args]
    if name in ['negq', 'negl'] and len(args) == 1:
        width = 64 if name.endswith('q') else 32
        regs = REG64 if width == 64 else REG32
        return operation(12, width=width, source=regs.index(args[0]), destination=regs.index(args[0])) if args[0] in regs else None
    if len(args) != 2:
        return None
    a, b = args
    if name == 'movabs':
        value = decimal(a[1:], 64) if a.startswith('$') else None
        return operation(41, width=64, destination=REG64.index(b), immediate=value) if value is not None and b in REG64 else None
    width = 64 if name.endswith('q') else 32 if name.endswith('l') else 0
    regs = REG64 if width == 64 else REG32
    src = regs.index(a) if a in regs else -1
    dst = regs.index(b) if b in regs else -1
    value = narrow(a[1:]) if a.startswith('$') else None
    if name in ['cmpq', 'cmpl'] and dst >= 0:
        if src >= 0:
            return operation(28, width=width, source=src, destination=dst)
        if value is not None:
            return operation(16, width=width, source=dst, narrow_immediate=value, immediate=value)
    if name in ['movq', 'movl']:
        if dst >= 0 and src >= 0:
            return operation(2, width=width, source=src, destination=dst)
        if dst >= 0 and value is not None:
            return operation(5, width=width, destination=dst, narrow_immediate=value, immediate=value)
        addr = address(a)
        if dst >= 0 and addr is not None:
            return operation(3, width=width, destination=dst, **addr)
        addr = address(b)
        if src >= 0 and addr is not None:
            return operation(4, width=width, source=src, **addr)
    if name in ['addq', 'addl', 'subq', 'subl', 'andq', 'andl'] and dst >= 0 and (src >= 0 or value is not None):
        value = 0 if src >= 0 else value
        return operation({'sub': 9, 'add': 10, 'and': 11}[name[:-1]], width=width, source=src, destination=dst, narrow_immediate=value, immediate=value)
    if name in ['bsrq', 'bsrl'] and src >= 0 and dst >= 0:
        return operation(8, width=width, source=src, destination=dst)
    if name in ['shlq', 'shll', 'shrq', 'shrl'] and dst >= 0:
        if a == '%cl':
            return operation(13 if name.startswith('shl') else 14, width=width, source=1, destination=dst)
        if value is not None and 0 <= value <= 255:
            return operation(20 if name.startswith('shl') else 21, width=width, destination=dst, narrow_immediate=value, immediate=value)
    if name == 'leaq' and b in REG64:
        if a == '__fern_large_freelist(%rip)':
            return operation(19, width=64, destination=REG64.index(b), symbol='__fern_large_freelist')
        addr = address(a)
        if addr is not None:
            return operation(15, width=64, destination=REG64.index(b), **addr)
    return None


def decode_body(body):
    lines = [line.strip(' \t\r') for line in body.splitlines() if line.strip(' \t\r')]
    definitions = [line[:-1] for line in lines if line.endswith(':')]
    if len(set(definitions)) != len(definitions):
        return None
    labels = {line[:-1]: i for i, line in enumerate(lines) if line.endswith(':')}
    result = [decode_line(line, labels) for line in lines]
    return result if all(op is not None for op in result) else None


def envelope(body):
    return '.text\n__fern_large_push:\n' + body + '\n__fern_capw:\nret\n'


def catalog():
    cases = []
    def add(name, body, required=None):
        ops = decode_body(body)
        if required is not None:
            assert (ops is not None) == required, (name, body, ops)
        source = envelope(body)
        case = dict(id=len(cases), name=name, source=source, expected=dict(ok=ops is not None, ops=[] if ops is None else ops))
        case['payload_sha256'] = sha(packet(case))
        cases.append(case)
    def special(name, source, ops):
        case = dict(id=len(cases), name=name, source=source, expected=dict(ok=ops is not None, ops=[] if ops is None else ops))
        case['payload_sha256'] = sha(packet(case))
        cases.append(case)
    values = {0, 1, -1, -(1 << 63), -(1 << 63) + 1, (1 << 63) - 1, (1 << 63) - 2}
    for bit in [0, 1, 7, 8, 15, 16, 30, 31, 32, 33, 34, 35, 62, 63]:
        for sign in [-1, 1]:
            for delta in [-1, 0, 1]:
                value = sign * (1 << bit) + delta
                if -(1 << 63) <= value < 1 << 63:
                    values.add(value)
    seeded = random.Random(SEED)
    values = sorted(values) + [seeded.randrange(-(1 << 63), 1 << 63) for _ in range(128)]
    for at, value in enumerate(values):
        for reg in REG64:
            add(f'movabs-{at}-{reg[1:]}', f'movabs ${value}, {reg}\nret', True)
    for width, regs in [(32, REG32), (64, REG64)]:
        for source in regs:
            for destination in regs:
                add(f'compare-{width}-{source[1:]}-{destination[1:]}', f'cmp{"q" if width == 64 else "l"} {source}, {destination}\nret', True)
    invalid = ['', '-', '+0', '+1', '00', '01', '-0', '-01', '0x1', '0X1', '1_0', '1e0', '1.0', ' 1', 'symbol', '1+1', str(1 << 63), str(-(1 << 63) - 1), str((1 << 64) - 1), '9' * 512]
    for at, spelling in enumerate(invalid):
        for reg in REG64:
            add(f'invalid-literal-{at}-{reg[1:]}', f'movabs ${spelling}, {reg}\nret', False)
    for at, reg in enumerate(REG32 + ['%xmm0', '%rip', '%r16', '%r-1', 'rax', '(%rax)', '%RAX']):
        add(f'invalid-movabs-register-{at}', f'movabs $17179869184, {reg}\nret', False)
    for a, b in zip(REG64, REG32, strict=True):
        for name, src, dst in [('cmpq', a, b), ('cmpq', b, a), ('cmpl', a, b), ('cmpl', b, a)]:
            add(f'mixed-{name}-{src[1:]}-{dst[1:]}', f'{name} {src}, {dst}\nret', False)
    for width, regs in [(32, REG32), (64, REG64)]:
        suffix = 'q' if width == 64 else 'l'
        for at, reg in enumerate(regs):
            add(f'ordinary-move-{width}-{at}', f'mov{suffix} {reg}, {regs[(at + 1) % 16]}\nret', True)
            add(f'ordinary-bits-{width}-{at}', f'bsr{suffix} {reg}, {reg}\nneg{suffix} {reg}\nret', True)
            for n in [-(1 << 31), -1, 0, (1 << 31) - 1]:
                add(f'ordinary-immediate-{width}-{at}-{n}', f'mov{suffix} ${n}, {reg}\nadd{suffix} $1, {reg}\nsub{suffix} $2, {reg}\nand{suffix} $-4, {reg}\nret', True)
            for count in [0, 31, 32, 63, 64, 255]:
                add(f'shift-{width}-{at}-{count}', f'shl{suffix} ${count}, {reg}\nshr{suffix} %cl, {reg}\nret', True)
    for scale in [1, 2, 4, 8]:
        for off in [-(1 << 31), -1, 0, (1 << 31) - 1]:
            addr = f'{off}(%r15,%r12,{scale})'
            add(f'address-{scale}-{off}', f'movq {addr}, %rax\nmovq %rax, {addr}\nleaq {addr}, %rdi\nret', True)
    for branch in BRANCHES:
        add('branch-' + branch, f'{branch} .Ltarget\nmovabs $1, %r15\n.Ltarget:\nret', True)
    bad = ['unknown_instruction', 'nop', 'retq', 'ret $8', 'call __fern_alloc', 'movabsq $1, %rax', 'movabs $1, %eax', 'cmpq %eax, %rsi', 'cmpq %rax, %esi', 'movq (%rax,%rsp,8), %rcx', 'movq (%rax,%rdx,3), %rcx', 'movq (%rax,%edx,8), %rcx', 'movq 2147483648(%rax), %rcx', 'movq $2147483648, %rax', 'shlq $256, %rax', 'shlq $-1, %rax', 'sarq $2, %rax', 'leaq wrong(%rip), %rax', 'movabs $1, %rax, %rdx', 'movabs $1,%rаx', 'movabs\u00a0$1,%rax']
    for at, line in enumerate(bad):
        for prefix in ['', 'movabs $17179869184, %rax\n']:
            add(f'unknown-{at}-{bool(prefix)}', prefix + line + '\nret', False)
    for at, body in enumerate(['ja .Lmissing\nret', 'ja __fern_alloc_size_abort.c\nret', 'jmp __fern_large_push\nret', '.Ldup:\nret\n.Ldup:\nret']):
        add('bad-transfer-' + str(at), body, False)
    ops = decode_body('movabs $17179869184, %rax\nret')
    special('quoted-outside-label', '.data\n.ascii "quoted __fern_large_push: .set alias, hidden"\n' + envelope('movabs $17179869184, %rax\nret'), ops)
    special('line-whitespace', '.text\r\n__fern_large_push:\r\n\tmovabs\t$17179869184 ,\t%rax\r\nret\r\n__fern_capw:\r\nret\r\n', ops)
    for at, source in enumerate(['.set alias, __fern_large_push\n' + envelope('ret'), envelope('ret') + '.data\n__fern_large_push: .quad 0\n', '.text\n__fern_large_push:\njmp .Loutside\n__other:\n.Loutside:\nret\n', '.text\n__fern_large_push:\n.Ldup:\nret\n__other:\n.Ldup:\nret\n']):
        special('namespace-' + str(at), source, None)
    special('empty-body-data', '.text\n__fern_large_push:\n__next:\nret\n', [])
    add('cycle-data', '.Lcycle:\njmp .Lcycle', True)
    helper = FIXTURE.read_text()
    assert helper.startswith('__fern_large_push:\n')
    body = helper.split('\n', 1)[1]
    exact = decode_body(body)
    assert exact is not None and len(exact) == 29 and exact[0]['immediate'] == 1 << 34 and exact[1]['source'] == 0 and exact[1]['destination'] == 6
    add('actual-current-helper', body, True)
    return dict(schema=1, seed=SEED, criterion='Exact signed integers and every operation field; no error waiver', fixture_sha256=sha(FIXTURE.read_bytes()), cases=cases)


def number(n, bits=32):
    if bits == 64:
        return '-(9223372036854775807 as i64) - 1 as i64' if n == -(1 << 63) else f'-({-n} as i64)' if n < 0 else f'{n} as i64'
    return str(n)


def fern_bytes(reference):
    lines = ['import "std/hex";', 'import "std/utf8";', '', 'pub struct ExpectedOp { kind: i32, width: i32, source: i32, destination: i32, base: i32, index: i32, scale: i32, offset: i32, narrow_immediate: i32, immediate: i64, condition: string, target: i32, symbol: string }', '', 'pub struct Case { id: i32, source: string, ok: boolean, ops: ExpectedOp[] }', '', 'function source(spelling: string): string {', '  if let Ok(bytes) = hex.decode(spelling) {', '    if let Ok(value) = utf8.from_bytes(bytes) {', '      return value;', '    }', '  }', '  exit(2);', '  return "";', '}', '', 'pub function rows(): Case[] {', '  return [']
    for case in reference['cases']:
        ops = []
        for op in case['expected']['ops']:
            fields = [key + ': ' + (json.dumps(op[key]) if isinstance(op[key], str) else number(op[key], 64 if key == 'immediate' else 32)) for key in FIELDS]
            ops.append('ExpectedOp { ' + ', '.join(fields) + ' }')
        lines.append('    Case { id: ' + str(case['id']) + ', source: source("' + case['source'].encode().hex() + '"), ok: ' + str(case['expected']['ok']).lower() + ', ops: [' + ', '.join(ops) + '] },')
    lines += ['  ];', '}', '', 'pub function count(): i32 {', f'  return {len(reference["cases"])};', '}', '']
    return '\n'.join(lines).encode()


def generate():
    ref = catalog()
    raw = (json.dumps(ref, sort_keys=True, separators=(',', ':'), ensure_ascii=True) + '\n').encode()
    return ref, gzip.compress(raw, mtime=0), fern_bytes(ref)


def main():
    args = argparse.ArgumentParser()
    args.add_argument('--write', action='store_true')
    args.add_argument('--check', action='store_true')
    options = args.parse_args()
    ref, data, fern = generate()
    if options.write:
        for p in [REFERENCE, FERN]:
            relative = str(p.relative_to(ROOT))
            if p.exists() or subprocess.run(['git', 'cat-file', '-e', 'HEAD:' + relative], cwd=ROOT, stderr=subprocess.DEVNULL).returncode == 0 or subprocess.check_output(['git', 'ls-files', '--', relative], cwd=ROOT):
                raise SystemExit('Refuse existing reference output: ' + relative)
        REFERENCE.write_bytes(data)
        FERN.write_bytes(fern)
    if options.check:
        assert gzip.decompress(REFERENCE.read_bytes()) == gzip.decompress(data) and FERN.read_bytes() == fern, 'reference reproduction differs'
    print(json.dumps(dict(cases=len(ref['cases']), success=sum(c['expected']['ok'] for c in ref['cases']), errors=sum(not c['expected']['ok'] for c in ref['cases']), bundle_sha256=sha(data + b'\n--FERN--\n' + fern))))


if __name__ == '__main__':
    main()
