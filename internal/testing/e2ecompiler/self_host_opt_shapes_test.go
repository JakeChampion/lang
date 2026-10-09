package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// Optimisations that compute the right answer whether or not they fire, so no
// runtime test notices when one stops: each case names a function and the
// shape its emitted code must (and must not) have on each native target. The
// program's exit code is checked too, on the host target.
type optShapeCase struct {
	name string
	src  string
	fn   string
	exit int
	// want and forbid are regexps over the function's body, keyed by target.
	want   map[string][]string
	forbid map[string][]string
}

var optShapeCases = []optShapeCase{
	// `while (i < xs.len())` with `i` counting up from 0 reads in bounds.
	{name: "bce_while_len", fn: "sum_while", exit: 39, src: `
@noinline function sum_while(xs: i32[]): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s;
}
function main(): i32 { return sum_while([3, 5, 7, 11, 13]); }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_oob_abort`}, "arm64-linux": {`__fern_oob_abort`}}},
	// A for-in loop's index is below the length by construction.
	{name: "bce_for_in", fn: "sum_for", exit: 39, src: `
@noinline function sum_for(xs: i64[]): i64 {
    let s: i64 = 0;
    for x in xs { s = s + x; }
    return s;
}
function main(): i32 { return sum_for([3, 5, 7, 11, 13]) as i32; }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_oob_abort`}, "arm64-linux": {`__fern_oob_abort`}}},
	// The same two shapes over a string's bytes.
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
		forbid: map[string][]string{"x86-64-linux": {`__fern_oob_abort`}, "arm64-linux": {`__fern_oob_abort`}}},
	// A loop is rotated: the back edge re-runs the header's test and branches
	// to the body, so no unconditional branch is left in the loop.
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
	// A uniqueness test whose answer only chooses a branch is fused into it:
	// the guards meet at a `_uq` join and the branch reads the flags, with no
	// 0/1 built and copied into a scratch to be tested again. Reuse, and so
	// the test, is the typed lowering's.
	// A string literal's static box is immortal: comparing against one
	// releases nothing afterwards.
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
	// A literal a consumer takes while it stays live is handed on without a
	// retain.
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
	// A phi of literals is a literal too: handed on while it stays live, it
	// is not retained.
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
	// A phi that can also carry a counted string is not a literal: handed on
	// while live, it is retained.
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
	// A phi of literals holds nothing, so its death releases nothing.
	{name: "literal_phi_no_release", fn: "pick", exit: 3, src: `
@noinline function pick(c: boolean): i32 {
    let s: string = "ab";
    if (c) { s = "abc"; }
    return s.len();
}
function main(): i32 { return pick(true); }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_str_free`}, "arm64-linux": {`__fern_str_free`}}},
	// An array release whose count survives it is decremented in place; only
	// the free calls __fern_arr_dec.
	{name: "release_inline", fn: "grow", exit: 7, src: `
@noinline function grow(xs: i32[]): i32 {
    let ys: i32[] = xs.append(4);
    return ys.len() + xs.len();
}
function main(): i32 { return grow([1, 2, 3]); }
`,
		want: map[string][]string{"x86-64-linux": {`rcdecd\d+:\n\s+subl \$1, -8\(`}, "arm64-linux": {`rcdecd\d+:\n\s+sub w5, w5, #1`}}},
	// An append to a receiver with a free slot that it solely owns stores in
	// place; only the grow or a shared receiver calls the helper.
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
	// An inline retain tests and bumps the count in memory rather than
	// round-tripping it through %ecx.
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
	// A runtime call's arguments move straight into %rdi and %rsi, not
	// through %r11 and %rcx first.
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
	// With the callee-saved registers full, the value a loop reads and writes
	// every iteration keeps its register and a cold one spills, though the
	// loop value lives longer. The x86 add reads both saved registers in one
	// lea before the call, without reloading either loop value from the frame.
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
	// A block of values each read a few times across a long call-free loop body
	// gives up its registers before the short temporaries between them: a
	// shift's result is used at once, so spilling it costs a store and a load
	// for almost no span freed (#10615).
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
	// `(x >> n) | (x << (W - n))` is one rotate at either width, and a pair of
	// shifts that is not one (a signed right shift, counts not summing to the
	// width) keeps its shifts.
	{name: "rotate_u32", fn: "rot32", exit: 170, src: rotateShapesSrc,
		want:   map[string][]string{"x86-64-linux": {`rorl \$7, %e\w+`}, "arm64-linux": {`ror w\d+, w\d+, #7\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bsh[lr]q\b`}, "arm64-linux": {`\bls[lr]\b`}}},
	{name: "rotate_u64", fn: "rot64", exit: 170, src: rotateShapesSrc,
		want:   map[string][]string{"x86-64-linux": {`rorq \$51, %r\w+`}, "arm64-linux": {`ror x\d+, x\d+, #51\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bsh[lr]q\b`}, "arm64-linux": {`\bls[lr]\b`}}},
	{name: "rotate_lookalikes_kept", fn: "notrot", exit: 170, src: rotateShapesSrc,
		want:   map[string][]string{"x86-64-linux": {`shlq \$24,`, `sarq \$3,`}, "arm64-linux": {`lsl x\d+, x\d+, #24`, `asr x\d+, x\d+, #3`}},
		forbid: map[string][]string{"x86-64-linux": {`\bror`}, "arm64-linux": {`\bror\b`}}},
	// A little-endian word built from bytes, as std/crypto reads its message
	// words, is one load of the word, from an owned array or a view (#8782).
	// A big-endian word is not, and keeps its shifts.
	{name: "le_word_owned_u32", fn: "word32", exit: 95, src: wordLoadSrc,
		forbid: map[string][]string{"x86-64-linux": {`\bshl`}, "arm64-linux": {`\blsl\b`}}},
	{name: "le_word_view_u64", fn: "word64", exit: 95, src: wordLoadSrc,
		forbid: map[string][]string{"x86-64-linux": {`\bshl`, `\bmovzb`}, "arm64-linux": {`\blsl\b`, `\bldrb\b`}}},
	{name: "be_word_kept", fn: "bigend", exit: 95, src: wordLoadSrc,
		want: map[string][]string{"x86-64-linux": {`\bshl`}, "arm64-linux": {`\blsl\b`}}},
	// A rotate whose operand is written out twice, as std/crypto's md5 rounds
	// spell it: the two copies merge, so it is one sum and one rotate. Halves
	// over different sums stay two shifts.
	{name: "rotate_of_repeated_operand", fn: "rotdup", exit: 47, src: mergedRotateSrc,
		want:   map[string][]string{"x86-64-linux": {`rorl \$25, %e\w+`}, "arm64-linux": {`ror w\d+, w\d+, #25\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bsh[lr]q\b`, `(?s)\badd[lq]\b.*\badd[lq]\b`}, "arm64-linux": {`\bls[lr]\b`, `(?s)\badd\b.*\badd\b`}}},
	{name: "rotate_of_different_operands_kept", fn: "notdup", exit: 47, src: mergedRotateSrc,
		want:   map[string][]string{"x86-64-linux": {`shrq \$25,`, `shlq \$7,`}, "arm64-linux": {`lsr \w+, \w+, #25`, `lsl \w+, \w+, #7`}},
		forbid: map[string][]string{"x86-64-linux": {`\bror`}, "arm64-linux": {`\bror\b`}}},
	// Spilled values whose lifetimes do not meet share a frame slot: the
	// second phase's spills reuse the first phase's slots.
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
	// A branch on a boolean tests the register the boolean lives in.
	{name: "value_test_in_place", fn: "pick", exit: 7, src: `
@noinline function pick(b: boolean, x: i32): i32 { if (b) { return x; } return 0; }
function main(): i32 { return pick(true, 7) + pick(false, 9); }
`,
		want:   map[string][]string{"x86-64-linux": {`testq (%r\w+), (%r\w+)`}, "arm64-linux": {`\bcbn?z x\d+,`}},
		forbid: map[string][]string{"x86-64-linux": {`testq %r11, %r11`, `movq %r\w+, %r11`}, "arm64-linux": {`\bcbn?z x4,`, `mov x4, x`}}},
	// A constant a phi merges is loaded on its edge, with no home of its own,
	// so the `false` the first compare merges does not hold a callee-saved
	// register across the call between; only the string the second compare
	// reads does. A returned constant goes straight to the result register.
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
	// A multiply by a constant reads its operand from its home: x86-64's
	// three-operand imul, with no copy into the destination first, whichever
	// side the constant is written on. The forbid is the half that tells it
	// from main, and it holds only while each product has a register home.
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
	// A 32-bit value read only by further 32-bit arithmetic keeps no sign
	// extension: the product feeds the sum without one, and the sum is
	// extended once, where the comparison and the division read it. The roll
	// overflows on every byte, so the exit code checks the wrap still lands.
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
	// A loop counter stepped by one under `i < n` cannot overflow, so its
	// step keeps no sign extension even though the test reads it whole.
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
	// Under `<=` the step can pass INT32_MAX, so it keeps its wrap, and the
	// counter still turns negative where it overflows.
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
	// A body the header's other test also enters is not below `n` on every
	// path, so its step keeps the wrap: the first test's successor does not
	// dominate it.
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
	// A multiply by a power of two is a shift.
	{name: "strength_mul_pow2", fn: "times8", exit: 40, src: `
@noinline function times8(x: i32): i32 { return x * 8; }
function main(): i32 { return times8(5); }
`,
		want:   map[string][]string{"x86-64-linux": {`\bshl[lq]? \$3,`}, "arm64-linux": {`\blsl x\d+, x\d+, #3\b`}},
		forbid: map[string][]string{"x86-64-linux": {`\bimul`}, "arm64-linux": {`\bmul\b`}}},
	// A direct call passes its arguments in registers to the callee's `.r`
	// entry: no push, no pop, and the callee reads no parameter from memory.
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
	// A tiny call-free function is spliced into its callers; @noinline keeps
	// the call.
	{name: "inline_tiny_leaf", fn: "caller", exit: 33, src: `
function sq(x: i32): i32 { return x * x; }
@noinline function cube(x: i32): i32 { return x * x * x; }
@noinline function caller(v: i32): i32 { return sq(v) + sq(v + 1) + cube(v - 1); }
function main(): i32 { return caller(3); }
`,
		want:   map[string][]string{"x86-64-linux": {`call __fn_cube\b`}, "arm64-linux": {`bl __fn_cube\b`}},
		forbid: map[string][]string{"x86-64-linux": {`__fn_sq\b`}, "arm64-linux": {`__fn_sq\b`}}},
	// An i32 result's wrap is one sign-extension on the value's own register,
	// not a round trip through the scratch.
	{name: "wrap_in_place", fn: "add3", exit: 12, src: `
@noinline function add3(a: i32, b: i32, c: i32): i32 { return a + b + c; }
function main(): i32 { return add3(3, 4, 5); }
`,
		want:   map[string][]string{"x86-64-linux": {`\bmovslq %(\w+)d?, %\w+`}, "arm64-linux": {`\bsxtw x(\d+), w\d+`}},
		forbid: map[string][]string{"arm64-linux": {`\bsxtw x4, w4\b`}}},
	// A wrap whose operand sits in another register reads it there: one
	// extension into the destination, not a copy and an extension in place.
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
function main(): i32 {
    let b: u8[] = [];
    let i: i32 = 0;
    while (i < 24) { b = b.append(((i * 29 + 3) & 255) as u8); i = i + 1; }
    let x: u32 = word32(b, 5) ^ (word64(b, 9) >> 24u64) as u32 ^ bigend(b, 2);
    return (x & 127u32) as i32;
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
