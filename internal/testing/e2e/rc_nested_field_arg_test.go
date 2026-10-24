package e2e

import "testing"

const nestedFieldArgSrc = `struct Frame { alias: string[], sole: string[] }
struct Nested { frame: Frame, exempt: string[], n: i32 }
struct Flat { alias: string[], sole: string[], exempt: string[], n: i32 }

function index_of(xs: string[], s: string): i32 {
    let i: i32 = 0;
    while (i < xs.len()) { if (xs[i] == s) { return i; } i = i + 1; }
    return 0 - 1;
}
function filter(names: string[], alias: string[]): string[] {
    if (alias.len() == 0 || names.len() == 0) { return names; }
    let out: string[] = [];
    let i: i32 = 0;
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
    let out: string[] = [];
    if (k % 2 == 0) { out = out.append("alpha_even"); } else { out = out.append("alpha_odd"); }
    out = out.append("beta_name");
    return out;
}
function (s: Nested) with_exempt(names: string[]): Nested { return Nested { ...s, exempt: names }; }
function (s: Flat) with_exempt(names: string[]): Flat { return Flat { ...s, exempt: names }; }

function step_nested(k: i32, s: Nested): Nested {
    let gex: string[] = filter(names_of(k), s.frame.alias);
    let gi: i32 = 0;
    while (gi < s.frame.sole.len()) { gex = push_unique(gex, s.frame.sole[gi]); gi = gi + 1; }
    if (gex.len() == 0) { return Nested { ...s, n: s.n + 1 }; }
    let gr: Nested = Nested { ...s.with_exempt(gex), n: s.n + 1 };
    let gnone: string[] = [];
    return gr.with_exempt(gnone);
}
function step_flat(k: i32, s: Flat): Flat {
    let gex: string[] = filter(names_of(k), s.alias);
    let gi: i32 = 0;
    while (gi < s.sole.len()) { gex = push_unique(gex, s.sole[gi]); gi = gi + 1; }
    if (gex.len() == 0) { return Flat { ...s, n: s.n + 1 }; }
    let gr: Flat = Flat { ...s.with_exempt(gex), n: s.n + 1 };
    let gnone: string[] = [];
    return gr.with_exempt(gnone);
}
function main(): i32 {
    let alias: string[] = ["alpha_even", "zeta"];
    let sole: string[] = ["gamma_one", "delta_two"];
    let none: string[] = [];
    let b0: i32 = (__heap_bump_bytes() as i32);
    let sn: Nested = Nested { frame: Frame { alias: alias, sole: sole }, exempt: none, n: 0 };
    let k: i32 = 0;
    while (k < 1000) { sn = step_nested(k, sn); k = k + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let sf: Flat = Flat { alias: alias, sole: sole, exempt: none, n: 0 };
    k = 0;
    while (k < 1000) { sf = step_flat(k, sf); k = k + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (sn.n != 1000 || sf.n != 1000) { return 254; }
    let extra: i32 = ((b1 - b0) - (b2 - b1)) / 64;
    if (extra < 0) { extra = 0; }
    if (extra > 250) { extra = 250; }
    return extra;
}`

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
