package e2ecompiler

import (
	"strings"
	"testing"
)

const raeScalarEnumSrc = `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let e: E = Box(i, i);
    match (e) { Box(a, b) => { return a + b; }, Nil => { return 0; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

const raeScalarEnumFallthroughSrc = `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let e: E = Box(i, i);
    let t: i32 = 0;
    match (e) { Box(a, b) => { t = a + b; }, Nil => { t = 0; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t % 7;
}`

const raeScalarOptionSrc = `function round(i: i32): i32 {
    let o: Option[i32] = Some(i);
    match (o) { Some(a) => { return a; }, None => { return 0; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

const raeRcPayloadEnumSrc = `enum E { Box(i32[], i32), Nil }
function round(i: i32): i32 {
    let e: E = Box([i, i + 1], i);
    match (e) { Box(_, b) => { return b; }, Nil => { return 0; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

const raeMovedPayloadSrc = `enum E { Box(i32[], i32), Nil }
function mk(i: i32): i32[] {
    let e: E = Box([i, i + 1], i);
    match (e) { Box(a, b) => { return a; }, Nil => { return []; } }
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let v: i32[] = mk(r); t = t + v[0] + v[1]; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

const raeMixedPathsSrc = `enum E { Box(i32, i32), Nil }
function round(i: i32): i32 {
    let e: E = Box(i, i);
    let t: i32 = 0;
    match (e) { Box(a, b) => { if (a > 50) { return a + b; } t = a; }, Nil => { t = 0; } }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 7;
}`

const raeLoopBlockSrc = `enum E { Box(i32, i32), Nil }
function round(n: i32): i32 {
    let r: i32 = 0;
    while (r < n) {
        let e: E = Box(r, r);
        match (e) { Box(a, b) => { if (a + b > 400) { return a; } }, Nil => {} }
        r = r + 1;
    }
    return 0;
}
function main(): i32 {
    if (__rc_underflow_count() != 0) { return 99; }
    return round(300) % 7;
}`

func TestSelfHostReturningArmEnumFreeX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit == 99 {
			t.Fatalf("%s: __rc_underflow_count() != 0 — the box was released more than once", name)
		}
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d — the release reached a live value", name, exit, wantExit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
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

	for _, tc := range []struct {
		name       string
		src        string
		wantExit   int
		wantAllocs int64
	}{
		{"scalar-enum-returning-arms", raeScalarEnumSrc, 2, 100},
		{"scalar-enum-fallthrough-control", raeScalarEnumFallthroughSrc, 2, 100},
		{"scalar-option-returning-arms", raeScalarOptionSrc, 1, 100},
		{"rcpayload-enum-returning-arms", raeRcPayloadEnumSrc, 1, 200},
		{"rcpayload-moved-out-of-arm", raeMovedPayloadSrc, 4, 200},
		{"mixed-return-and-fallthrough", raeMixedPathsSrc, 1, 100},
		{"loop-block-returning-arm", raeLoopBlockSrc, 5, 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.wantExit)
			if allocs != tc.wantAllocs {
				t.Errorf("%s: allocs=%d, want %d — the probe is no longer allocating the shape it measures",
					tc.name, allocs, tc.wantAllocs)
			}
			if allocs != frees || live != 0 {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d — want an exact balance. frees=0 is the "+
					"#6219 leak (the post-match release is dead code when the arm returns); frees > allocs "+
					"means the pending release and the post-match release both ran on one path",
					tc.name, allocs, frees, live)
			}
		})
	}
}
