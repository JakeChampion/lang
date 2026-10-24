package e2ecompiler

import "testing"

var structAliasRebindCases = []struct{ name, src string }{
	{"loop-alias-array-override", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    let s: S = S { ops: [1, 2, 3], n: 0 };
    let i: i32 = 0;
    while (i < 200) {
        let prev: S = s;
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n;
}
function main(): i32 { let t: i32 = 0; let k: i32 = 0; while (k < 100) { t = t + run(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	{"loop-alias-scalar-override", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    let s: S = S { ops: [1, 2, 3], n: 0 };
    let i: i32 = 0;
    while (i < 200) {
        let prev: S = s;
        s = S { ...s, n: s.n + 1 };
        i = i + 1;
    }
    return s.n;
}
function main(): i32 { let t: i32 = 0; let k: i32 = 0; while (k < 100) { t = t + run(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	{"alias-read-after-rebind", `struct P { xs: i32[], n: i32 }
function f(k: i32): i32 {
    let t: P = P { xs: [k, k + 1], n: k };
    let keep: P = t;
    t = P { xs: [9, 9, 9], n: 1 };
    return keep.xs[1] + t.xs.len();
}
function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 100) { s = s + f(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return s % 100; }`},
	{"plain-alias", `struct P { xs: i32[], n: i32 }
function f(k: i32): i32 { let t: P = P { xs: [k, k + 1], n: k }; let v: P = t; return v.xs.len() + t.xs[1]; }
function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 100) { s = s + f(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return s % 100; }`},
	{"alias-in-a-conditional", `struct P { xs: i32[], n: i32 }
function f(k: i32): i32 {
    let t: P = P { xs: [k, k + 1], n: k };
    let r: i32 = 0;
    if (k % 2 == 0) { let v: P = t; r = v.xs.len() + v.n; }
    return r + t.xs[0];
}
function main(): i32 { let s: i32 = 0; let k: i32 = 0; while (k < 100) { s = s + f(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return s % 100; }`},
}

func TestSelfHostStructAliasRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

	for _, tc := range structAliasRebindCases {
		t.Run(tc.name, func(t *testing.T) {
			natV, natExit := nativeLeakVerdict(t, cli, dir, "sar_"+tc.name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, "sar_"+tc.name, tc.src)
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
