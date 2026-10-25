package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

type optShapeCase struct {
	name   string
	src    string
	fn     string
	exit   int
	want   map[string][]string
	forbid map[string][]string
}

var optShapeCases = []optShapeCase{
	{name: "shift_in_operand_home", fn: "pack", exit: 38, src: `
@noinline function pack(a: u64, b: u64, c: u64): u64 {
    return (a & 255u64) << 16 | (b & 255u64) << 8 | c;
}
function main(): i32 { return (pack(1u64, 2u64, 3u64) % 251u64) as i32; }
`,
		forbid: map[string][]string{"x86-64-linux": {`movq %r\w+, %r\w+\s+shlq`}}},
	{name: "bce_while_len", fn: "sum_while", exit: 39, src: `
@noinline function sum_while(xs: i32[]): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s;
}
function main(): i32 { return sum_while([3, 5, 7, 11, 13]); }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_(str_)?oob_abort`}, "arm64-linux": {`__fern_(str_)?oob_abort`}}},
	{name: "bce_for_in", fn: "sum_for", exit: 39, src: `
@noinline function sum_for(xs: i64[]): i64 {
    let s: i64 = 0;
    for x in xs { s = s + x; }
    return s;
}
function main(): i32 { return sum_for([3, 5, 7, 11, 13]) as i32; }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_(str_)?oob_abort`}, "arm64-linux": {`__fern_(str_)?oob_abort`}}},
	{name: "bce_string", fn: "count_a", exit: 5, src: `
@noinline function count_a(s: string): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < s.len()) { if (s[i] == 97u8) { n = n + 1; } i = i + 1; }
    for c in s { if (c == 97u8) { n = n + 1; } }
    return n;
}
function main(): i32 { return count_a("banana") - 1; }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_(str_)?oob_abort`}, "arm64-linux": {`__fern_(str_)?oob_abort`}}},
	{name: "loop_rotation", fn: "sum_to", exit: 45, src: `
@noinline function sum_to(n: i64): i64 {
    let s: i64 = 0i64;
    let i: i64 = 0i64;
    while (i < n) { s = s + i; i = i + 1i64; }
    return s;
}
function main(): i32 { return sum_to(10i64) as i32; }
`,
		forbid: map[string][]string{"x86-64-linux": {`\bjmp\b`}, "arm64-linux": {`\bb \.L`}}},
	{name: "literal_compare_no_release", fn: "sym", exit: 5, src: `
@noinline function sym(k: string): i32 {
    if (k == "add") { return 1; }
    if (k == "sub") { return 2; }
    return 3;
}
function main(): i32 { return sym("sub") + sym("x"); }
`,
		want:   map[string][]string{"x86-64-linux": {`__fern_str_eq`}, "arm64-linux": {`__fern_str_eq`}},
		forbid: map[string][]string{"x86-64-linux": {`__fern_str_free`}, "arm64-linux": {`__fern_str_free`}}},
	{name: "literal_consumed_no_retain", fn: "pair", exit: 2, src: `
@noinline function pair(): string[] {
    let s: string = "x";
    let out: string[] = [];
    out = out.append(s);
    out = out.append(s);
    return out;
}
function main(): i32 { return pair().len(); }
`,
		forbid: map[string][]string{"x86-64-linux": {`rc_?inc`}, "arm64-linux": {`rc_?inc`}}},
	{name: "literal_phi_consumed_no_retain", fn: "pick2", exit: 2, src: `
@noinline function pick2(c: boolean): string[] {
    let s: string = "ab";
    if (c) { s = "abc"; }
    let out: string[] = [];
    out = out.append(s);
    out = out.append(s);
    return out;
}
function main(): i32 { return pick2(true).len(); }
`,
		forbid: map[string][]string{"x86-64-linux": {`rc_?inc`}, "arm64-linux": {`rc_?inc`}}},
	{name: "mixed_phi_consumed_retained", fn: "pick3", exit: 2, src: `
@noinline function pick3(c: boolean, t: string): string[] {
    let s: string = "a";
    if (c) { s = t + t; }
    let out: string[] = [];
    out = out.append(s);
    out = out.append(s);
    return out;
}
function main(): i32 { return pick3(true, "b").len(); }
`,
		want: map[string][]string{"x86-64-linux": {`rc_?inc`}, "arm64-linux": {`rc_?inc`}}},
	{name: "literal_phi_no_release", fn: "pick", exit: 3, src: `
@noinline function pick(c: boolean): i32 {
    let s: string = "ab";
    if (c) { s = "abc"; }
    return s.len();
}
function main(): i32 { return pick(true); }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_str_free`}, "arm64-linux": {`__fern_str_free`}}},
	{name: "release_inline", fn: "grow", exit: 7, src: `
@noinline function grow(xs: i32[]): i32 {
    let ys: i32[] = xs.append(4);
    return ys.len() + xs.len();
}
function main(): i32 { return grow([1, 2, 3]); }
`,
		want: map[string][]string{"x86-64-linux": {`rcdecd\d+:\n\s+subl \$1, -8\(`}, "arm64-linux": {`rcdecd\d+:\n\s+sub w5, w5, #1`}}},
	{name: "push_inline", fn: "fill", exit: 3, src: `
@noinline function fill(n: i32): i32[] {
    let out: i32[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(i); i = i + 1; }
    return out;
}
function main(): i32 { return fill(3).len(); }
`,
		want: map[string][]string{"x86-64-linux": {`apush\d+o:\n\s+movq %rsi, 8\(%rdi,%rdx,8\)`}, "arm64-linux": {`apush\d+o:\n\s+add x2, x2, #1\n\s+str x1, \[x0, x2, lsl #3\]`}}},
	{name: "unique_test_fused", fn: "bump", exit: 11, src: `
struct Pt { x: i32, y: i32, tag: string }
@noinline function bump(p: Pt): Pt { return Pt { ...p, x: p.x + 1 }; }
function main(): i32 {
    let p: Pt = Pt { x: 1, y: 2, tag: "a" };
    let i: i32 = 0;
    while (i < 10) { p = bump(p); i = i + 1; }
    return p.x;
}
`,
		want:   map[string][]string{"x86-64-linux": {`_uq\d+:`, `cmpl \$1, -8\(%\w+\)`}, "arm64-linux": {`_uq\d+:`}},
		forbid: map[string][]string{"x86-64-linux": {`_rcuniq\d+:`, `testq %r11, %r11`, `movl -8\(%\w+\), %edx`}, "arm64-linux": {`_rcuniq\d+:`, `mov x6, #1`}}},
	{name: "closure_call_unchecked", fn: "apply", exit: 8, src: `
@noinline function apply(f: (i32) => i32, x: i32): i32 { return f(x) + 1; }
function main(): i32 {
    let k: i32 = 4;
    return apply((y: i32) => y + k, 3);
}
`,
		want:   map[string][]string{"x86-64-linux": {`call \*8\(%rax\)`}, "arm64-linux": {`blr x\d+`}},
		forbid: map[string][]string{"x86-64-linux": {`__fern_(str_)?oob_abort`}, "arm64-linux": {`__fern_(str_)?oob_abort`}}},
	{name: "drop_variants_chained", fn: "__sem_drop_Tok", exit: 4, src: `
enum Tok { Word(string), Num(string, i32), Punct(string), Gap(string, string) }
@noinline function make(i: i32): Tok {
    if (i == 0) { return Word("w"); }
    if (i == 1) { return Num("n", 1); }
    if (i == 2) { return Punct("p"); }
    return Gap("a", "b");
}
function main(): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { let t: Tok = make(i); n = n + 1; i = i + 1; }
    return n;
}
`,
		want: map[string][]string{"x86-64-linux": {`movq 0\(%r\w+\), %r\w+`}, "arm64-linux": {`ldr x\d+, \[x\d+, #0\]`}},
		forbid: map[string][]string{
			"x86-64-linux": {`(?s)movq 0\(%r\w+\), %r\w+.*movq 0\(%r\w+\), %r\w+`},
			"arm64-linux":  {`(?s)ldr x\d+, \[x\d+, #0\].*ldr x\d+, \[x\d+, #0\]`}}},
	{name: "rc_inc_in_memory", fn: "twice", exit: 5, src: `
struct Two { a: string, b: string }
@noinline function twice(s: string): Two { return Two { a: s, b: s }; }
function main(): i32 {
    let t: Two = twice("hi");
    return t.a.len() + t.b.len() + 1;
}
`,
		want:   map[string][]string{"x86-64-linux": {`cmpl \$0, -8\(%\w+\)\n\s+js `, `addl \$1, -8\(%\w+\)`}},
		forbid: map[string][]string{"x86-64-linux": {`addl \$1, %ecx`, `movl -8\(%\w+\), %ecx`}}},
	{name: "rt_call_args_direct", fn: "fill", exit: 37, src: `
@noinline function fill(n: i32): i32[] {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i * 3); i = i + 1; }
    return xs;
}
function main(): i32 { let xs: i32[] = fill(10); return xs[9] + xs.len(); }
`,
		want:   map[string][]string{"x86-64-linux": {`call __fern_arr_push`}},
		forbid: map[string][]string{"x86-64-linux": {`movq %r11, %rdi`, `movq %rcx, %rsi`}}},
	{name: "spill_the_cold_value", fn: "hot", exit: 56, src: `
@noinline function g(x: i64): i64 { return x + 1i64; }
@noinline function hot(n: i64): i64 {
    let c1: i64 = g(n); let c2: i64 = g(c1); let c3: i64 = g(c2); let c4: i64 = g(c3);
    let c5: i64 = g(c4); let c6: i64 = g(c5); let c7: i64 = g(c6); let c8: i64 = g(c7);
    let c9: i64 = g(c8); let c10: i64 = g(c9); let c11: i64 = g(c10); let c12: i64 = g(c11);
    let k: i64 = g(0i64);
    let i: i64 = 0i64;
    while (i < n) { k = g(k + i); i = i + 1i64; }
    let t: i64 = g(c1 + c2 + c3 + c4 + c5 + c6 + c7 + c8 + c9 + c10 + c11 + c12);
    return g(k) + t;
}
function main(): i32 { return (hot(5i64) % 100i64) as i32; }
`,
		want: map[string][]string{
			"x86-64-linux": {`leaq \(%r(?:bx|1[2-5]),%r(?:bx|1[2-5])\), %rax\n\s+call __fn_g\.r`},
			"arm64-linux":  {`add x0, x(?:19|2\d), x(?:19|2\d)\n\s+bl __fn_g\.r`}},
		forbid: map[string][]string{
			"x86-64-linux": {`movq %rax, -\d+\(%rbp\)\n\s+cmpq`},
			"arm64-linux":  {`str x0, \[sp, #\d+\]\n\s+cmp `}}},
	{name: "spill_the_long_value", fn: "mix", exit: 49, src: `
@noinline function mix(xs: i64[], n: i32): i64 {
    let h: i64 = 0i64;
    let i: i32 = 0;
    while (i < n) {
        let w0: i64 = xs[0]; let w1: i64 = xs[1]; let w2: i64 = xs[2]; let w3: i64 = xs[3];
        let w4: i64 = xs[4]; let w5: i64 = xs[5]; let w6: i64 = xs[6]; let w7: i64 = xs[7];
        let w8: i64 = xs[8]; let w9: i64 = xs[9]; let w10: i64 = xs[10]; let w11: i64 = xs[11];
        h = (h ^ (h >> 7i64)) + w0 + ((h << 3i64) ^ w11);
        h = (h ^ (h >> 5i64)) + w1 + ((h << 2i64) ^ w10);
        h = (h ^ (h >> 3i64)) + w2 + ((h << 4i64) ^ w9);
        h = (h ^ (h >> 9i64)) + w3 + ((h << 1i64) ^ w8);
        h = (h ^ (h >> 7i64)) + w4 + ((h << 3i64) ^ w7);
        h = (h ^ (h >> 5i64)) + w5 + ((h << 2i64) ^ w6);
        h = (h ^ (h >> 3i64)) + w6 + ((h << 4i64) ^ w5);
        h = (h ^ (h >> 9i64)) + w7 + ((h << 1i64) ^ w4);
        h = (h ^ (h >> 7i64)) + w8 + ((h << 3i64) ^ w3);
        h = (h ^ (h >> 5i64)) + w9 + ((h << 2i64) ^ w2);
        h = (h ^ (h >> 3i64)) + w10 + ((h << 4i64) ^ w1);
        h = (h ^ (h >> 9i64)) + w11 + ((h << 1i64) ^ w0);
        i = i + 1;
    }
    return h;
}
function main(): i32 {
    let xs: i64[] = [1i64, 2i64, 3i64, 4i64, 5i64, 6i64, 7i64, 8i64, 9i64, 10i64, 11i64, 12i64];
    return (((mix(xs, 1000) % 100i64) + 100i64) % 100i64) as i32;
}
`,
		want:   map[string][]string{"x86-64-linux": {`sarq \$7, %\w+\n\s+xorq`}},
		forbid: map[string][]string{"x86-64-linux": {`(?:sar|shl)q \$\d+, %\w+\n\s+movq %\w+, -\d+\(%rbp\)`}}},
	{name: "rotate_u32", fn: "rot32", exit: 170, src: rotateShapesSrc,
		want:   map[string][]string{"x86-64-linux": {`rorl \$7, %e\w+`}, "arm64-linux": {`ror w\d+, w\d+, #7\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bsh[lr]q\b`}, "arm64-linux": {`\bls[lr]\b`}}},
	{name: "rotate_u64", fn: "rot64", exit: 170, src: rotateShapesSrc,
		want:   map[string][]string{"x86-64-linux": {`rorq \$51, %r\w+`}, "arm64-linux": {`ror x\d+, x\d+, #51\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bsh[lr]q\b`}, "arm64-linux": {`\bls[lr]\b`}}},
	{name: "rotate_lookalikes_kept", fn: "notrot", exit: 170, src: rotateShapesSrc,
		want:   map[string][]string{"x86-64-linux": {`shlq \$24,`, `sarq \$3,`}, "arm64-linux": {`lsl x\d+, x\d+, #24`, `asr x\d+, x\d+, #3`}},
		forbid: map[string][]string{"x86-64-linux": {`\bror`}, "arm64-linux": {`\bror\b`}}},
	{name: "le_word_owned_u32", fn: "word32", exit: 5, src: wordLoadSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "le_word_view_u64", fn: "word64", exit: 5, src: wordLoadSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "be_word_owned_u32", fn: "bigend", exit: 5, src: wordLoadSrc,
		want:   map[string][]string{"x86-64-linux": {`\bbswapl %e\w+`, `__fern_(str_)?oob_abort`}, "arm64-linux": {`\brev w\d+, w\d+`, `__fern_(str_)?oob_abort`}},
		forbid: wordForbid},
	{name: "be_word_view_u64", fn: "bigend64", exit: 5, src: wordLoadSrc,
		want:   map[string][]string{"x86-64-linux": {`\bbswapq %r\w+`, `__fern_(str_)?oob_abort`}, "arm64-linux": {`\brev x\d+, x\d+`, `__fern_(str_)?oob_abort`}},
		forbid: wordForbid},
	{name: "and_mask_branch_is_one_test", fn: "masked", exit: 66, src: maskTestSrc,
		want:   map[string][]string{"x86-64-linux": {`testq \$256, %r\w+`}},
		forbid: map[string][]string{"x86-64-linux": {`andq \$256`}}},
	{name: "and_mask_loop_header_tests_each_trip", fn: "pad", exit: 66, src: maskTestSrc,
		want:   map[string][]string{"x86-64-linux": {`testq \$15, %r\w+`}},
		forbid: map[string][]string{"x86-64-linux": {`andq \$15`}}},
	{name: "le_word_view_i64", fn: "iword64", exit: 125, src: signedWordSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "le_word_owned_i32", fn: "iword32", exit: 125, src: signedWordSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "le_word_bytes_scrambled", fn: "scrambled", exit: 5, src: wordLoadSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "be_word_constant_indexes", fn: "header", exit: 5, src: wordLoadSrc,
		want:   map[string][]string{"x86-64-linux": {`\bbswapl %e\w+`, `__fern_(str_)?oob_abort`}, "arm64-linux": {`\brev w\d+, w\d+`, `__fern_(str_)?oob_abort`}},
		forbid: wordForbid},
	{name: "le_word_string_u32", fn: "sword32", exit: 110, src: stringWordSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "be_word_string_u64", fn: "sbigend64", exit: 110, src: stringWordSrc,
		want:   map[string][]string{"x86-64-linux": {`\bbswapq %r\w+`, `__fern_(str_)?oob_abort`}, "arm64-linux": {`\brev x\d+, x\d+`, `__fern_(str_)?oob_abort`}},
		forbid: wordForbid},
	{name: "le_word_string_constant_indexes", fn: "sheader", exit: 110, src: stringWordSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "le_word_str_view", fn: "vword32", exit: 110, src: stringWordSrc,
		want:   oneCheck,
		forbid: wordForbid},
	{name: "raw_byte_load_constant_offset", fn: "__fern_str_cmp", exit: 73, src: strCmpSrc,
		want:   map[string][]string{"x86-64-linux": {`\bmovzbl 0\(%r\w+\), %e\w+`}, "arm64-linux": {`\bldrb w\d+, \[x\d+, #0\]`}},
		forbid: map[string][]string{"x86-64-linux": {`\bxorl %e\w+, %e\w+\n\s+movzbl \(`}}},
	{name: "word_bytes_out_of_place_kept", fn: "swapped", exit: 5, src: wordLoadSrc,
		want:   map[string][]string{"x86-64-linux": {`\bmovzb`}, "arm64-linux": {`\bldrb\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bbswap[lq]?\b`}, "arm64-linux": {`\brev\b`}}},
	{name: "rotate_of_repeated_operand", fn: "rotdup", exit: 47, src: mergedRotateSrc,
		want:   map[string][]string{"x86-64-linux": {`rorl \$25, %e\w+`}, "arm64-linux": {`ror w\d+, w\d+, #25\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bsh[lr]q\b`, `(?s)\badd[lq]\b.*\badd[lq]\b`}, "arm64-linux": {`\bls[lr]\b`, `(?s)\badd\b.*\badd\b`}}},
	{name: "rotate_of_different_operands_kept", fn: "notdup", exit: 47, src: mergedRotateSrc,
		want:   map[string][]string{"x86-64-linux": {`shrq \$25,`, `shlq \$7,`}, "arm64-linux": {`lsr \w+, \w+, #25`, `lsl \w+, \w+, #7`}},
		forbid: map[string][]string{"x86-64-linux": {`\bror`}, "arm64-linux": {`\bror\b`}}},
	{name: "u32_wraps_nothing_reads_dropped", fn: "mix32", exit: 10, src: `
@noinline function mix32(a: u32, b: u32, c: u32): u32 {
    let s: u32 = a + b + c;
    return (s >> 7u32 | s << 25u32) ^ (s >> 11u32 | s << 21u32);
}
function main(): i32 { return (mix32(4000000000u32, 3000000000u32, 123456789u32) % 101u32) as i32; }
`,
		want:   map[string][]string{"x86-64-linux": {`rorxl \$7, %e\w+, %e\w+`}, "arm64-linux": {`ror w\d+, w\d+, #7\b`}},
		forbid: map[string][]string{"x86-64-linux": {`movl %(e\w+), %e\w+\n\s+(?:add|ror)`}, "arm64-linux": {`\bmov w\d+, w\d+\n\s+(?:add|ror)`, `\bmov x\d+, x\d+\n\s+ror`}}},
	{name: "spill_slots_shared", fn: "two_phase", exit: 57, src: `
@noinline function g(x: i64): i64 { return x + 1i64; }
@noinline function two_phase(n: i64): i64 {
    let a1: i64 = g(n); let a2: i64 = g(a1); let a3: i64 = g(a2); let a4: i64 = g(a3); let a5: i64 = g(a4);
    let a6: i64 = g(a5); let a7: i64 = g(a6); let a8: i64 = g(a7); let a9: i64 = g(a8); let a10: i64 = g(a9);
    let a11: i64 = g(a10); let a12: i64 = g(a11); let a13: i64 = g(a12);
    let s: i64 = g(a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9 + a10 + a11 + a12 + a13);
    let b1: i64 = g(s); let b2: i64 = g(b1); let b3: i64 = g(b2); let b4: i64 = g(b3); let b5: i64 = g(b4);
    let b6: i64 = g(b5); let b7: i64 = g(b6); let b8: i64 = g(b7); let b9: i64 = g(b8); let b10: i64 = g(b9);
    let b11: i64 = g(b10); let b12: i64 = g(b11); let b13: i64 = g(b12);
    return g(b1 + b2 + b3 + b4 + b5 + b6 + b7 + b8 + b9 + b10 + b11 + b12 + b13);
}
function main(): i32 { return (two_phase(1i64) % 100i64) as i32; }
`,
		want:   map[string][]string{"x86-64-linux": {`-\d+\(%rbp\)`}, "arm64-linux": {`\[sp, #\d+\]`}},
		forbid: map[string][]string{"x86-64-linux": {`-(?:1\d\d)\(%rbp\)`}, "arm64-linux": {`\[sp, #(?:1[6-9]|[2-9]\d)\]`}}},
	{name: "value_test_in_place", fn: "pick", exit: 7, src: `
@noinline function id(x: i32): i32 { return x; }
@noinline function pick(b: boolean, x: i32): i32 { if (b) { return id(x); } return 0; }
function main(): i32 { return pick(true, 7) + pick(false, 9); }
`,
		want:   map[string][]string{"x86-64-linux": {`testq (%r\w+), (%r\w+)`}, "arm64-linux": {`\bcbn?z x\d+,`}},
		forbid: map[string][]string{"x86-64-linux": {`testq %r11, %r11`, `movq %r\w+, %r11`}, "arm64-linux": {`\bcbn?z x4,`, `mov x4, x`}}},
	{name: "phi_constant_loads_on_edge", fn: "is_stream", exit: 1, src: `
enum Ty { Named(string), Other(i32) }
@noinline function is_stream(t: Ty): boolean {
    if let Ty.Named(s) = t {
        return s == "Reader" || s == "Writer";
    }
    return false;
}
function main(): i32 {
    let n: i32 = 0;
    if (is_stream(Ty.Named("Writer"))) { n = n + 1; }
    if (is_stream(Ty.Other(3))) { n = n + 10; }
    if (is_stream(Ty.Named("Wrote!"))) { n = n + 100; }
    return n;
}
`,
		want:   map[string][]string{"x86-64-linux": {`pushq %rbx`, `movl \$1, %eax`}, "arm64-linux": {`str x19, \[sp, #-16\]!`, `mov x0, #1\b`}},
		forbid: map[string][]string{"x86-64-linux": {`pushq %r12`}, "arm64-linux": {`\bx20\b`}}},
	{name: "imul_reads_operand_in_place", fn: "poly", exit: 87, src: `
@noinline function poly(s: string): i32 {
    let h: i32 = 7;
    let i: i32 = 0;
    while (i < s.len()) { h = h * 1000003 + s[i] as i32; i = i + 1; }
    if (h < 0) { h = 0 - h; }
    return h % 101;
}
function main(): i32 { return poly("pack my box with five dozen liquor jugs"); }
`,
		want:   map[string][]string{"x86-64-linux": {`imulq \$1000003, %\w+, %\w+`}},
		forbid: map[string][]string{"x86-64-linux": {`movq %r\w+, %r\w+\n\s+imulq \$1000003`}}},
	{name: "imul_left_constant_reads_operand_in_place", fn: "poly", exit: 87, src: `
@noinline function poly(s: string): i32 {
    let h: i32 = 7;
    let i: i32 = 0;
    while (i < s.len()) { h = 1000003 * h + s[i] as i32; i = i + 1; }
    if (h < 0) { h = 0 - h; }
    return h % 101;
}
function main(): i32 { return poly("pack my box with five dozen liquor jugs"); }
`,
		want:   map[string][]string{"x86-64-linux": {`imulq \$1000003, %\w+, %\w+`}},
		forbid: map[string][]string{"x86-64-linux": {`movq %r\w+, %r\w+\n\s+imulq \$1000003`}}},
	{name: "wrap_dropped_before_low_reader", fn: "roll", exit: 48, src: `
@noinline function roll(s: string): i32 {
    let h: i32 = 7;
    let i: i32 = 0;
    while (i < s.len()) { h = h * 1000003 + s[i] as i32; i = i + 1; }
    if (h < 0) { h = 0 - h; }
    return h % 97;
}
function main(): i32 { return roll("the quick brown fox jumps over the lazy dog"); }
`,
		want:   map[string][]string{"x86-64-linux": {`\bmovslq %\w+, %r\w+`}, "arm64-linux": {`\bsxtw x\d+, w\d+\b`}},
		forbid: map[string][]string{"x86-64-linux": {`imulq \$1000003, %\w+, (%\w+)\n\s+movslq`}, "arm64-linux": {`\bmul (x\d+), x\d+, x\d+\n\s+sxtw`}}},
	{name: "counter_step_unwrapped", fn: "count_odd", exit: 21, src: `
@noinline function count_odd(xs: i32[]): i32 {
    let n: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { if ((xs[i] & 1) == 1) { n = n + xs[i]; } i = i + 1; }
    return n;
}
function main(): i32 { return count_odd([1, 2, 3, 4, 5, 6, 12]) + 12; }
`,
		forbid: map[string][]string{"x86-64-linux": {`addq \$1, %(\w+)\n\s+movslq`}, "arm64-linux": {`\badd (x\d+), x\d+, #1\n\s+sxtw`}}},
	{name: "counter_le_keeps_wrap", fn: "climb", exit: 3, src: `
@noinline function climb(start: i32): i32 {
    let i: i32 = start;
    let steps: i32 = 0;
    while (i <= 2147483647) { i = i + 1; steps = steps + 1; if (i < 0) { return steps; } }
    return 0;
}
function main(): i32 { return climb(2147483645); }
`,
		want: map[string][]string{"x86-64-linux": {`addq \$1, %(\w+)\n\s+movslq`}, "arm64-linux": {`\badd (x\d+), x\d+, #1\n\s+sxtw`}}},
	{name: "counter_two_entries_keeps_wrap", fn: "mix", exit: 65, src: `
@noinline function mix(n: i32): i32 {
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < n || acc < 1000) { acc = acc + i; i = i + 1; }
    return acc % 97;
}
function main(): i32 { return mix(10); }
`,
		want: map[string][]string{"x86-64-linux": {`addq \$1, %(\w+)\n\s+movslq`}, "arm64-linux": {`\badd (x\d+), x\d+, #1\n\s+sxtw`}}},
	{name: "strength_mul_pow2", fn: "times8", exit: 40, src: `
@noinline function times8(x: i32): i32 { return x * 8; }
function main(): i32 { return times8(5); }
`,
		want:   map[string][]string{"x86-64-linux": {`\bshl[lq]? \$3,`}, "arm64-linux": {`\blsl x\d+, x\d+, #3\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bimul`}, "arm64-linux": {`\bmul\b`}}},
	{name: "register_args", fn: "caller", exit: 10, src: `
@noinline function clamp(v: i32, lo: i32, hi: i32): i32 {
    if (v < lo) { return lo; }
    if (v > hi) { return hi; }
    return v;
}
@noinline function caller(v: i32): i32 { return clamp(v, 0, 10) + clamp(v - 30, 0, 10); }
function main(): i32 { return caller(25); }
`,
		want:   map[string][]string{"x86-64-linux": {`call __fn_clamp\.r\n`}},
		forbid: map[string][]string{"x86-64-linux": {`\bpushq %(rax|rsi|rdi|r8|r9|r10)\b`, `addq \$\d+, %rsp`, `\s[1-9]\d*\(%rbp\), %`}}},
	{name: "inline_tiny_leaf", fn: "caller", exit: 33, src: `
function sq(x: i32): i32 { return x * x; }
@noinline function cube(x: i32): i32 { return x * x * x; }
@noinline function caller(v: i32): i32 { return sq(v) + sq(v + 1) + cube(v - 1); }
function main(): i32 { return caller(3); }
`,
		want:   map[string][]string{"x86-64-linux": {`call __fn_cube\b`}, "arm64-linux": {`bl __fn_cube\b`}},
		forbid: map[string][]string{"x86-64-linux": {`__fn_sq\b`}, "arm64-linux": {`__fn_sq\b`}}},
	{name: "wrap_in_place", fn: "add3", exit: 12, src: `
@noinline function add3(a: i32, b: i32, c: i32): i32 { return a + b + c; }
function main(): i32 { return add3(3, 4, 5); }
`,
		want:   map[string][]string{"x86-64-linux": {`\bmovslq %(\w+)d?, %\w+`}, "arm64-linux": {`\bsxtw x(\d+), w\d+`}},
		forbid: map[string][]string{"arm64-linux": {`\bsxtw x4, w4\b`}}},
	{name: "wrap_reads_its_source", fn: "sum", exit: 15, src: `
@noinline function sum(xs: i32[]): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s;
}
function main(): i32 { return sum([3, 5, 7]); }
`,
		want:   map[string][]string{"x86-64-linux": {`\bmovslq %r\w+, %r\w+`}, "arm64-linux": {`\bsxtw x\d+, w\d+`}},
		forbid: map[string][]string{"x86-64-linux": {`movq %r\w+, %r\w+\n\s+movslq`}, "arm64-linux": {`\bmov x\d+, x\d+\n\s+sxtw`}}},
	{name: "match_loads_the_shape_once", fn: "area", exit: 9, src: `
enum Shape { Circle(i32), Square(i32), Tri(i32), Line(i32) }
@noinline function area(s: Shape): i32 {
    match (s) {
        Circle(r) => { return r * 3; },
        Square(w) => { return w * w; },
        Tri(b) => { return b / 2; },
        Line(l) => { return l; }
    }
}
function main(): i32 { return area(Line(7)) + area(Tri(4)); }
`,
		want: map[string][]string{
			"x86-64-linux": {`\.Lssa_area_\d+:\n\s+cmpq \$\.S\d+, %r\w+`},
			"arm64-linux":  {`\.Lssa_area_\d+:\n\s+adrp x\d+, \.S\d+`}},
		forbid: map[string][]string{
			"x86-64-linux": {`(?s)movq 0\(%r\w+\), %r\w+\n\s+cmpq \$\.S.*movq 0\(%r\w+\), %r\w+\n\s+cmpq \$\.S`, `leaq \.S\d+\(%rip\)`},
			"arm64-linux":  {`(?s)ldr x\d+, \[x\d+, #0\]\n\s+adrp x\d+, \.S.*ldr x\d+, \[x\d+, #0\]\n\s+adrp x\d+, \.S`}}},
}

