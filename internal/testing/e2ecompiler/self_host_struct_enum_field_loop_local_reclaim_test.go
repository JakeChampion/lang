package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4357 (#4297 A2 follow-up): a reclaimable struct LOOP-LOCAL carrying a DIRECT enum
// field (`while { let t: Tagged = Tagged { e: Poly([i, i+1]), n: i }; }`) must release
// the enum field's VARIANT PAYLOAD at each loop rebind, not only the struct box: for
// each enum field the runtime variant dispatch drops the variant payload and frees the
// enum box before the struct box is freed.
//
// SOUNDNESS: that holds only when every enum field of the literal is a FRESH variant
// ctor (`e: Poly([..])` with a fresh array payload), so the old enum box + payload is
// sole-owned (rc=1). A NON-fresh (aliased bare-ident) enum field is retained by its
// owner, so freeing its payload would double-release — such a struct must leak
// instead (the ALIAS-SAFETY case proves __rc_underflow_count stays 0). A once-off
// final-box payload leak is bounded, so the fixpoint is flat across N.
//
// Gated on the self-host x86-64 IR path: FIXPOINT (bump growth equal at N=50 / N=5000)
// + OVER-RELEASE (the fresh payload is read each iteration) + ALIAS-SAFETY.

func structEnumFieldLoopLocalSrc(n string) string {
	return `enum Shape { Poly(i32[]), Dot }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) {
        let t: Tagged = Tagged { e: Poly([i, i + 1]), n: i };
        match (t.e) { Poly(xs) => { acc = acc + xs[0]; }, Dot => {} }
        i = i + 1;
    }
    if (acc < 0) { return 5; }
    // /8 keeps the bounded high-water (exactly 256 bytes here) off the 256-byte exit-
    // code wrap boundary — 256/8 = 32, a meaningful non-zero sanity value — while any
    // per-iteration leak still diverges N=50 vs N=5000.
    return ((__heap_bump_bytes() as i32) - before) / 8;
}`
}

// per iter reads xs[0..2] = i + (i+1) + (i+2) = 3i+3, plus t.n = i => 4i+3; sum over
// 0..199 = 4*19900 + 3*200 = 80200. A wrong free of the live payload corrupts the sum
// or trips __rc_underflow_count.
const structEnumFieldLoopLocalDetectorSrc = `enum Shape { Poly(i32[]), Dot }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let t: Tagged = Tagged { e: Poly([i, i + 1, i + 2]), n: i };
        match (t.e) { Poly(xs) => { acc = acc + xs[0] + xs[1] + xs[2]; }, Dot => {} }
        acc = acc + t.n;
        i = i + 1;
    }
    if (acc != 80200) { return 99; }
    return __rc_underflow_count();
}`

// A struct whose enum field is a bare IDENT (`e: shared`, shared a live enum local
// across the loop) must NOT be deep-dropped — that would double-release shared's
// payload, so the struct takes the leak-safe shallow path; __rc_underflow_count == 0
// proves no over-release. acc = 100*7 (xs[0] per iter) + 7 (final match) = 707.
const structEnumFieldAliasSafetySrc = `enum Shape { Poly(i32[]), Dot }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    let shared: Shape = Poly([7, 8, 9]);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 100) {
        let t: Tagged = Tagged { e: shared, n: i };
        match (t.e) { Poly(xs) => { acc = acc + xs[0]; }, Dot => {} }
        i = i + 1;
    }
    match (shared) { Poly(xs) => { acc = acc + xs[0]; }, Dot => {} }
    if (acc != 707) { return 99; }
    return __rc_underflow_count();
}`

// A struct whose enum field is a FRESH ctor but with a NON-fresh (aliased bare-ident)
// STRING payload (`m: Text(s)`, s a live string local): a non-fresh string payload is
// not sole-owned any more than an array one, so the struct takes the leak-safe shallow
// path rather than __fern_str_free the aliased string; s stays live (read after the
// loop). acc = 100*5 (v.len per iter) + 5 (s.len after) = 505;
// __rc_underflow_count == 0 proves no over-release of s.
const structEnumFieldStringPayloadAliasSrc = `enum Msg { Text(string), None }
struct W { m: Msg, n: i32 }
function main(): i32 {
    let s: string = "hello";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 100) {
        let t: W = W { m: Text(s), n: i };
        match (t.m) { Text(v) => { acc = acc + v.len(); }, None => {} }
        i = i + 1;
    }
    acc = acc + s.len();
    if (acc != 505) { return 99; }
    return __rc_underflow_count();
}`

func TestSelfHostStructEnumFieldLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	t.Run("fixpoint-bounded", func(t *testing.T) {
		small := run(t, "structenum-50", structEnumFieldLoopLocalSrc("50"))
		large := run(t, "structenum-5000", structEnumFieldLoopLocalSrc("5000"))
		if small != large {
			t.Errorf("struct-enum-field loop-local bump must be bounded: N=50 -> %d, N=5000 -> %d (variant payload leaked per iteration)", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	})

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "structenum-detector", structEnumFieldLoopLocalDetectorSrc); code != 0 {
			t.Errorf("struct-enum-field loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow_count)", code)
		}
	})

	t.Run("alias-safety", func(t *testing.T) {
		if code := run(t, "structenum-alias", structEnumFieldAliasSafetySrc); code != 0 {
			t.Errorf("struct-enum-field deep reclaim freed an ALIASED enum field (exit %d, 99=value mismatch, >0=__rc_underflow_count — shared payload double-released)", code)
		}
	})

	t.Run("string-payload-alias-safety", func(t *testing.T) {
		if code := run(t, "structenum-strpay", structEnumFieldStringPayloadAliasSrc); code != 0 {
			t.Errorf("struct-enum-field deep reclaim freed an ALIASED string payload (exit %d, 99=value mismatch, >0=__rc_underflow_count — the array-only freshness gate would have mis-freed s)", code)
		}
	})
}
