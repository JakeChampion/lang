package e2ecompiler

import "testing"

// mapBoxColumnCases pin #4353 item 3: a map whose VALUE column holds heap BOXES
// — a scalar-element array (`Map[K, i32[]]`) or an all-scalar struct
// (`Map[K, Q]`) — must reclaim those boxes, and a value read back out of such a
// column must survive the reading frame's dec-sweep.
//
// Two defects, one column:
//
//  1. RECLAIM. The column deep-release only ever knew the STRING kind (MAPVS: /
//     MAPKS:), so a box column kept the shallow buffer-only free and every
//     element leaked. Measured against the native oracle (flat on all four
//     shapes): 86 B/round for `Map[i32, i32[]]` and 94 B/round for `Map[i32, Q]`
//     on x86-64, and 55 / 63 B/round on wasm.
//
//  2. USE-AFTER-FREE, pre-existing and independent of the leak. `let v: i32[] =
//     m.get_or(k, d)` binds the column's RAW pointer — the register map read
//     hands back an uncounted alias — into a slot the exit dec-sweep releases
//     unconditionally, so the sweep freed the map's live value. The read now
//     retains, the read-side twin of #6880's insert-side vretain. The `read-then-
//     recycle` case below is the one that returned another local's contents on
//     the self-host register backends while the interpreter and native agreed on
//     the right answer.
//
// The flatness cases are DIFFERENTIAL against a same-shape scalar-valued map:
// both share the map's __fern_arr_push grow-leak (a separate, documented
// LOAD-BEARING leak), so that baseline cancels and only the VALUE column can
// make the box map grow more. Building the map in a HELPER is deliberate — a map
// declared directly in a loop is not freed per iteration (a separate gap).
//
// Exit 0 is correct throughout; each nonzero code names the check that failed.
const mapBoxColumnExitHint = "a page count = the box column still leaks; 90/91 = wrong value read back; 99 = over-release"

var mapBoxColumnCases = []struct {
	name string
	src  string
}{
	// RECLAIM, array column. Returns the page delta of the steady window, so a
	// leaking column exits nonzero and a flat one exits 0.
	{"arr-column-flat", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32[]] = Map { 1: [n, n + 1], 2: [n + 2, n + 3, n + 4] };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function build_i32(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i) + build_i32(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j) + build_i32(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 8800) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// RECLAIM, struct column. Q is all-scalar, so one dec per element box frees
	// it completely — which is exactly what the credit's gate demands.
	{"struct-column-flat", `import "core/map";
struct Q { a: i32, b: i32 }

function build(n: i32): i32 {
    let m: Map[i32, Q] = Map { 1: Q { a: n, b: n + 1 }, 2: Q { a: n + 2, b: n + 3 } };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 4400) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// USE-AFTER-FREE. `inner` binds the column's value into an array slot whose
	// sweep releases it; `junk` then recycles the freed block, and the second
	// read sees it. Without the read-side retain the self-host register
	// backends returned 90 here while the interpreter and native returned 0.
	{"read-then-recycle", `import "core/map";
function inner(m: Map[i32, i32[]]): i32 {
    let v: i32[] = m.get_or(2, []);
    return v.len();
}

function build(n: i32): i32 {
    let m: Map[i32, i32[]] = Map { 2: [n + 2, n + 3, n + 4] };
    let a: i32 = inner(m);
    let junk: i32[] = [999, 998, 997];
    let v2: i32[] = m.get_or(2, []);
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < v2.len()) { s = s + v2[i]; i = i + 1; }
    if (a != 3) { return 0 - 1; }
    if (junk.len() != 3) { return 0 - 2; }
    return s;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 100) {
        if (build(i) != 3 * i + 9) { return 90; }
        i = i + 1;
    }
    return 0;
}
`},
	// Both columns at once, with the values read back and the RC underflow
	// counter consulted: the per-element free must not double-release. This is
	// the case that caught the missing read-side retain — the column free turned
	// the silent early free above into a reported over-release.
	{"read-back-no-over-release", `import "core/map";
struct Q { a: i32, b: i32 }

function build_arr(n: i32): i32 {
    let m: Map[i32, i32[]] = Map { 1: [n, n + 1], 2: [n + 2, n + 3, n + 4] };
    let v: i32[] = m.get_or(2, []);
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < v.len()) { s = s + v[i]; i = i + 1; }
    return s;
}

function build_struct(n: i32): i32 {
    let m: Map[i32, Q] = Map { 1: Q { a: n, b: n + 1 }, 2: Q { a: n + 2, b: n + 3 } };
    let q: Q = m.get_or(2, Q { a: 0, b: 0 });
    return q.a + q.b;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 500) {
        if (build_arr(i) != 3 * i + 9) { return 90; }
        if (build_struct(i) != 2 * i + 5) { return 91; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`},
	// Control: a `string[]` value column is NOT credited — its elements are
	// pointers that one dec does not release — so it keeps the shallow free.
	// It must still read back correctly and report no over-release, which is
	// what says the gate refused rather than half-freeing.
	{"strarr-column-uncredited-control", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, string[]] = Map { 1: ["a" + "b", "c" + "d"] };
    let v: string[] = m.get_or(1, []);
    if (v.len() != 2) { return 0 - 1; }
    if (v[0].len() != 2) { return 0 - 2; }
    return 1;
}

function main(): i32 {
    let i: i32 = 0;
    while (i < 200) {
        if (build(i) != 1) { return 90; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`},
}

// TestSelfHostMapBoxColumnReclaimIRX86_64 is the x86-64 leg.
func TestSelfHostMapBoxColumnReclaimIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want 0 (%s)", tc.name, code, mapBoxColumnExitHint)
			}
		})
	}
}

// TestSelfHostMapBoxColumnReclaimIRArm64 is the arm64 leg: same programs through
// the arm64 map-free family, run under qemu.
func TestSelfHostMapBoxColumnReclaimIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want 0 (%s)", tc.name, code, mapBoxColumnExitHint)
			}
		})
	}
}

// TestSelfHostMapBoxColumnReclaimIRWasm is the wasm leg.
func TestSelfHostMapBoxColumnReclaimIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want 0 (%s)", tc.name, code, mapBoxColumnExitHint)
			}
		})
	}
}
