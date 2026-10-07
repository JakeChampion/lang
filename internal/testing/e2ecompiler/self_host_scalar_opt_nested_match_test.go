package e2ecompiler

import (
	"strings"
	"testing"
)

// A fresh SCALAR `Option` consumed by a match one block deeper is released
// (#6319's class, scalar arm): the scrutinee is a borrow, so the box is freed
// after the match rather than leaked every round, the same as the flat
// spelling. Both an inline ctor and a CALL init are covered, nested and flat;
// a box released twice shows as `__rc_underflow_count() == -1`, which the
// `*_flat_control` rows would catch.

// The fn-scoped nested spelling. `__rc_underflow_count()` is the return value,
// so an over-release shows up as a nonzero exit rather than as a byte count.
const scalarOptNestedIfSrc = `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32] = Some(i + 1);
    if (i >= 0) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The `while` body, the other nesting #6127's note names.
const scalarOptNestedWhileSrc = `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32] = Some(i + 1);
    let k: i32 = 0;
    while (k < 1) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// Bound from a CALL to a producer that returns a fresh Option rather than from
// an inline ctor.
const scalarOptCallNestedIfSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i + 1);
}
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32] = mk(i);
    if (i >= 0) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The call-init FLAT control. It must be released exactly once, and only the
// underflow counter would show a second release.
const scalarOptCallFlatSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i + 1);
}
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32] = mk(i);
    match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The inline-ctor FLAT control. It must stay balanced and be released exactly
// once.
const scalarOptFlatSrc = `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32] = Some(i + 1);
    match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// BLOCK-scoped — the local declared inside a loop, with the match nested one
// deeper again. Both inits appear in ONE program deliberately: running the two
// locals' releases in the same block is what would surface an ordering bug
// between them.
const scalarOptBlockNestedSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i + 1);
}
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let o: Option[i32] = mk(k);
        if (k >= 0) {
            match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
        }
        let p: Option[i32] = Some(k + 7);
        if (k >= 0) {
            match (p) { Some(b) => { acc = acc + b; }, None => { acc = acc + 1; } }
        }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The block-scoped FLAT control — already worked, and must keep working with
// exactly one credit.
const scalarOptBlockFlatSrc = `function mk(i: i32): Option[i32] {
    if (i < 0) { return None; }
    return Some(i + 1);
}
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let o: Option[i32] = mk(k);
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
        k = k + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

// The hazard: the arm binding is a scalar, but the OPTION itself is read again
// after the match, so the box is still live where a drop inside the arm would
// land.
const scalarOptUsedAfterSrc = `function olen(o: Option[i32]): i32 { match (o) { Some(a) => { return a; }, None => { return 0; } } }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32] = Some(i + 1);
    if (i >= 0) {
        match (o) { Some(a) => { acc = acc + a; }, None => { acc = acc + 1; } }
    }
    return acc + olen(o);
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}
`

func TestSelfHostScalarOptNestedMatchX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
		{"call_init_nested_in_if", scalarOptCallNestedIfSrc},
		{"flat_control", scalarOptFlatSrc},
		{"call_init_flat_control", scalarOptCallFlatSrc},
		{"block_scoped_nested", scalarOptBlockNestedSrc},
		{"block_scoped_flat_control", scalarOptBlockFlatSrc},
		// The option is read again after the match, so the box is live where a
		// drop inside the arm would land; the underflow counter guards that.
		{"read_after_the_enclosing_if", scalarOptUsedAfterSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance",
					tc.name, allocs, frees, live)
			}
		})
	}

}
