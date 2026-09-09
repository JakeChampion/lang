package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withAliasIRCases exercise the in-place `a = a.with(i, v)` self-reassign through
// the self-host IR path when the array `a` has a lasting LOCAL alias (#3599).
//
// The single-owner / leak model lowered the self-reassign as an in-place arr_set,
// which is UNSOUND once `a` is aliased (`var b = a`, captured into a struct
// literal, …): the in-place write mutates the buffer the alias still reads, so
// `b` observes the change. The interpreter and the native (Perceus) backend both
// copy-on-write and leave the alias unchanged. The fix detects the alias at
// lower_func time (aliased_array_names_of) and routes the aliased self-reassign
// through the value-producing clone (lower_arr_with_value) instead of the
// in-place store. The unaliased "no-alias" case still takes the in-place path.
//
// The own-caller cases cover #8874: a declared consuming parameter can arrive
// shared through its caller, so its update must test runtime uniqueness. These
// also check the counted reference protocol across calls, rebinds and exits.
//
// Each case is oracle-checked against the interpreter, routing-pinned to "ir",
// and returns a non-negative value <= 126.
var withAliasIRCases = []struct {
	name string
	main string
}{
	// The issue's minimal repro: a plain `var b = a` alias, mutate a, read both.
	// In-place mutation made b[0] == 9 too (9+9=18); copy-on-write keeps b[0]==1.
	{"basic", `function main(): i32 { var a = [1, 2, 3]; var b = a; a = a.with(0, 9); return a[0] + b[0]; }`},
	// Two live aliases of the same array; neither must see a's later mutation.
	{"two-alias", `function main(): i32 { var a = [1, 2]; var b = a; var c = a; a = a.with(0, 7); return b[0] + c[0]; }`},
	// The mutation is conditional (inside an if) but the alias is live across it.
	{"cond", `function main(): i32 { var a = [1, 2, 3]; var b = a; if (a[0] == 1) { a = a.with(1, 8); } return a[1] + b[1]; }`},
	// Alias captured into a struct field literal (`H { xs: a }`): the field still
	// references the original buffer, so the in-place mutate would corrupt it.
	{"struct-field-alias", `struct H { xs: i32[] }
function main(): i32 { var a = [1, 2, 3]; var h = H { xs: a }; a = a.with(0, 9); return a[0] + h.xs[0]; }`},
	// Nested array: `var h = g` aliases the outer array; replacing g[0] must not
	// disturb h[0][0].
	{"nested", `function main(): i32 { var g = [[1, 2], [3, 4]]; var h = g; g = g.with(0, [9, 9]); return g[0][0] + h[0][0]; }`},
	// REGRESSION: no alias — the in-place fast path must still apply and be
	// correct (a sole-owned array's .with returns the mutated value).
	{"no-alias", `function main(): i32 { var a = [1, 2, 3]; a = a.with(0, 9); return a[0] + a[1]; }`},
	// An own parameter transfers one counted reference, not unique storage.
	// The alias is in the caller, outside the callee's local alias analysis.
	{"own-caller-snapshot-i32", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function main(): i32 { var a: i32[] = [1, 2, 3]; var old = a; a = update(a); if (old[0] != 1 || old[1] != 2 || old[2] != 3 || a[0] != 9 || a[1] != 2 || a[2] != 3) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-u8", `@noinline
function update(own xs: u8[]): u8[] { xs = xs.with(0, 255u8); return xs; }
function main(): i32 { var a: u8[] = [1u8, 128u8]; var old = a; a = update(a); if (old[0] != 1u8 || old[1] != 128u8 || a[0] != 255u8 || a[1] != 128u8) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-shared-field", `struct S { buf: u8[], n: i32 }
@noinline
function zero(n: i32): u8[] { var a: u8[] = __alloc_u8(n); var i: i32 = 0; while (i < n) { a = a.with(i, 0u8); i = i + 1; } return a; }
@noinline
function put(own dst: u8[], at: i32, v: u8): u8[] { dst = dst.with(at, v); return dst; }
function main(): i32 { var a: S = S { buf: zero(4), n: 0 }; var keep: S = a; a = S { ...a, buf: put(a.buf, 0, 9u8), n: a.n + 1 }; if (keep.n != 0 || a.n != 1) { return 1; } var i: i32 = 0; while (i < 4) { if (keep.buf[i] != 0u8) { return 2; } if (i == 0) { if (a.buf[i] != 9u8) { return 3; } } else { if (a.buf[i] != 0u8) { return 4; } } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-i64", `@noinline
function update(own xs: i64[]): i64[] { xs = xs.with(0, 9000000000i64); return xs; }
function main(): i32 { var a: i64[] = [1000000000i64, 2000000000i64]; var old = a; a = update(a); if (old[0] != 1000000000i64 || old[1] != 2000000000i64 || a[0] != 9000000000i64 || a[1] != 2000000000i64) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-u64", `@noinline
function update(own xs: u64[]): u64[] { xs = xs.with(0, 18446744073709551615u64); return xs; }
function main(): i32 { var a: u64[] = [1u64, 9223372036854775808u64]; var old = a; a = update(a); if (old[0] != 1u64 || old[1] != 9223372036854775808u64 || a[0] != 18446744073709551615u64 || a[1] != 9223372036854775808u64) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-f64", `@noinline
function update(own xs: f64[]): f64[] { xs = xs.with(0, 9.5); return xs; }
function main(): i32 { var a: f64[] = [1.5, 2.5]; var old = a; a = update(a); if (old[0] != 1.5 || old[1] != 2.5 || a[0] != 9.5 || a[1] != 2.5) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"borrowed-to-own-caller-live", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
@noinline
function forward(xs: i32[]): i32[] { xs = update(xs); return xs; }
function main(): i32 { var a: i32[] = [1, 2, 3]; var b = forward(a); if (a[0] != 1 || a[1] != 2 || a[2] != 3 || b[0] != 9 || b[1] != 2 || b[2] != 3) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-f32", `@noinline
function update(own xs: f32[]): f32[] { xs = xs.with(0, 9.5f32); return xs; }
function main(): i32 { var xs: f32[] = [1.5f32, 2.5f32]; var old = xs; xs = update(xs); if (old[0] != 1.5f32 || old[1] != 2.5f32 || xs[0] != 9.5f32 || xs[1] != 2.5f32) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-boolean", `@noinline
function update(own xs: boolean[]): boolean[] { xs = xs.with(0, false); return xs; }
function main(): i32 { var xs: boolean[] = [true, false]; var old = xs; xs = update(xs); if (!old[0] || old[1] || xs[0] || xs[1]) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-u32", `@noinline
function update(own xs: u32[]): u32[] { xs = xs.with(0, 4294967295u32); return xs; }
function main(): i32 { var xs: u32[] = [1u32, 2147483648u32]; var old = xs; xs = update(xs); if (old[0] != 1u32 || old[1] != 2147483648u32 || xs[0] != 4294967295u32 || xs[1] != 2147483648u32) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-snapshot-char", `@noinline
function update(own xs: char[]): char[] { xs = xs.with(0, 937 as char); return xs; }
function main(): i32 { var xs: char[] = [97 as char, 128512 as char]; var old = xs; xs = update(xs); if ((old[0] as i32) != 97 || (old[1] as i32) != 128512 || (xs[0] as i32) != 937 || (xs[1] as i32) != 128512) { return 1; } return 0; }`},
	{"own-caller-foreach-snapshot", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function main(): i32 { var rows = [[1, 2], [3, 4]]; var sum = 0; for xs in rows { xs = update(xs); sum = sum + xs[0] + xs[1]; } if (sum != 24 || rows[0][0] != 1 || rows[0][1] != 2 || rows[1][0] != 3 || rows[1][1] != 4) { return 1; } return 0; }`},
	{"own-caller-match-payload-snapshot", `enum E { Data(i32[]) }
@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function forward(e: E): i32[] { match (e) { Data(xs) => { xs = update(xs); return xs; } } return []; }
function main(): i32 { var e = Data([1, 2]); var xs = forward(e); if (xs[0] != 9 || xs[1] != 2) { return 1; } match (e) { Data(old) => { if (old[0] != 1 || old[1] != 2) { return 2; } } } return 0; }`},
	{"own-caller-lifetime-foreach-projection", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function churn(rows: i32[][]): i32 { var i = 0; while (i < 32) { for xs in rows { if (xs[0] == 3) { continue; } xs = update(xs); xs = update(xs); if (xs[0] != 9 || xs[1] != 2) { return 1; } if (i == 31) { break; } } i = i + 1; } return 0; }
function main(): i32 { var rows = [[1, 2], [3, 4]]; if (churn(rows) != 0) { return 1; } var before = __heap_bump_bytes(); if (churn(rows) != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (rows[0][0] != 1 || rows[1][0] != 3) { return 4; } return 0; }`},
	{"own-caller-lifetime-match-projection", `enum E { Data(i32[]) }
@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function forward(e: E, change: boolean): i32[] { match (e) { Data(xs) => { if (change) { xs = update(xs); xs = update(xs); } return xs; } } return []; }
function churn(e: E): i32 { var i = 0; while (i < 32) { var xs = forward(e, i % 2 == 0); if (i % 2 == 0) { if (xs[0] != 9) { return 1; } } else { if (xs[0] != 1) { return 2; } } if (xs[1] != 2) { return 3; } i = i + 1; } return 0; }
function main(): i32 { var e = Data([1, 2]); if (churn(e) != 0) { return 1; } var before = __heap_bump_bytes(); if (churn(e) != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } match (e) { Data(old) => { if (old[0] != 1 || old[1] != 2) { return 4; } } } return 0; }`},
	{"own-caller-lifetime-option-projection", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function churn(e: Option[i32[]]): i32 { var i = 0; while (i < 32) { match (e) { Some(xs) => { xs = update(xs); if (xs[0] != 9 || xs[1] != 2) { return 1; } }, None => { return 2; } } i = i + 1; } return 0; }
function main(): i32 { var e: Option[i32[]] = Some([1, 2]); if (churn(e) != 0) { return 1; } var before = __heap_bump_bytes(); if (churn(e) != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } match (e) { Some(xs) => { if (xs[0] != 1 || xs[1] != 2) { return 4; } }, None => { return 5; } } return 0; }`},
	{"own-caller-lifetime-fresh-payload-transfer", `enum E { Data(i32[]) }
function make(n: i32): E { return Data([n, n + 1]); }
function take(n: i32): i32[] { var e = make(n); match (e) { Data(xs) => { return xs; } } return []; }
function churn(): i32 { var i = 0; while (i < 32) { var xs = take(i); if (xs[0] != i || xs[1] != i + 1) { return 1; } i = i + 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } return 0; }`},
	{"own-caller-wide-match-projection", `enum E { Data(f64[]) }
@noinline
function update(own xs: f64[]): f64[] { xs = xs.with(0, 9.5); return xs; }
function forward(e: E): f64[] { match (e) { Data(xs) => { if (xs[0] != 1.5 || xs[1] != 2.5) { return []; } xs = update(xs); return xs; } } return []; }
function main(): i32 { var e = Data([1.5, 2.5]); var xs = forward(e); if (xs.len() != 2 || xs[0] != 9.5 || xs[1] != 2.5) { return 1; } match (e) { Data(old) => { if (old[0] != 1.5 || old[1] != 2.5) { return 2; } } } return 0; }`},
	{"own-caller-wide-foreach-projection", `@noinline
function update(own xs: u64[]): u64[] { xs = xs.with(0, 18446744073709551615u64); return xs; }
function main(): i32 { var rows: u64[][] = [[1u64, 9223372036854775808u64]]; for xs in rows { xs = update(xs); if (xs[0] != 18446744073709551615u64 || xs[1] != 9223372036854775808u64) { return 1; } } if (rows[0][0] != 1u64 || rows[0][1] != 9223372036854775808u64) { return 2; } return 0; }`},
	{"own-caller-operand-order", `@noinline
function tap(c: Cell[i32], digit: i32, result: i32): i32 { c.set(c.get() * 10 + digit); return result; }
@noinline
function update(own xs: i32[], c: Cell[i32]): i32[] { xs = xs.with(tap(c, 1, 0), tap(c, 2, 9)); return xs; }
function main(): i32 { var c = cell_new(0); var xs = [1, 2]; var old = xs; xs = update(xs, c); if (c.get() != 12 || old[0] != 1 || xs[0] != 9 || xs[1] != 2) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-indirect", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
function main(): i32 { var f = update; var xs = [1, 2]; var old = xs; xs = f(xs); if (xs[0] != 9 || old[0] != 1) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-unique-reuse", `@noinline
function update(own xs: i32[], n: i32): i32[] { xs = xs.with(0, n); return xs; }
function churn(): i32 { var xs = [1, 2, 3]; var before: i64 = __heap_bump_bytes(); var i = 0; while (i < 32) { xs = update(xs, i); i = i + 1; } if (xs[0] != 31 || xs[1] != 2 || xs[2] != 3) { return 1; } if (__heap_bump_bytes() != before) { return 2; } return 0; }
function main(): i32 { if (churn() != 0 || churn() != 0) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-higher-order", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
@noinline
function apply(f: (i32[]) => i32[], xs: i32[]): i32[] { return f(xs); }
function main(): i32 { var xs = [1, 2]; var next = apply(update, xs); if (next[0] != 9 || xs[0] != 1 || xs[1] != 2) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-parameter-alias", `@noinline
function keep(own xs: i32[]): i32[] { var alias = xs; return alias; }
function churn(): i32 { var i = 0; while (i < 32) { var xs = [1, 2]; xs = keep(xs); if (xs[0] != 1 || xs[1] != 2) { return 1; } i = i + 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before: i64 = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-capturing-callback", `@noinline
function update(own xs: i32[], n: i32): i32[] { xs = xs.with(0, n); return xs; }
function make(n: i32): (i32[]) => i32[] { return (xs: i32[]): i32[] => { xs = update(xs, n); return xs; }; }
function main(): i32 { var f = make(9); var xs = [1, 2]; var next = f(xs); if (next[0] != 9 || xs[0] != 1 || xs[1] != 2) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-inline-callback", `@noinline
function update(own xs: i32[]): i32[] { xs = xs.with(0, 9); return xs; }
@noinline
function apply(f: (i32[]) => i32[], xs: i32[]): i32[] { return f(xs); }
function main(): i32 { var xs = [1, 2]; var next = apply((a: i32[]): i32[] => { a = update(a); return a; }, xs); if (next[0] != 9 || xs[0] != 1 || xs[1] != 2) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-consume", `@noinline
function consume(own xs: i32[]): i32 { if (xs[0] == 1) { return xs[1]; } return xs.len(); }
@noinline
function forward(own xs: i32[]): i32 { return consume(xs); }
function churn(): i32 { var i = 0; while (i < 32) { if (forward([1, 2, 3]) != 2 || consume([4, 5, 6]) != 3) { return 1; } i = i + 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before: i64 = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-shared-update", `@noinline
function update(own xs: i32[], n: i32): i32[] { xs = xs.with(0, n); return xs; }
function churn(): i32 { var i = 0; while (i < 32) { var xs = [1, 2, 3]; var old = xs; xs = update(xs, i); if (xs[0] != i || old[0] != 1 || old[2] != 3) { return 1; } i = i + 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before: i64 = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-repeated-forward", `@noinline
function update(own xs: i32[], n: i32): i32[] { xs = xs.with(0, n); return xs; }
@noinline
function forward(xs: i32[]): i32[] { var i = 0; while (i < 32) { xs = update(xs, i); i = i + 1; } return xs; }
function churn(): i32 { var i = 0; while (i < 32) { var xs = [1, 2, 3]; var next = forward(xs); if (xs[0] != 1 || next[0] != 31 || next[2] != 3) { return 1; } i = i + 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before: i64 = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-grow", `@noinline
function grow(own xs: i32[]): i32[] { var i = 0; while (i < 32) { xs = xs.append(i); i = i + 1; } return xs; }
function churn(): i32 { var i = 0; while (i < 32) { var xs = [1, 2, 3]; xs = grow(xs); if (xs.len() != 35 || xs[0] != 1 || xs[34] != 31) { return 1; } i = i + 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before: i64 = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-earlier-argument-read", `@noinline
function update(n: i32, own xs: i32[]): i32[] { xs = xs.with(0, n); return xs; }
@noinline
function forward(own xs: i32[]): i32[] { return update(xs[1], xs); }
function main(): i32 { var xs = forward([1, 2, 3]); if (xs[0] != 2 || xs[1] != 2 || xs[2] != 3) { return 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"own-caller-lifetime-different-result", `@noinline
function replace(own xs: i32[]): i32[] { return [xs[0] + 1, xs[1], xs[2]]; }
function churn(): i32 { var xs = [1, 2, 3]; var i = 0; while (i < 32) { xs = replace(xs); i = i + 1; } if (xs[0] != 33 || xs[1] != 2 || xs[2] != 3) { return 1; } return 0; }
function main(): i32 { if (churn() != 0) { return 1; } var before: i64 = __heap_bump_bytes(); if (churn() != 0) { return 2; } if (__heap_bump_bytes() != before) { return 3; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
}

// Observe underflows after the fixture's array-owning frame has exited. A
// counter read inside that frame cannot see a double drop in its exit sweep.
func withAliasCheckedSource(name, source string) string {
	if !strings.HasPrefix(name, "own-caller-") && name != "borrowed-to-own-caller-live" {
		return source
	}
	source = strings.Replace(source, "function main(): i32", "function exercise_own_array_case(): i32", 1)
	return source + `
function main(): i32 {
    var result = exercise_own_array_case();
    if (result != 0) { return result; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`
}

func TestSelfHostOwnArrayLifetimeNativeOracle(t *testing.T) {
	for _, tc := range withAliasIRCases {
		if !strings.HasPrefix(tc.name, "own-caller-") && tc.name != "borrowed-to-own-caller-live" {
			continue
		}
		// These semantic cases already fail in the unchanged Go backend.
		// They remain interpreter-checked on all three self-host targets.
		if tc.name == "own-caller-operand-order" || tc.name == "own-caller-higher-order" || tc.name == "own-caller-lifetime-option-projection" {
			continue
		}
		tc.main = withAliasCheckedSource(tc.name, tc.main)
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.main+"\n"); code != 0 {
				t.Fatalf("native violated ownership lifetime contract: exit %d", code)
			}
		})
	}
}

func TestSelfHostWithAliasIRArm64(t *testing.T) {
	armGCC, armRunner := arm64Tooling(t)
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range withAliasIRCases {
		tc.main = withAliasCheckedSource(tc.name, tc.main)
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			if strings.HasPrefix(tc.name, "own-caller-") || tc.name == "borrowed-to-own-caller-live" {
				if want != 0 {
					t.Fatalf("interpreter violated snapshot contract: exit %d", want)
				}
			}
			asm := runCaptureStrictIR(t, gcc, runner, driver, []byte(tc.main), "-target", "arm64-linux", "-ir")
			binary := buildBin(t, armGCC, dir, tc.name, string(asm))
			cmd := runArm64Bin(armRunner, binary)
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("program did not exit normally: %v\n%s", err, output)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("exit %d, want %d: %v\n%s", got, want, err, output)
			}
		})
	}
}

// TestSelfHostWithAliasIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostWithAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range withAliasIRCases {
		tc.main = withAliasCheckedSource(tc.name, tc.main)
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if strings.HasPrefix(tc.name, "own-caller-") || tc.name == "borrowed-to-own-caller-live" {
				if want != 0 {
					t.Fatalf("interpreter violated snapshot contract: exit %d", want)
				}
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostWithAliasIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostWithAliasIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host with-alias wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range withAliasIRCases {
		tc.main = withAliasCheckedSource(tc.name, tc.main)
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			if strings.HasPrefix(tc.name, "own-caller-") || tc.name == "borrowed-to-own-caller-live" {
				if want != 0 {
					t.Fatalf("interpreter violated snapshot contract: exit %d", want)
				}
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "withalias_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("with-alias wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
