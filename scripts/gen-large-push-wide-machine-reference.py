"""Freeze independent exact proofs for the declared current helper contract."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import random
import struct
import subprocess

from large_push_wide_machine_reference import proof, DECODER, ROOT, INTERVALS

REFERENCE = ROOT / 'internal/testing/e2ecompiler/testdata/large_push_wide_machine_reference.json.gz'
FIXTURE = ROOT / 'internal/testing/e2ecompiler/testdata/large_push_wide_decoder_current_helper.txt'
LEGACY_PATH = 'compiler/drivers/ssalargepushmachines.fern'
LEGACY_SHA256 = 'dbdffb26523a9e0a333028827b53b6c8566171a1f9f0e5ec7faf014b40bea229'
SEED = 0x13744


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def packet(case):
    data = case['source'].encode()
    return struct.pack('<4sII', b'FWP1', case['id'], len(data)) + data


def legacy_text():
    # The predecessor driver is deleted; its text survives only in the frozen reference.
    text = json.loads(gzip.decompress(REFERENCE.read_bytes()))['legacy_source_text']
    assert sha(text.encode()) == LEGACY_SHA256
    return text


def catalog():
    helper = FIXTURE.read_text().split('\n', 1)[1]
    cases = []
    legacy = legacy_text()
    inherited = {}
    for old, new in [('bsrq %rax, %rcx', 'bsrl %eax, %ecx'), ('shrq %cl, %rdx', 'shrl %cl, %edx'), ('cmpq $524288, %rax', 'cmpl $524288, %eax')]:
        original = f'mutation(s, {json.dumps(old)}, {json.dumps(new)}, true);'
        assert original in legacy
        inherited[(old, new)] = dict(source_path=LEGACY_PATH, source_sha256=LEGACY_SHA256, original_assertion=original, original_expected=True, original_context='Bounded predecessor through 1 GiB; current wider expectation is explicit in issue #13744')

    def add(name, body, required, origin=None, source=None):
        expected = proof(body) if body is not None else False
        assert expected is required, ('independent reference conflicts with declared control', name, expected, required)
        c = dict(id=len(cases), name=name, source=DECODER.envelope(body) if source is None else source, expected=expected, legacy_provenance=origin)
        c['payload_sha256'] = sha(packet(c))
        cases.append(c)

    def mutation(name, old, new, required):
        if old == 'ret\n':
            old, new = '\nret\n', '\n' + new
        assert old in helper, (name, old)
        add(name, helper.replace(old, new), required, inherited.get((old, new)))

    add('actual-current-helper', helper, True)
    add('missing-effects', 'ret', False)
    for at, line in enumerate(['bsrq %rdx, %rcx', 'movl $0, %ecx', 'movl $19, %ecx']):
        mutation('wrong-bit-scan-' + str(at), 'bsrq %rax, %rcx', line, False)
    for name, old, new, expected in [
        ('bit-scan32', 'bsrq %rax, %rcx', 'bsrl %eax, %ecx', False),
        ('subtract64', 'subl $2, %ecx', 'subq $2, %rcx', True),
        ('wrong-bit-offset', 'subl $2, %ecx', 'subl $1, %ecx', False),
        ('left-shift32', 'shlq %cl, %rdx', 'shll %cl, %edx', True),
        ('right-shift32', 'shrq %cl, %rdx', 'shrl %cl, %edx', False),
        ('masked-shift64', 'shlq $2, %rcx', 'shlq $66, %rcx', True),
        ('masked-shift32', 'shlq $2, %rcx', 'shll $34, %ecx', True),
        ('wrong-class-shift', 'shlq $2, %rcx', 'shlq $1, %rcx', False),
        ('missing-round-offset', 'leaq -1(%rax,%rdx), %rax', 'leaq (%rax,%rdx), %rax', False),
        ('wrong-round-scale', 'leaq -1(%rax,%rdx), %rax', 'leaq -1(%rax,%rdx,2), %rax', False),
        ('negate32', 'negq %rdx', 'negl %edx', False),
        ('wrong-mask', 'andq %rdx, %rax', 'andq $-3, %rax', False),
        ('wrong-index-offset', 'subq $72, %rdx', 'subq $71, %rdx', False),
        ('large-index-offset', 'subq $72, %rdx', 'subq $2147483647, %rdx', False),
        ('lower-clamp-neighbor', 'movl $524288, %eax', 'movl $524287, %eax', True),
        ('upper-clamp-neighbor', 'movl $524288, %eax', 'movl $524289, %eax', False),
        ('clamp-compare32', 'cmpq $524288, %rax', 'cmpl $524288, %eax', False),
    ]:
        mutation(name, old, new, expected)
    load, block, slot = 'movq (%rdx,%rax,8), %rcx', 'movq %rcx, (%r8)', 'movq %r8, (%rdx,%rax,8)'
    for at, line in enumerate(['movl (%rdx,%rax,8), %ecx', 'movq 4(%rdx,%rax,8), %rcx', 'movq (%rdx,%rax,4), %rcx', 'movq (%rdx,%rsi,8), %rcx', 'movq (%r8), %rcx', 'movq (%rcx,%rax,8), %rcx', 'movq 2147483647(%rdx,%rax,8), %rcx']):
        mutation('wrong-load-' + str(at), load, line, False)
    for at, line in enumerate(['', 'movl %ecx, (%r8)', 'movq %rax, (%r8)', 'movq %rcx, 4(%r8)', 'movq %rcx, (%rsp)', block + '\n' + block]):
        mutation('wrong-block-store-' + str(at), block, line, False)
    for at, line in enumerate(['', 'movl %r8d, (%rdx,%rax,8)', 'movq %rcx, (%rdx,%rax,8)', 'movq %r8, 8(%rdx,%rax,8)', 'movq %r8, (%rdx,%rax,4)', slot + '\n' + slot]):
        mutation('wrong-slot-store-' + str(at), slot, line, False)
    for name, old, new, expected in [('reload-block', block, block + '\nmovq (%r8), %rcx', True), ('reload-slot-wrong-register', slot, slot + '\nmovq (%rdx,%rax,8), %rcx', False), ('reload-slot-block-register', slot, slot + '\nmovq (%rdx,%rax,8), %r8', True), ('reload-upper-block-word', block, block + '\nmovl 4(%r8), %ecx', False), ('wrong-table', 'leaq __fern_large_freelist(%rip), %rdx', 'leaq __fern_small_freelist(%rip), %rdx', False)]:
        mutation(name, old, new, expected)
    for at, reg in enumerate(DECODER.REG64):
        mutation('identity64-' + str(at), 'ret\n', f'movq {reg}, {reg}\nret\n', True)
        mutation('identity32-' + str(at), 'ret\n', f'movl {DECODER.REG32[at]}, {DECODER.REG32[at]}\nret\n', False)
        mutation('wrong-register-' + str(at), 'ret\n', f'movq {DECODER.REG64[(at + 1) % 16]}, {reg}\nret\n', False)
    for at, line in enumerate(['ret $8', 'movq (%rsp), %rsp\nret', 'movq %rax, (%rsp)\nret', 'movq %rsp, %r8\nret', 'movq 8(%rsp), %rax\nret', 'jmp .Llpush_ret', 'unknown_instruction\nret', 'call __fern_capw\nret', '__other:\nret']):
        mutation('invalid-return-' + str(at), 'ret\n', line + '\n', False)
    for at, prefix in enumerate(['movq (%rsp), %rcx\n', 'movl 4(%rsp), %ecx\n', 'movq %rbx, %rcx\nmovq %rcx, %rbx\n', 'movl %ebx, %ecx\nmovq %rcx, %rbx\n']):
        mutation('safe-read-or-copy-' + str(at), load, prefix + load, at != 3)
    cmp = 'cmpq %rax, %rsi'
    for name, old, new, expected in [
        ('guard32', cmp, 'cmpl %eax, %esi', False),
        ('guard-limit-below', 'movabs $17179869184, %rax', 'movabs $17179869183, %rax', False),
        ('guard-limit-above', 'movabs $17179869184, %rax', 'movabs $17179869185, %rax', False),
        ('guard-inclusive', 'ja .Llpush_ret', 'jae .Llpush_ret', False),
        ('guard-alias', 'ja .Llpush_ret', 'jnbe .Llpush_ret', True),
        ('guard-signed', 'ja .Llpush_ret', 'jg .Llpush_ret', False),
        ('guard-overflow', 'ja .Llpush_ret', 'jo .Llpush_ret', False),
        ('guard-size-truncate', cmp, 'movl %esi, %esi\n' + cmp, False),
        ('guard-zero-shift', cmp, 'shlq $0, %rsi\n' + cmp, True),
        ('guard-masked-zero-shift', cmp, 'shlq $64, %rsi\n' + cmp, True),
        ('guard-truncated-zero-shift', cmp, 'shll $32, %esi\n' + cmp, False),
        ('guard-flags-preserved', cmp, cmp + '\nshlq $64, %rax', True),
        ('guard-flags-clobbered', cmp, cmp + '\naddq $0, %rax', False),
        ('clamp-alias', 'jae .L2', 'jnb .L2', True),
        ('clamp-signed', 'jae .L2', 'jge .L2', False),
        ('clamp-equal-only', 'jae .L2', 'je .L2', False),
        ('clamp-flags-clobbered', 'cmpq $524288, %rax', 'cmpq $524288, %rax\nshlq $1, %rax\nshrq $1, %rax', False),
    ]:
        mutation(name, old, new, expected)
    add('both-memory-scales-wrong', helper.replace(',8)', ',4)'), False)
    add('wrong-oversized-rax', helper.replace('ja .Llpush_ret', 'ja .Loversized') + '.Loversized:\nmovabs $0, %rax\nret\n', False)
    for at, (low, high) in enumerate(INTERVALS):
        prefix = f'movabs ${low}, %rax\ncmpq %rax, %rsi\nje .Lmissing_effects\n'
        add('partition-witness-' + str(at), prefix + helper + '.Lmissing_effects:\nret\n', False)
    rng = random.Random(SEED)
    for at in range(128):
        body = helper.replace('.Llpush_ret', '.Lreturn_' + str(rng.getrandbits(32))).replace('.L2', '.Lclamp_' + str(rng.getrandbits(32)))
        reg = DECODER.REG64[rng.randrange(16)]
        body = body.replace('\nret\n', f'\nmovq {reg}, {reg}\nret\n')
        add('seeded-label-identity-' + str(at), body, True)
    source = DECODER.envelope(helper)
    for at, bad in enumerate(['.set alias, __fern_large_push\n' + source, source + '.data\n__fern_large_push:\n.quad 0\n', source + '.include "hidden.s"\n', source.replace('.Llpush_ret:', '.Llpush_ret: ret'), source.replace('ja .Llpush_ret', 'ja .Loutside')]):
        add('namespace-' + str(at), None, False, source=bad)
    return dict(schema=1, seed=SEED, criterion='Exact ABI proof results over all 77 normal partitions and the complete oversized predicate', fixture_sha256=sha(FIXTURE.read_bytes()), legacy_source_text=legacy, legacy_source_sha256=LEGACY_SHA256, cases=cases)


def generate():
    reference = catalog()
    raw = (json.dumps(reference, sort_keys=True, separators=(',', ':')) + '\n').encode()
    return reference, gzip.compress(raw, mtime=0)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--write', action='store_true')
    parser.add_argument('--check', action='store_true')
    options = parser.parse_args()
    ref, data = generate()
    if options.write:
        rel = str(REFERENCE.relative_to(ROOT))
        if REFERENCE.exists() or subprocess.run(['git', 'cat-file', '-e', 'HEAD:' + rel], cwd=ROOT, stderr=subprocess.DEVNULL).returncode == 0 or subprocess.check_output(['git', 'ls-files', '--', rel], cwd=ROOT):
            raise SystemExit('Refuse existing reference output')
        REFERENCE.write_bytes(data)
    if options.check:
        assert REFERENCE.read_bytes() == data, 'reference reproduction differs'
    print(json.dumps(dict(cases=len(ref['cases']), positive=sum(c['expected'] for c in ref['cases']), negative=sum(not c['expected'] for c in ref['cases']), bundle_sha256=sha(data))))


if __name__ == '__main__':
    main()
