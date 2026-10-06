package e2ecompiler

import "testing"

// u64IRCases exercise std/u64's named methods — `min` / `max` / `clamp` — through
// the self-host IR path on x86-64 + wasm. The raw unsigned operators
// (`>` `<` `>>` `/` `%`, plus u64 param+return) are already covered by the #2904
// u64_* fixtures in the parity corpus (testdata/parity/,
// TestSelfHostParityCorpus*); this adds the std/u64 *method* surface that
// those don't, and the new wrinkle is **unsigned `clamp`/`max` against a
// high-bit-set bound** (>= 2^63), where a signed comparison inside the helper
// would pick the wrong branch. min/max/clamp are inlined verbatim from std/u64 as
// free functions. (std/u64's `to_string` is excluded: it wraps core/int's
// `__int_to_string_u64`, whose `u8[]`/`usize`/`__memcpy` internals route through
// the AST path — a separate low-level concern.)
//
// No imports are needed for the interpreter oracle (inlined free functions +
// builtin casts). Each case returns a value kept <= 126 (avoiding the wasmtime
// exit-code truncation gap, cf. #2908) and is oracle-checked against the
// interpreter. FEATURE-AUDIT std/u64 row.
const u64IRPrelude = `function u64_min(a: u64, b: u64): u64 { if (a < b) { return a; } return b; }
function u64_max(a: u64, b: u64): u64 { if (a > b) { return a; } return b; }
function u64_clamp(n: u64, lo: u64, hi: u64): u64 { if (n < lo) { return lo; } if (n > hi) { return hi; } return n; }
function u64_id(x: u64): u64 { return x; }
struct UPair { n: u64 }
`

var u64IRCases = []struct {
	name string
	main string
}{
	// min / max on small values.
	{"min", `let a: u64 = 7 as u64; let b: u64 = 3 as u64; return u64_min(a, b) as i32;`},
	{"max", `let a: u64 = 7 as u64; let b: u64 = 3 as u64; return u64_max(a, b) as i32;`},
	// clamp: below / within / above the range.
	{"clamp-lo", `return u64_clamp(5 as u64, 10 as u64, 40 as u64) as i32;`},
	{"clamp-mid", `return u64_clamp(25 as u64, 10 as u64, 40 as u64) as i32;`},
	{"clamp-hi", `return u64_clamp(50 as u64, 10 as u64, 40 as u64) as i32;`},
	// max with a high-bit-set operand (>= 2^63): u64_max's internal `>` must be
	// unsigned, so it picks `big`; % 100 of ...007 = 7. A signed compare would
	// read `big` as negative and wrongly return `b`.
	{"umax-highbit", `let a: u64 = 18000000000000000007 as u64; let b: u64 = 9 as u64; return (u64_max(a, b) % (100 as u64)) as i32;`},
	// clamp with a high-bit-set hi bound: n (=50) must stay within [10, big], so
	// the result is 50 — only if u64_clamp's `n > hi` compare is unsigned (else
	// `big` reads negative, n > hi is true, and it clamps to the wrong value).
	{"uclamp-highbit-hi", `let hi: u64 = 18000000000000000000 as u64; return u64_clamp(50 as u64, 10 as u64, hi) as i32;`},
	// A CONCRETE u64-returning function's result chained DIRECTLY in an unsigned
	// op, where the call is the SOLE u64 operand (a shift follows its left operand,
	// and the shift amount is a plain i32) — so the unsigned-ness depends only on the
	// callee's u64 return, not on an `as u64` sibling. Without is_u64_ret_fn the
	// shift lowered SIGNED (arithmetic) and diverged: 0xF9CCD8A1C5080000 >> 57 is
	// 124 unsigned but 252 (sign-extended low byte) signed. #5159.
	{"concrete-u64-ret-shift", `let a: u64 = 18000000000000000000 as u64; return (u64_id(a) >> 57) as i32;`},
	// A u64-valued if/match-EXPRESSION (the 0-arg IIFE the desugar emits) chained
	// in a shift, where the IIFE is the SOLE u64 operand. expr_is_u64 gained an
	// IIFE arm (the u64 sibling of expr_is_f64's) so the shift stays unsigned —
	// same 124-vs-252 distinction as the concrete-call case.
	{"u64-iife-shift", `let c: boolean = true; let a: u64 = 18000000000000000000 as u64; return ((if (c) { a } else { 0 as u64 }) >> 57) as i32;`},
	// A u64 STRUCT FIELD read chained in a shift (`p.n >> 57`). expr_is_u64 gained
	// a struct-field arm (the u64 sibling of expr_is_f64's / the tuple-element
	// case) so the shift stays unsigned — 124, not the signed 252.
	{"struct-u64-field-shift", `let p: UPair = UPair { n: 18000000000000000000 as u64 }; return (p.n >> 57) as i32;`},
	// A direct index of a u64[] LITERAL chained in a shift (`[big, …][0] >> 57`):
	// expr_is_u64's ExprIndex arm gained an ExprArray case so the element is
	// unsigned — 124, not the signed 252.
	{"u64-literal-index-shift", `return ([18000000000000000000 as u64, 1 as u64][0] >> 57) as i32;`},
	// A direct index of a u64[] SLICE chained in a shift (`a[lo:hi][0] >> 57`):
	// the sliced array is u64[] (expr_is_u64arr), so the element stays unsigned.
	{"u64-slice-index-shift", `let a: u64[] = [18000000000000000000 as u64, 1 as u64, 2 as u64]; return (a[0:2][0] >> 57) as i32;`},
}

func u64IRSrc(mainBody string) string {
	return u64IRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostU64IR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostU64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64IRCases {
		src := u64IRSrc(tc.main)
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
