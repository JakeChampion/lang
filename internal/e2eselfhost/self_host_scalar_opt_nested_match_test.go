package e2eselfhost

import (
	"strings"
	"testing"
)

// A fresh SCALAR `Option` consumed by a match one block deeper is precise-dropped
// (#6319's class, scalar arm).
//
// #6127 gave the consuming match the scrutinee-is-a-borrow reading, but wired it
// to the rc-PAYLOAD kind alone — deliberately, "widens exactly one class at a
// time". For a scalar Option the coarse `body_unsafe_for` still read the
// scrutinee as an escape, so `precise_drop_names` refused the candidate and
// nothing else claimed it: `consumed_scalar_enum_frees` finds its consuming match
// by top-level statement INDEX and cannot see one nested in an `if`. The box
// leaked every round, frees=0, while the flat spelling was flat at 0.
//
// Disjointness needs no new gate, which is why this is the borrow reading rather
// than a widened lookup. `is_opt` is only ever set when
// `!body_has_top_level_match`, so precise-drop takes the shape exactly when the
// flat analysis cannot, and the flat analysis takes it exactly when precise-drop
// stands down. Widening BOTH would put two credits on one box — the failure mode
// #6480 shipped and CI caught as `__rc_underflow_count() == -1`.

// The fn-scoped nested spelling: precise_drop_names' own class, and the row this
// closes. `__rc_underflow_count()` is the return value, so an over-release shows
// up as a nonzero exit rather than as a byte count.
const scalarOptNestedIfSrc = `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32] = Some(i + 1);
    if (i >= 0) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The `while` body, the other nesting #6127's note names.
const scalarOptNestedWhileSrc = `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32] = Some(i + 1);
    var k: i32 = 0;
    while (k < 1) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The FLAT control, which the other analysis owns. It must stay balanced and must
// not gain a second credit from this change.
const scalarOptFlatSrc = `function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32] = Some(i + 1);
    match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The hazard: the arm binding is a scalar, but the OPTION itself is read again
// after the match, so the box is still live where the precise drop would land.
// `body_unsafe_for_match_borrow` re-reads only the scrutinee as a borrow — a
// second, non-scrutinee mention must still refuse.
const scalarOptUsedAfterSrc = `function olen(o: Option[i32]): i32 { match (o) { Some(a) => { return a; }, None => { return 0; } } }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[i32] = Some(i + 1);
    if (i >= 0) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    }
    return acc + olen(o);
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

func TestSelfHostScalarOptNestedMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string) (int64, int64, int64) {
		t.Helper()
		// NOT compared against `fern -interp` here, unlike the sibling reclaim
		// suites: these programs return `__rc_underflow_count()`, which the
		// interpreter has no rc runtime to answer, so it exits 1 on every one of
		// them. The oracle IS the constant 0 — the counter is the detector.
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != 0 {
			t.Fatalf("%s: __rc_underflow_count() == %d, want 0 — a box was released twice",
				name, exit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	for _, tc := range []struct{ name, src string }{
		{"nested_in_if", scalarOptNestedIfSrc},
		{"nested_in_while", scalarOptNestedWhileSrc},
		{"flat_control", scalarOptFlatSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. "+
					"The nested spelling leaked the box every round (4000 over 100) while "+
					"the flat control was 0", tc.name, allocs, frees, live)
			}
		})
	}

	t.Run("read_after_the_enclosing_if", func(t *testing.T) {
		allocs, frees, live := counts(t, "read_after_the_enclosing_if", scalarOptUsedAfterSrc)
		if frees != 0 || live == 0 {
			t.Errorf("read_after_the_enclosing_if: allocs=%d frees=%d live_bytes=%d — want "+
				"frees=0. The option is read again after the match, so the box is live "+
				"where the precise drop would land; the borrow reading re-reads only the "+
				"SCRUTINEE, and a second mention must still refuse", allocs, frees, live)
		}
	})
}
