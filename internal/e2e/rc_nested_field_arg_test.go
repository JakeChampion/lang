package e2e

import "testing"

// A local bound from a call whose argument is a field read TWO levels deep
// (`filter(fresh(), s.frame.alias)`) reclaims exactly like the one-level read
// (`filter(fresh(), s.alias)`): both are counted aliases of a container the
// frame deep-drops, so neither taints the binding. The shape is the self-host
// `lower_stmt_grow_exempt_inner` after LowerState's per-function facts moved
// into a nested box (#8179): the callee hands its first parameter back bare and
// passes the second on to a helper, which is what puts the argument's taint on
// the result, and the two-level read used to keep the conservative taint the
// one-level read had already shed — so `gex` was never swept and its buffer
// leaked once per statement lowered.
//
// Both shapes run in one program; the exit code is the per-iteration bump
// difference between them, so the residual leak the handback itself costs
// (the same in both) cancels out.
const nestedFieldArgSrc = `struct Frame { alias: string[], sole: string[] }
struct Nested { frame: Frame, exempt: string[], n: i32 }
struct Flat { alias: string[], sole: string[], exempt: string[], n: i32 }

function index_of(xs: string[], s: string): i32 {
    var i: i32 = 0;
    while (i < xs.len()) { if (xs[i] == s) { return i; } i = i + 1; }
    return 0 - 1;
}
function filter(names: string[], alias: string[]): string[] {
    if (alias.len() == 0 || names.len() == 0) { return names; }
    var out: string[] = [];
    var i: i32 = 0;
    while (i < names.len()) {
        if (index_of(alias, names[i]) < 0) { out = out.append(names[i]); }
        i = i + 1;
    }
    return out;
}
function push_unique(xs: string[], s: string): string[] {
    if (index_of(xs, s) >= 0) { return xs; }
    return xs.append(s);
}
function names_of(k: i32): string[] {
    var out: string[] = [];
    if (k % 2 == 0) { out = out.append("alpha_even"); } else { out = out.append("alpha_odd"); }
    out = out.append("beta_name");
    return out;
}
function (s: Nested) with_exempt(names: string[]): Nested { return Nested { ...s, exempt: names }; }
function (s: Flat) with_exempt(names: string[]): Flat { return Flat { ...s, exempt: names }; }

function step_nested(k: i32, s: Nested): Nested {
    var gex: string[] = filter(names_of(k), s.frame.alias);
    var gi: i32 = 0;
    while (gi < s.frame.sole.len()) { gex = push_unique(gex, s.frame.sole[gi]); gi = gi + 1; }
    if (gex.len() == 0) { return Nested { ...s, n: s.n + 1 }; }
    var gr: Nested = Nested { ...s.with_exempt(gex), n: s.n + 1 };
    var gnone: string[] = [];
    return gr.with_exempt(gnone);
}
function step_flat(k: i32, s: Flat): Flat {
    var gex: string[] = filter(names_of(k), s.alias);
    var gi: i32 = 0;
    while (gi < s.sole.len()) { gex = push_unique(gex, s.sole[gi]); gi = gi + 1; }
    if (gex.len() == 0) { return Flat { ...s, n: s.n + 1 }; }
    var gr: Flat = Flat { ...s.with_exempt(gex), n: s.n + 1 };
    var gnone: string[] = [];
    return gr.with_exempt(gnone);
}
function main(): i32 {
    var alias: string[] = ["alpha_even", "zeta"];
    var sole: string[] = ["gamma_one", "delta_two"];
    var none: string[] = [];
    var b0: i32 = (__heap_bump_bytes() as i32);
    var sn: Nested = Nested { frame: Frame { alias: alias, sole: sole }, exempt: none, n: 0 };
    var k: i32 = 0;
    while (k < 1000) { sn = step_nested(k, sn); k = k + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var sf: Flat = Flat { alias: alias, sole: sole, exempt: none, n: 0 };
    k = 0;
    while (k < 1000) { sf = step_flat(k, sf); k = k + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (sn.n != 1000 || sf.n != 1000) { return 254; }
    var extra: i32 = ((b1 - b0) - (b2 - b1)) / 64;
    if (extra < 0) { extra = 0; }
    if (extra > 250) { extra = 250; }
    return extra;
}`

// The two-level read must cost no more than the one-level one: a few blocks
// of slack covers allocator granularity, not a per-iteration leak (which
// reads 250, the cap, at 1000 iterations).
func assertNestedFieldArgFlat(t *testing.T, backend string, out string, code int) {
	t.Helper()
	if code == 254 {
		t.Fatalf("%s: value check failed: %s", backend, out)
	}
	if code > 4 {
		t.Errorf("%s: two-level field-read argument leaks %d extra 64-byte units over 1000 iterations (want <= 4): %s", backend, code, out)
	}
}

func TestX86_64NestedFieldArgBindingReclaim(t *testing.T) {
	out, code := compileAndRunX86_64FreeOn(t, nestedFieldArgSrc)
	assertNestedFieldArgFlat(t, "x86-64-linux", out, code)
}

func TestArm64NestedFieldArgBindingReclaim(t *testing.T) {
	out, code := compileAndRunArm64FreeOn(t, nestedFieldArgSrc)
	assertNestedFieldArgFlat(t, "arm64-linux", out, code)
}