func TestSelfHostOptimisationShapes(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, c := range optShapeCases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				out := filepath.Join(dir, target+".s")
				cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
				if combined, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
				}
				asm, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				body := selfHostFnBody(t, asm, c.fn)
				for _, re := range c.want[target] {
					if !regexp.MustCompile(re).MatchString(body) {
						t.Errorf("%s: %s lacks %q:\n%s", target, c.fn, re, body)
					}
				}
				for _, re := range c.forbid[target] {
					if regexp.MustCompile(re).MatchString(body) {
						t.Errorf("%s: %s still has %q:\n%s", target, c.fn, re, body)
					}
				}
			}
			tg := h.targets[0]
			bin := filepath.Join(dir, "prog.bin")
			cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
			if combined, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("building: %v\n%s", err, combined)
			}
			run := exec.Command(bin)
			if len(tg.runner) > 0 {
				run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != c.exit {
				t.Errorf("exit %d, want %d", got, c.exit)
			}
		})
	}
}

var oneCheck = map[string][]string{"x86-64-linux": {`__fern_(str_)?oob_abort`}, "arm64-linux": {`__fern_(str_)?oob_abort`}}

var wordForbid = map[string][]string{
	"x86-64-linux": {`\bshl`, `\bmovzb`, `(?s)__fern_(str_)?oob_abort.*__fern_(str_)?oob_abort`},
	"arm64-linux":  {`\blsl\b`, `\bldrb\b`, `(?s)__fern_(str_)?oob_abort.*__fern_(str_)?oob_abort`},
}

