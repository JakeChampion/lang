package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4357 (#4297 A2 follow-up): a reclaimable struct LOOP-LOCAL carrying a DIRECT enum
// field (`while { var t: Tagged = Tagged { e: Poly([i, i+1]), n: i }; }`) leaked the
// enum field's VARIANT PAYLOAD every iteration. The struct is admitted to the reclaim
// set (struct_has_reclaim_array_field admits a direct enum field since #4297 A2), but
// its loop-rebind routed through the array-only __field_reclaim path, which skips enum
// fields entirely — the enum box AND its payload array leaked per iteration; the exit
// sweep's k_enum arm is only a shallow box-only dec. The fix routes such a binding
// through emit_struct_enum_deep_reinit_store: for each enum field it runs the runtime
// variant-dispatch deep-drop (emit_enum_variant_drops → drop the variant payload, free
// the enum box) before freeing the struct box.
//
// SOUNDNESS: it fires ONLY when every enum field of the literal is a FRESH variant ctor
// (struct_lit_all_enum_fields_fresh — `e: Poly([..])` with a fresh array payload), so
// old's enum box + payload is sole-owned (rc=1, no construction alias-inc). A NON-fresh
// (aliased bare-ident) enum field is retained + alias-inc'd by its owner, so freeing its
// payload would double-release — such a struct is rejected at the gate and rides the
// leak-safe shallow path (the ALIAS-SAFETY case proves __rc_underflow stays 0). The exit
// sweep is deliberately NOT widened; only the per-iteration rebind reclaim is added, so
// the once-off final-box payload leak stays bounded (the fixpoint is flat across N).
//
// Gated on the self-host x86-64 IR path: FIXPOINT (bump growth equal at N=50 / N=5000)
// + OVER-RELEASE (the fresh payload is read each iteration) + ALIAS-SAFETY.

func structEnumFieldLoopLocalSrc(n string) string {
	return `enum Shape { Poly(i32[]), Dot }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) {
        var t: Tagged = Tagged { e: Poly([i, i + 1]), n: i };
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
// or trips __rc_underflow.
const structEnumFieldLoopLocalDetectorSrc = `enum Shape { Poly(i32[]), Dot }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        var t: Tagged = Tagged { e: Poly([i, i + 1, i + 2]), n: i };
        match (t.e) { Poly(xs) => { acc = acc + xs[0] + xs[1] + xs[2]; }, Dot => {} }
        acc = acc + t.n;
        i = i + 1;
    }
    if (acc != 80200) { return 99; }
    return __rc_underflow();
}`

// A struct whose enum field is a bare IDENT (`e: shared`, shared a live enum local
// across the loop) must NOT be deep-dropped — that would double-release shared's
// payload. The gate (struct_lit_all_enum_fields_fresh) rejects the non-fresh field, so
// the struct rides the leak-safe shallow path; __rc_underflow == 0 proves no over-
// release. acc = 100*7 (xs[0] per iter) + 7 (final match) = 707.
const structEnumFieldAliasSafetySrc = `enum Shape { Poly(i32[]), Dot }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    var shared: Shape = Poly([7, 8, 9]);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 100) {
        var t: Tagged = Tagged { e: shared, n: i };
        match (t.e) { Poly(xs) => { acc = acc + xs[0]; }, Dot => {} }
        i = i + 1;
    }
    match (shared) { Poly(xs) => { acc = acc + xs[0]; }, Dot => {} }
    if (acc != 707) { return 99; }
    return __rc_underflow();
}`

// A struct whose enum field is a FRESH ctor but with a NON-fresh (aliased bare-ident)
// STRING payload (`m: Text(s)`, s a live string local): the freshness gate keys off
// fresh_rcpayload_enum_init -> variant_struct_payloads_fresh, which rejects a non-fresh
// STRING payload (not just array payloads — the array-only check would wrongly admit
// this and __fern_str_free the aliased string). So the struct rides the leak-safe
// shallow path; s stays live (read after the loop). acc = 100*5 (v.len per iter) + 5
// (s.len after) = 505; __rc_underflow == 0 proves no over-release of s.
const structEnumFieldStringPayloadAliasSrc = `enum Msg { Text(string), None }
struct W { m: Msg, n: i32 }
function main(): i32 {
    var s: string = "hello";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 100) {
        var t: W = W { m: Text(s), n: i };
        match (t.m) { Text(v) => { acc = acc + v.len(); }, None => {} }
        i = i + 1;
    }
    acc = acc + s.len();
    if (acc != 505) { return 99; }
    return __rc_underflow();
}`

func TestSelfHostStructEnumFieldLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
			t.Errorf("struct-enum-field loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow)", code)
		}
	})

	t.Run("alias-safety", func(t *testing.T) {
		if code := run(t, "structenum-alias", structEnumFieldAliasSafetySrc); code != 0 {
			t.Errorf("struct-enum-field deep reclaim freed an ALIASED enum field (exit %d, 99=value mismatch, >0=__rc_underflow — shared payload double-released)", code)
		}
	})

	t.Run("string-payload-alias-safety", func(t *testing.T) {
		if code := run(t, "structenum-strpay", structEnumFieldStringPayloadAliasSrc); code != 0 {
			t.Errorf("struct-enum-field deep reclaim freed an ALIASED string payload (exit %d, 99=value mismatch, >0=__rc_underflow — the array-only freshness gate would have mis-freed s)", code)
		}
	})
}
