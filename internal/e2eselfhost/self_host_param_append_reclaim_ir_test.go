package e2eselfhost

import "testing"

// paramAppendReclaimCases pin the borrow boundary of the self-append reclaim
// (#5717 / #5713). `a = a.append(v)` on a sole-owner target grows the buffer
// and, on a grow-realloc, reclaims the pre-grow block (`arr_push_owned`)
// instead of leaking it. The sole-owner test, `is_aliased_name`, only sees
// aliases created INSIDE the function, so it also fired when the target was a
// borrowed PARAM — whose buffer the CALLER owns and still references. The fix
// adds the explicit `slot < n_params` borrow boundary.
//
// #5717 landed the fix WITHOUT a regression test, on the finding that the
// symptom could not be reproduced synthetically ("several shapes modelling the
// aliasing exactly still exit 0 unfixed — the corruption needs the real
// allocation order"), leaving the whole-checker corpus
// (`TestSelfHostCheckerCodes*` / `…Differential*`) as the only pin. These cases
// show it IS synthetically reproducible, and name the missing ingredient.
//
// Modelling the aliasing is not enough, because freeing the caller's buffer is
// harmless on its own — no rc underflow, the caller's count genuinely does
// reach zero, and nothing has yet been handed the recycled block. The symptom
// needs the callee to ALLOCATE AGAIN after the append and for that allocation
// to be the value it RETURNS: then the returned object sits in the block the
// param append released, and the caller's exit-sweep dec of its own stale
// pointer frees the value it was just handed. `caller-reads-seed-after-callee-append`
// below is the aliasing-only shape and passes even unfixed — it is kept
// precisely to document that distinction; the other two catch the bug.
//
// That is the live shape: `slc_walk` / `e065_stmts` self-append their
// `localarr` / `sbacked` param while building the `Diag[]` they return, so
// `slice_escape_diags` / `e065_diags` freed their own return value on the way
// out and the diagnostic codes read back as recycled memory (`E063` as ` E06`,
// `E065` as `)E06`).
//
// Verified to catch it: reverting the `|| slot < s.n_params` gate turns
// `param-append-recycled-by-return` and `param-append-i32-recycled` red (both
// exit 3, want 4). Worth keeping alongside the corpus pin — it fails in
// seconds on a standalone program instead of via a multi-minute checker build
// reporting garbled diagnostic codes, and it covers wasm, which the
// x86-only checker corpus does not.
var paramAppendReclaimCases = []struct {
	name string
	main string
	want int
}{
	// The live shape: the callee self-appends a `string[]` param AND builds a
	// struct array it returns, so the returned buffer lands in the released
	// block. Reading the returned array's string fields must survive the
	// caller's exit sweep. 4 diagnostics, all readable.
	{"param-append-recycled-by-return", `
struct D { code: string, n: i32 }
function walk(acc: string[], n: i32): D[] {
    var out: D[] = [];
    var i: i32 = 0;
    while (i < n) {
        acc = acc.append("xx");
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    var seed: string[] = [];
    return walk(seed, n);
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 30) {
        var d: D[] = entry(4);
        t = (t + d.len()) % 100;
        var junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    var ds: D[] = entry(4);
    var hit: i32 = 0;
    var j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	// Element-kind agnostic: the reclaim is on the buffer, so an `i32[]` param
	// append corrupts the same way.
	{"param-append-i32-recycled", `
struct D { code: string, n: i32 }
function walk(acc: i32[], n: i32): D[] {
    var out: D[] = [];
    var i: i32 = 0;
    while (i < n) {
        acc = acc.append(i);
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    var seed: i32[] = [];
    return walk(seed, n);
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 30) {
        var d: D[] = entry(4);
        t = (t + d.len()) % 100;
        var junk: i32[] = [1, 2];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    var ds: D[] = entry(4);
    var hit: i32 = 0;
    var j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	// The direct half: the CALLER reads its own seed array after the callee
	// self-appended it. The caller's buffer must still hold its own contents.
	// walk returns 7, the seed still reads "keep" -> 8.
	{"caller-reads-seed-after-callee-append", `
function walk(acc: string[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { acc = acc.append("xx"); i = i + 1; }
    return acc.len();
}
function entry(): i32 {
    var seed: string[] = [];
    seed = seed.append("keep");
    var r: i32 = walk(seed, 6);
    var hit: i32 = 0;
    if (seed[0] == "keep") { hit = 1; }
    return r + hit;
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 30) {
        t = (t + entry()) % 100;
        var junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    return entry();
}
`, 8},
}

// TestSelfHostParamAppendReclaimIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostParamAppendReclaimIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range paramAppendReclaimCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main, target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