const wordLoadSrc = `
@noinline function word32(bs: u8[], o: i32): u32 {
    return bs[o] as u32 | bs[o + 1] as u32 << 8 | bs[o + 2] as u32 << 16 | bs[o + 3] as u32 << 24;
}
@noinline function word64(bs: [u8], o: i32): u64 {
    return bs[o] as u64 | bs[o + 1] as u64 << 8 | bs[o + 2] as u64 << 16 | bs[o + 3] as u64 << 24 | bs[o + 4] as u64 << 32 | bs[o + 5] as u64 << 40 | bs[o + 6] as u64 << 48 | bs[o + 7] as u64 << 56;
}
@noinline function bigend(bs: u8[], o: i32): u32 {
    return bs[o] as u32 << 24 | bs[o + 1] as u32 << 16 | bs[o + 2] as u32 << 8 | bs[o + 3] as u32;
}
@noinline function bigend64(bs: [u8], o: i32): u64 {
    return bs[o + 4] as u64 << 56 | bs[o + 5] as u64 << 48 | bs[o + 6] as u64 << 40 | bs[o + 7] as u64 << 32 | bs[o + 8] as u64 << 24 | bs[o + 9] as u64 << 16 | bs[o + 10] as u64 << 8 | bs[o + 11] as u64;
}
@noinline function swapped(bs: [u8], o: i32): u32 {
    return bs[o] as u32 | bs[o + 1] as u32 << 8 | bs[o + 3] as u32 << 16 | bs[o + 2] as u32 << 24;
}
@noinline function scrambled(bs: u8[], o: i32): u32 {
    return bs[o + 2] as u32 << 16 | bs[o] as u32 | bs[o + 3] as u32 << 24 | bs[o + 1] as u32 << 8;
}
@noinline function header(bs: u8[]): u32 {
    return bs[4] as u32 << 24 | bs[5] as u32 << 16 | bs[6] as u32 << 8 | bs[7] as u32;
}
function main(): i32 {
    let b: u8[] = [];
    let i: i32 = 0;
    while (i < 24) { b = b.append(((i * 29 + 3) & 255) as u8); i = i + 1; }
    let x: u32 = word32(b, 5) ^ (word64(b, 9) >> 24u64) as u32 ^ bigend(b, 2);
    let y: u64 = bigend64(b[1:20], 3) ^ swapped(b, 11) as u64;
    let z: u32 = scrambled(b, 7) ^ header(b);
    return ((x ^ (y >> 13u64) as u32 ^ y as u32 ^ z) & 127u32) as i32;
}
`

