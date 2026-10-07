package e2ecompiler

import "testing"

// A callee that hands a borrowed struct parameter back COUNTED (#9203) — its
// return path retains the box — leaves the caller owning that count. When the
// caller's own argument is itself a borrowed parameter, the caller must still
// release it:
//
//   - a binding (`let a = mk(p)`) is released like any owned result;
//   - a read straight through the result (`mk(p).xs.len()`) releases the temp
//     for a free-call callee as well as for a METHOD callee.
//
// A missed release leaks one count per call, and the box it holds with it.
// With an OWNED argument there is nothing to miss, which is why every row
// borrows `p`.
var countedHandbackBorrowedCases = []struct{ name, src string }{
	{"binding", `struct St { ops: i32[], n: i32 }
@noinline function mk(p: St): St { return p; }
function take(p: St): i32 { let a: St = mk(p); return a.ops.len() + p.ops.len() + a.n; }
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) { let s: St = St { ops: [j, 1, 2], n: 1 }; t = t + take(s); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}`},
	{"read-through", `import "std/i32";
struct St { ops: i32[], tag: string, n: i32 }
@noinline function mk(p: St): St { return p; }
function take(p: St): i32 { return mk(p).ops.len() + mk(p).tag.len() + mk(p).n + mk(p).ops[1]; }
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [j, 1, 2, 3], tag: "ab" + j.to_string(), n: 1 };
        t = t + take(s) + s.ops.len();
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}`},
	// Fresh on one path, the handback on the other: the binding's release has
	// to free the first and give the second's count back.
	{"fresh-or-handback", `struct St { ops: i32[], n: i32 }
function mk(p: St, k: i32): St { if (k > 2) { return St { ops: [k], n: k }; } return p; }
function take(p: St, k: i32): i32 { let a: St = mk(p, k); return a.ops.len() + mk(p, k).ops.len() + p.ops.len(); }
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) { let s: St = St { ops: [j, 1, 2, 3], n: 1 }; t = t + take(s, j % 5); j = j + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}`},
	// The binding rebuilt from its own spread: the rebind supersedes the
	// handed-back box, which the caller still owns and reads afterwards.
	{"binding-rebuilt", `struct St { ops: i32[], n: i32 }
@noinline function mk(p: St): St { return p; }
function take(p: St): i32 {
    let a: St = mk(p);
    let i: i32 = 0;
    while (i < 3) { a = St { ...a, ops: a.ops.append(i), n: a.n + 1 }; i = i + 1; }
    return p.ops.len() * 10 + a.ops.len() + p.ops[2];
}
function main(): i32 {
    let t: i32 = 0; let j: i32 = 0;
    while (j < 50) {
        let s: St = St { ops: [], n: 0 };
        let k: i32 = 0;
        while (k < 4) { s = St { ...s, ops: s.ops.append(k), n: s.n + 1 }; k = k + 1; }
        t = t + take(s);
        j = j + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 100;
}`},
}

func TestSelfHostCountedHandbackBorrowedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

	for _, tc := range countedHandbackBorrowedCases {
		t.Run(tc.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdict(t, cli, dir, "cnthb_"+tc.name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, "cnthb_"+tc.name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native verdict %s (exit %d): the oracle itself is not clean", natV, natExit)
			}
			if shExit != natExit {
				t.Fatalf("self-host exited %d, native %d (99 = rc underflow)", shExit, natExit)
			}
			if shV != verdictClean {
				t.Fatalf("self-host verdict %s, want clean", shV)
			}
		})
	}
}
