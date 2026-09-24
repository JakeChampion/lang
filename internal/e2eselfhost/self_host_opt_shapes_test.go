package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// Optimisations that compute the right answer whether or not they fire, so no
// runtime test notices when one stops: each case names a function and the
// shape its emitted code must (and must not) have on each native target.
// Every case is checked on BOTH lowerings the CLI has — the typed semantic
// path it takes by default and the AST path `FERN_SEM_IR=` selects — since
// an optimisation living in one of them is invisible to the other. The
// program's exit code is checked too, on the host target.
type optShapeCase struct {
	name string
	src  string
	fn   string
	exit int
	// want and forbid are regexps over the function's body, keyed by target.
	want   map[string][]string
	forbid map[string][]string
	// typedOnly names an optimisation of a pass only the typed lowering runs.
	typedOnly bool
}

var optShapeCases = []optShapeCase{
	// `while (i < xs.len())` with `i` counting up from 0 reads in bounds.
	{name: "bce_while_len", fn: "sum_while", exit: 39, src: `
@noinline function sum_while(xs: i32[]): i32 {
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s;
}
function main(): i32 { return sum_while([3, 5, 7, 11, 13]); }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_oob_abort`}, "arm64-linux": {`__fern_oob_abort`}}},
	// A for-in loop's index is below the length by construction.
	{name: "bce_for_in", fn: "sum_for", exit: 39, src: `
@noinline function sum_for(xs: i64[]): i64 {
    var s: i64 = 0;
    for x in xs { s = s + x; }
    return s;
}
function main(): i32 { return sum_for([3, 5, 7, 11, 13]) as i32; }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_oob_abort`}, "arm64-linux": {`__fern_oob_abort`}}},
	// The same two shapes over a string's bytes.
	{name: "bce_string", fn: "count_a", exit: 5, src: `
@noinline function count_a(s: string): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
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
    var s: i64 = 0i64;
    var i: i64 = 0i64;
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
	{name: "literal_compare_no_release", fn: "sym", exit: 5, typedOnly: true, src: `
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
	{name: "literal_consumed_no_retain", fn: "pair", exit: 2, typedOnly: true, src: `
@noinline function pair(): string[] {
    var s: string = "x";
    var out: string[] = [];
    out = out.append(s);
    out = out.append(s);
    return out;
}
function main(): i32 { return pair().len(); }
`,
		forbid: map[string][]string{"x86-64-linux": {`rc_?inc`}, "arm64-linux": {`rc_?inc`}}},
	// A phi of literals is a literal too: handed on while it stays live, it
	// is not retained.
	{name: "literal_phi_consumed_no_retain", fn: "pick2", exit: 2, typedOnly: true, src: `
@noinline function pick2(c: boolean): string[] {
    var s: string = "ab";
    if (c) { s = "abc"; }
    var out: string[] = [];
    out = out.append(s);
    out = out.append(s);
    return out;
}
function main(): i32 { return pick2(true).len(); }
`,
		forbid: map[string][]string{"x86-64-linux": {`rc_?inc`}, "arm64-linux": {`rc_?inc`}}},
	// A phi that can also carry a counted string is not a literal: handed on
	// while live, it is retained.
	{name: "mixed_phi_consumed_retained", fn: "pick3", exit: 2, typedOnly: true, src: `
@noinline function pick3(c: boolean, t: string): string[] {
    var s: string = "a";
    if (c) { s = t + t; }
    var out: string[] = [];
    out = out.append(s);
    out = out.append(s);
    return out;
}
function main(): i32 { return pick3(true, "b").len(); }
`,
		want: map[string][]string{"x86-64-linux": {`rc_?inc`}, "arm64-linux": {`rc_?inc`}}},
	// A phi of literals holds nothing, so its death releases nothing.
	{name: "literal_phi_no_release", fn: "pick", exit: 3, typedOnly: true, src: `
@noinline function pick(c: boolean): i32 {
    var s: string = "ab";
    if (c) { s = "abc"; }
    return s.len();
}
function main(): i32 { return pick(true); }
`,
		forbid: map[string][]string{"x86-64-linux": {`__fern_str_free`}, "arm64-linux": {`__fern_str_free`}}},
	// An array release whose count survives it is decremented in place; only
	// the free calls __fern_arr_dec.
	{name: "release_inline", fn: "grow", exit: 7, typedOnly: true, src: `
@noinline function grow(xs: i32[]): i32 {
    var ys: i32[] = xs.append(4);
    return ys.len() + xs.len();
}
function main(): i32 { return grow([1, 2, 3]); }
`,
		want: map[string][]string{"x86-64-linux": {`rcdecd\d+:\n\s+subl \$1, -8\(`}, "arm64-linux": {`rcdecd\d+:\n\s+sub w5, w5, #1`}}},
	{name: "unique_test_fused", fn: "bump", exit: 11, typedOnly: true, src: `
struct Pt { x: i32, y: i32, tag: string }
@noinline function bump(p: Pt): Pt { return Pt { ...p, x: p.x + 1 }; }
function main(): i32 {
    var p: Pt = Pt { x: 1, y: 2, tag: "a" };
    var i: i32 = 0;
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
    var t: Two = twice("hi");
    return t.a.len() + t.b.len() + 1;
}
`,
		want:   map[string][]string{"x86-64-linux": {`cmpl \$0, -8\(%\w+\)\n\s+js `, `addl \$1, -8\(%\w+\)`}},
		forbid: map[string][]string{"x86-64-linux": {`addl \$1, %ecx`, `movl -8\(%\w+\), %ecx`}}},
	// A runtime call's arguments move straight into %rdi and %rsi, not
	// through %r11 and %rcx first.
	{name: "rt_call_args_direct", fn: "fill", exit: 37, src: `
@noinline function fill(n: i32): i32[] {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i * 3); i = i + 1; }
    return xs;
}
function main(): i32 { var xs: i32[] = fill(10); return xs[9] + xs.len(); }
`,
		want:   map[string][]string{"x86-64-linux": {`call __fern_arr_push`}},
		forbid: map[string][]string{"x86-64-linux": {`movq %r11, %rdi`, `movq %rcx, %rsi`}}},
	// With the callee-saved registers full, the value a loop reads and writes
	// every iteration keeps its register and a cold one spills, though the
	// loop value lives longer.
	{name: "spill_the_cold_value", fn: "hot", exit: 56, src: `
@noinline function g(x: i64): i64 { return x + 1i64; }
@noinline function hot(n: i64): i64 {
    var c1: i64 = g(n); var c2: i64 = g(c1); var c3: i64 = g(c2); var c4: i64 = g(c3);
    var c5: i64 = g(c4); var c6: i64 = g(c5); var c7: i64 = g(c6); var c8: i64 = g(c7);
    var c9: i64 = g(c8); var c10: i64 = g(c9); var c11: i64 = g(c10); var c12: i64 = g(c11);
    var k: i64 = g(0i64);
    var i: i64 = 0i64;
    while (i < n) { k = g(k + i); i = i + 1i64; }
    var t: i64 = g(c1 + c2 + c3 + c4 + c5 + c6 + c7 + c8 + c9 + c10 + c11 + c12);
    return g(k) + t;
}
function main(): i32 { return (hot(5i64) % 100i64) as i32; }
`,
		want: map[string][]string{
			"x86-64-linux": {`movq %r(?:bx|1[2-5]), %rax\n\s+addq %r(?:bx|1[2-5]), %rax\n\s+call __fn_g\.r`},
			"arm64-linux":  {`add x0, x(?:19|2\d), x(?:19|2\d)\n\s+bl __fn_g\.r`}},
		forbid: map[string][]string{
			"x86-64-linux": {`movq %rax, -\d+\(%rbp\)\n\s+cmpq`},
			"arm64-linux":  {`str x0, \[sp, #\d+\]\n\s+cmp `}}},
	// Spilled values whose lifetimes do not meet share a frame slot: the
	// second phase's spills reuse the first phase's slots.
	{name: "spill_slots_shared", fn: "two_phase", exit: 57, src: `
@noinline function g(x: i64): i64 { return x + 1i64; }
@noinline function two_phase(n: i64): i64 {
    var a1: i64 = g(n); var a2: i64 = g(a1); var a3: i64 = g(a2); var a4: i64 = g(a3); var a5: i64 = g(a4);
    var a6: i64 = g(a5); var a7: i64 = g(a6); var a8: i64 = g(a7); var a9: i64 = g(a8); var a10: i64 = g(a9);
    var a11: i64 = g(a10); var a12: i64 = g(a11); var a13: i64 = g(a12);
    var s: i64 = g(a1 + a2 + a3 + a4 + a5 + a6 + a7 + a8 + a9 + a10 + a11 + a12 + a13);
    var b1: i64 = g(s); var b2: i64 = g(b1); var b3: i64 = g(b2); var b4: i64 = g(b3); var b5: i64 = g(b4);
    var b6: i64 = g(b5); var b7: i64 = g(b6); var b8: i64 = g(b7); var b9: i64 = g(b8); var b10: i64 = g(b9);
    var b11: i64 = g(b10); var b12: i64 = g(b11); var b13: i64 = g(b12);
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
}

var optShapeLegs = []struct {
	name string
	env  []string
}{
	{"typed", nil},
	{"ast", []string{"FERN_SEM_IR="}},
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
			for _, leg := range optShapeLegs {
				if c.typedOnly && leg.name != "typed" {
					continue
				}
				for _, target := range []string{"x86-64-linux", "arm64-linux"} {
					out := filepath.Join(dir, leg.name+"-"+target+".s")
					cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
					cmd.Env = append(os.Environ(), leg.env...)
					if combined, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%s/%s: emitting: %v\n%s", leg.name, target, err, combined)
					}
					asm, err := os.ReadFile(out)
					if err != nil {
						t.Fatal(err)
					}
					body := selfHostFnBody(t, asm, c.fn)
					for _, re := range c.want[target] {
						if !regexp.MustCompile(re).MatchString(body) {
							t.Errorf("%s/%s: %s lacks %q:\n%s", leg.name, target, c.fn, re, body)
						}
					}
					for _, re := range c.forbid[target] {
						if regexp.MustCompile(re).MatchString(body) {
							t.Errorf("%s/%s: %s still has %q:\n%s", leg.name, target, c.fn, re, body)
						}
					}
				}
				tg := h.targets[0]
				bin := filepath.Join(dir, leg.name+".bin")
				cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
				cmd.Env = append(os.Environ(), leg.env...)
				if combined, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: building: %v\n%s", leg.name, err, combined)
				}
				run := exec.Command(bin)
				if len(tg.runner) > 0 {
					run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
				}
				_ = run.Run()
				if got := run.ProcessState.ExitCode(); got != c.exit {
					t.Errorf("%s: exit %d, want %d", leg.name, got, c.exit)
				}
			}
		})
	}
}