const maskTestSrc = `
@noinline function masked(x: i32, tag: string): i32 {
    if ((x & 256) != 0) { print(tag); return 5; }
    return 3;
}
@noinline function pad(own xs: u8[]): u8[] {
    while (xs.len() % 16 != 0) { xs = xs.append(7u8); }
    return xs;
}
function main(): i32 {
    let p: u8[] = pad([1u8, 2u8, 3u8]);
    return masked(args().len() * 300, "set") * 10 + p.len();
}
`

const signedWordSrc = `
@noinline function iword64(bs: [u8], o: i32): i64 {
    return bs[o] as i64 | bs[o + 1] as i64 << 8 | bs[o + 2] as i64 << 16 | bs[o + 3] as i64 << 24 | bs[o + 4] as i64 << 32 | bs[o + 5] as i64 << 40 | bs[o + 6] as i64 << 48 | bs[o + 7] as i64 << 56;
}
@noinline function iword32(bs: u8[], o: i32): i32 {
    return bs[o] as i32 | bs[o + 1] as i32 << 8 | bs[o + 2] as i32 << 16 | bs[o + 3] as i32 << 24;
}
function main(): i32 {
    let b: u8[] = [];
    let i: i32 = 0;
    while (i < 24) { b = b.append(((i * 29 + 3) & 255) as u8); i = i + 1; }
    let x: i64 = iword64(b, 9) ^ iword32(b, 5) as i64;
    let y: i32 = iword32(b, 13);
    return ((x ^ x >> 29 ^ y as i64) & 127) as i32;
}
`

