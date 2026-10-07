package e2ecompiler

import "testing"

// paramAppendReclaimCases pin the borrow boundary of the self-append reclaim
// (#5717 / #5713). `a = a.append(v)` on a sole-owner target grows the buffer
// and, on a grow-realloc, reclaims the pre-grow block (`arr_push_owned`)
// instead of leaking it. A borrowed PARAM is never a sole owner: the CALLER
// owns its buffer and still references it, however few aliases the callee
// itself creates.
//
// Modelling the aliasing is not enough to see a violation, because freeing the
// caller's buffer is harmless on its own — no rc underflow, the caller's count
// genuinely does reach zero, and nothing has yet been handed the recycled
// block. The symptom needs the callee to ALLOCATE AGAIN after the append and
// for that allocation to be the value it RETURNS: then the returned object
// sits in the block the param append released, and the caller's exit-sweep
// dec of its own stale pointer frees the value it was just handed.
// `caller-reads-seed-after-callee-append` below is the aliasing-only shape,
// which passes either way and documents that distinction; the other two catch
// the bug (exit 3, want 4).
//
// That is the shape `slc_walk` / `e065_stmts` have: they self-append a param
// while building the `Diag[]` they return, so a violation lets
// `slice_escape_diags` / `e065_diags` free their own return value and the
// diagnostic codes read back as recycled memory (`E063` as ` E06`, `E065` as
// `)E06`). These cases fail in seconds on a standalone program and cover wasm,
// which the x86-only checker corpus (`TestSelfHostCheckerCodes*`) does not.
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
    let out: D[] = [];
    let i: i32 = 0;
    while (i < n) {
        acc = acc.append("xx");
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    let seed: string[] = [];
    return walk(seed, n);
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 30) {
        let d: D[] = entry(4);
        t = (t + d.len()) % 100;
        let junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    let ds: D[] = entry(4);
    let hit: i32 = 0;
    let j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	// Element-kind agnostic: the reclaim is on the buffer, so an `i32[]` param
	// append corrupts the same way.
	{"param-append-i32-recycled", `
struct D { code: string, n: i32 }
function walk(acc: i32[], n: i32): D[] {
    let out: D[] = [];
    let i: i32 = 0;
    while (i < n) {
        acc = acc.append(i);
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    let seed: i32[] = [];
    return walk(seed, n);
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 30) {
        let d: D[] = entry(4);
        t = (t + d.len()) % 100;
        let junk: i32[] = [1, 2];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    let ds: D[] = entry(4);
    let hit: i32 = 0;
    let j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	// The direct half: the CALLER reads its own seed array after the callee
	// self-appended it. The caller's buffer must still hold its own contents.
	// walk returns 7, the seed still reads "keep" -> 8.
	{"caller-reads-seed-after-callee-append", `
function walk(acc: string[], n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { acc = acc.append("xx"); i = i + 1; }
    return acc.len();
}
function entry(): i32 {
    let seed: string[] = [];
    seed = seed.append("keep");
    let r: i32 = walk(seed, 6);
    let hit: i32 = 0;
    if (seed[0] == "keep") { hit = 1; }
    return r + hit;
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 30) {
        t = (t + entry()) % 100;
        let junk: string[] = ["aaaa", "bbbb"];
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
