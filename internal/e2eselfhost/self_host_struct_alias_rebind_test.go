package e2eselfhost

import "testing"

// #8644: an alias of a struct local and a rebind of the source share one box.
// The alias took a box-only release and the source the deep one, a pairing that
// assumes the source outlives the alias. In a loop the source is rebound while
// the alias still holds the old box, so the rebind's shared arm handed the deep
// work to an alias that never does it, and every field the old box owned was
// stranded, one buffer per iteration. Both owners now take the rc-gated walk,
// and the alias's own loop rebind releases the box it held through the gated
// field reclaim. The other rows pin the shapes the old pairing did get right.
var structAliasRebindCases = []struct{ name, src string }{
	{"loop-alias-array-override", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    var s: S = S { ops: [1, 2, 3], n: 0 };
    var i: i32 = 0;
    while (i < 200) {
        var prev: S = s;
        s = S { ...s, ops: [4, 5, 6], n: s.n + 1 };
        i = i + 1;
    }
    return s.n;
}
function main(): i32 { var t: i32 = 0; var k: i32 = 0; while (k < 100) { t = t + run(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	{"loop-alias-scalar-override", `struct S { ops: i32[], n: i32 }
function run(x: i32): i32 {
    var s: S = S { ops: [1, 2, 3], n: 0 };
    var i: i32 = 0;
    while (i < 200) {
        var prev: S = s;
        s = S { ...s, n: s.n + 1 };
        i = i + 1;
    }
    return s.n;
}
function main(): i32 { var t: i32 = 0; var k: i32 = 0; while (k < 100) { t = t + run(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 100; }`},
	// The alias is read after the rebind, so the old box's fields must survive
	// until the alias's release.
	{"alias-read-after-rebind", `struct P { xs: i32[], n: i32 }
function f(k: i32): i32 {
    var t: P = P { xs: [k, k + 1], n: k };
    var keep: P = t;
    t = P { xs: [9, 9, 9], n: 1 };
    return keep.xs[1] + t.xs.len();
}
function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 100) { s = s + f(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return s % 100; }`},
	{"plain-alias", `struct P { xs: i32[], n: i32 }
function f(k: i32): i32 { var t: P = P { xs: [k, k + 1], n: k }; var v: P = t; return v.xs.len() + t.xs[1]; }
function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 100) { s = s + f(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return s % 100; }`},
	{"alias-in-a-conditional", `struct P { xs: i32[], n: i32 }
function f(k: i32): i32 {
    var t: P = P { xs: [k, k + 1], n: k };
    var r: i32 = 0;
    if (k % 2 == 0) { var v: P = t; r = v.xs.len() + v.n; }
    return r + t.xs[0];
}
function main(): i32 { var s: i32 = 0; var k: i32 = 0; while (k < 100) { s = s + f(k); k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } return s % 100; }`},
}

func TestSelfHostStructAliasRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