const stringWordSrc = `
@noinline function sword32(s: string, o: i32): u32 {
    return s[o] as u32 | s[o + 1] as u32 << 8 | s[o + 2] as u32 << 16 | s[o + 3] as u32 << 24;
}
@noinline function sbigend64(s: string, o: i32): u64 {
    return s[o] as u64 << 56 | s[o + 1] as u64 << 48 | s[o + 2] as u64 << 40 | s[o + 3] as u64 << 32 | s[o + 4] as u64 << 24 | s[o + 5] as u64 << 16 | s[o + 6] as u64 << 8 | s[o + 7] as u64;
}
@noinline function sheader(s: string): u32 {
    return s[4] as u32 | s[5] as u32 << 8 | s[6] as u32 << 16 | s[7] as u32 << 24;
}
@noinline function vword32(v: str, o: i32): u32 {
    return v[o] as u32 | v[o + 1] as u32 << 8 | v[o + 2] as u32 << 16 | v[o + 3] as u32 << 24;
}
function main(): i32 {
    let s: string = "pack my box with five dozen liquor jugs";
    let x: u32 = sword32(s, 5) ^ (sbigend64(s, 11) >> 29u64) as u32 ^ sheader(slice_unchecked(s, 3, 20));
    return ((x ^ vword32(slice_unchecked(s, 8, 30), 4) << 7u32) & 127u32) as i32;
}
`

