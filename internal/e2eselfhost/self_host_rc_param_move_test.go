package e2eselfhost

import "testing"

// A param aliased or destructured at its LAST mention is a move in the rc plan
// when the frame owns it — the #8498 widening of native movableAliasSource to
// owned params, ported as rc_ml_owned_rc_param. What the emitter does with a
// param move is decided per alias limb by the slot facts it already reads: the
// array limb acts on it, so the alias takes the param's one release and the
// retain/exit-dec pair cancels as native's does; the tuple / string / struct
// limbs read credits a param never holds, and the destructure never consults
// the plan, so those stay plan-only. Both are pinned here: the acted-on move
// must be leak-free AND underflow-free, and the plan-only move must leave the
// consumed-param runtime ownflag path intact on the path that never
// reassigned, with the caller still reading its box afterwards.
func TestSelfHostRcParamMoveX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		exit int
		// leakFree pins allocs == frees; the plan-only rows do not claim it.
		leakFree bool
	}{
		// `own` array param aliased at its last mention: the alias owns the
		// box and releases it at exit — no retain, no second dec, no leak.
		{"own-array-alias", `function f(own xs: i32[]): i32 {
	var ys = xs;
	return ys[0] + ys[1];
}
function main(): i32 {
	var a: i32 = f([1, 2]);
	var b: i32 = f([3, 4]);
	if (__rc_underflow() != 0) { return 99; }
	return a + b;
}`, 10, true},
		// The consumed-tuple shape of TestSelfHostRcPlanDiff: a reassigned
		// tuple param destructured at its last mention, called on both the
		// reassigning and the non-reassigning path.
		{"consumed-tuple-destructure", `function tup(t: (string, i32), re: boolean): i32 {
	if (re) { t = ("xyz", 10); }
	var (s, k) = t;
	return k + s.len();
}
function main(): i32 {
	var t: (string, i32) = ("a", 2);
	var a: i32 = tup(t, false);
	var b: i32 = tup(t, true);
	if (__rc_underflow() != 0) { return 99; }
	return a + b + t.1;
}`, 18, false},
		// The same with a bare alias instead of a destructure.
		{"consumed-tuple-alias", `function g(t: (i32[], i32), re: boolean): i32 {
	if (re) { t = ([7], 3); }
	var u = t;
	return u.0[0] + u.1;
}
function main(): i32 {
	var t: (i32[], i32) = ([1], 2);
	var a: i32 = g(t, false);
	var b: i32 = g(t, true);
	if (__rc_underflow() != 0) { return 99; }
	return a + b + t.0[0];
}`, 14, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "pmv_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.exit {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow)", tc.name, exit, tc.exit)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			t.Logf("%s: allocs=%d frees=%d live=%d", tc.name, allocs, frees, live)
			if tc.leakFree && (allocs != frees || live != 0) {
				t.Errorf("%s: allocs=%d frees=%d live=%d — the moved param's box must be released exactly once", tc.name, allocs, frees, live)
			}
		})
	}
}