const strCmpSrc = `
@noinline function order(a: string, b: string, c: string, d: string): i32 {
    let n: i32 = 0;
    if (a < b) { n = n + 1; }
    if (b > c) { n = n + 2; }
    if (d > c) { n = n + 4; }
    return n;
}
function main(): i32 {
    return order("pack my box with five dozen", "pack my box with five dozes", "pa", "pac") * 10 + 3;
}
`

const rotateShapesSrc = `
@noinline function rot32(x: u32): u32 { return (x >> 7u32) | (x << 25u32); }
@noinline function rot64(x: u64): u64 { return (x << 13u64) | (x >> 51u64); }
@noinline function notrot(x: u32, y: i32, z: i64): u32 { return ((x >> 7u32) | (x << 24u32)) ^ (((y >> 3) | (y << 29)) as u32) ^ (((z >> 3i64) | (z << 61i64)) as u32); }
function main(): i32 {
    let a: u32 = rot32(2147483905u32);
    let b: u64 = rot64(81985529216486895u64);
    let c: u32 = notrot(3000000000u32, 0 - 12345, 0i64 - 9876543210i64);
    return ((a % 97u32) as i32) + ((b % 89u64) as i32) + ((c % 83u32) as i32);
}
`

const mergedRotateSrc = `
@noinline function rotdup(a: u32, b: u32): u32 { return (a + b >> 25u32) | (a + b << 7u32); }
@noinline function notdup(a: u32, b: u32, c: u32): u32 { return (a + b >> 25u32) | (a + c << 7u32); }
function main(): i32 {
    let r: u32 = rotdup(2147483905u32, 305419896u32);
    let n: u32 = notdup(2147483905u32, 305419896u32, 19088743u32);
    return ((r % 97u32) as i32) + ((n % 89u32) as i32);
}
`
