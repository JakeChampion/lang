package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The shapes all three legs below compile. Kept in one place so the x86-64,
// arm64 and wasm legs assert the SAME programs — the gap #6696 closed was a
// backend-independent lowering decision, so a leg drifting to its own program
// would stop being a parity check.
const (
	// churnEnumFieldFresh: a struct whose direct enum field is a FRESH variant
	// ctor with an array payload, built and dropped once per CALL (not per loop
	// rebind — the rebind chain is a different emitter and was already exact).
	// The payload array is the value that leaked once per construction.
	churnEnumFieldFresh = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    var s: S = S { v: A([9, 8, 7]), n: i };
    var got: i32 = 0;
    match (s.v) { A(p) => { got = p[0]; }, _ => {} }
    return got;
}
function drive(n: i32): i32 {
    var bad: i32 = 0; var i: i32 = 0;
    while (i < n) { if (mk(i) != 9) { bad = 1; } i = i + 1; }
    return bad;
}
`

	// churnEnumFieldAliasedBox: the enum field is bound from a LIVE enum local,
	// so construction alias-inc'd the box and this struct is not its sole owner.
	// The __fern_rc_is_unique gate must decline the payload release here — `e`
	// still reads its payload after `s` is swept, and `e`'s own drop releases it.
	churnEnumFieldAliasedBox = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    var e: V = A([1, 2, 3]);
    var s: S = S { v: e, n: i };
    var bad: i32 = 0;
    match (s.v) { A(p) => { if (p.len() != 3) { bad = 1; } }, _ => { bad = 2; } }
    match (e) { A(q) => { if (q[0] != 1) { bad = bad + 4; } }, _ => { bad = bad + 8; } }
    return bad;
}
function drive(n: i32): i32 {
    var bad: i32 = 0; var i: i32 = 0;
    while (i < n) { if (mk(i) != 0) { bad = 1; } i = i + 1; }
    return bad;
}
`

	// churnEnumFieldAliasedPayload: the payload ARRAY aliases a live local. This
	// one DOES release (the box is sole-owned), and stays balanced because a
	// bare-ident array payload rides the variant-construction alias-inc — the
	// reason the admission needs no per-construction freshness proof for arrays.
	churnEnumFieldAliasedPayload = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    var a: i32[] = [4, 5, 6];
    var s: S = S { v: A(a), n: i };
    var bad: i32 = 0;
    match (s.v) { A(p) => { if (p.len() != 3) { bad = 1; } }, _ => { bad = 2; } }
    if (a[1] != 5) { bad = bad + 4; }
    return bad;
}
function drive(n: i32): i32 {
    var bad: i32 = 0; var i: i32 = 0;
    while (i < n) { if (mk(i) != 0) { bad = 1; } i = i + 1; }
    return bad;
}
`

	// churnEnumFieldBaseCopy: `S { ...t1, n: 2 }` copies the enum field from the
	// base, so t2.v ALIASES t1.v under a base-copy retain. Both sweeps must
	// decline the payload release; releasing on either side frees a payload the
	// other still reads.
	churnEnumFieldBaseCopy = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    var t1: S = S { v: A([5, 6, 7]), n: i };
    var t2: S = S { ...t1, n: 2 };
    var bad: i32 = 0;
    match (t2.v) { A(p) => { if (p[2] != 7) { bad = 1; } }, _ => { bad = 2; } }
    match (t1.v) { A(q) => { if (q[0] != 5) { bad = bad + 4; } }, _ => { bad = bad + 8; } }
    return bad;
}
function drive(n: i32): i32 {
    var bad: i32 = 0; var i: i32 = 0;
    while (i < n) { if (mk(i) != 0) { bad = 1; } i = i + 1; }
    return bad;
}
`
)

// heapFlatMain drives `drive` at a small and a large count and compares the heap
// bump across the two, so the assertion is on GROWTH rather than on an absolute
// figure: a fixed one-time cost (freelist warm-up) cancels, and only a per-call
// residual moves the number. Exit 0 = flat, 1 = grew (leaked), 90/91 = the
// program's own result went wrong, 99 = an over-release ticked the RC underflow
// detector. Cast as a differential the same way __heap_bump_bytes is used
// elsewhere in this package.
func heapFlatMain(iters string) string {
	return `function main(): i32 {
    if (drive(50) != 0) { return 90; }
    var lo: i32 = __heap_bump_bytes();
    if (drive(` + iters + `) != 0) { return 91; }
    var hi: i32 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    var d: i32 = hi - lo;
    if (d != 0) { return 1; }
    return 0;
}`
}

// balancedMain is heapFlatMain without the growth check, for the shapes the
// admission deliberately DECLINES: an aliased or base-copied enum field is not
// sole-owned, so it keeps today's shallow path and its payload still leaks. Their
// contract is that declining is SAFE — the reads after the sweep still see live
// data (90/91) and nothing was released twice (99) — not that they are flat.
// Asserting flatness here would assert a leak that is out of scope for #6696 and
// would make this file fail for a reason it does not own.
func balancedMain(iters string) string {
	return `function main(): i32 {
    if (drive(50) != 0) { return 90; }
    if (drive(` + iters + `) != 0) { return 91; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`
}

// TestSelfHostStructEnumFieldPayloadDropIRX86_64 pins #6696: the VARIANT PAYLOAD
// of a struct's direct enum field is released when the struct is swept.
//
// __struct_drop_<T>'s k_enum arm is a box-only dec, so before this the payload
// array outlived the struct — 48 bytes per construction, unbounded in the call
// count, while the same enum spelled as a LOCAL was exact (lowering deep-drops
// that one via emit_enum_variant_drops). The sweep now runs the same variant
// dispatch over the field's box before handing it to __struct_drop_.
//
// The four shapes are the admission's corners: a fresh sole-owned field (must
// reclaim), an aliased BOX and a base-copied field (the __fern_rc_is_unique gate
// must decline — releasing there frees a payload another owner still reads), and
// an aliased PAYLOAD (must reclaim AND stay balanced, since a bare-ident array
// payload rides the variant-construction alias-inc). Growth proves the reclaim;
// exit 99 would catch the over-release the aliased shapes risk.
func TestSelfHostStructEnumFieldPayloadDropIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("%s exited %d, want 0 (1 = heap grew, so the enum field's payload was not reclaimed; 99 = over-release; 90/91 = wrong result)", name, code)
		}
	}

	flat, balanced := heapFlatMain("200000"), balancedMain("200000")
	run(t, churnEnumFieldFresh+flat, "enum-field-fresh-payload-reclaimed")
	run(t, churnEnumFieldAliasedPayload+flat, "enum-field-aliased-payload-balanced")
	run(t, churnEnumFieldAliasedBox+balanced, "enum-field-aliased-box-balanced")
	run(t, churnEnumFieldBaseCopy+balanced, "enum-field-base-copy-balanced")
}

// TestSelfHostStructEnumFieldPayloadDropIRArm64 is the arm64 leg of #6696. The
// reclaim decision is the shared irlower sweep, so arm64 gets it from the same
// change — this proves the emitted arm64 asm actually carries it. The iteration
// count is cut for qemu; the leak was per CALL, so it is still visible.
func TestSelfHostStructEnumFieldPayloadDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("%s exited %d, want 0 (1 = heap grew, so the enum field's payload was not reclaimed; 99 = over-release; 90/91 = wrong result)", name, code)
		}
	}

	flat, balanced := heapFlatMain("20000"), balancedMain("20000")
	run(t, churnEnumFieldFresh+flat, "enum-field-fresh-payload-reclaimed")
	run(t, churnEnumFieldAliasedPayload+flat, "enum-field-aliased-payload-balanced")
	run(t, churnEnumFieldAliasedBox+balanced, "enum-field-aliased-box-balanced")
	run(t, churnEnumFieldBaseCopy+balanced, "enum-field-base-copy-balanced")
}

// TestSelfHostStructEnumFieldPayloadDropWasm is the wasm leg of #6696, and it
// needed a second fix to go green: wasm's $__struct_drop_ had NO enum-field arm
// at all, so where x86-64/arm64 shallow-freed the enum box and leaked only its
// payload, wasm leaked the box too. Releasing the payload alone left the shape
// still growing, so the wasm sibling of the register backends' k_enum arm landed
// with it. The WAT assertion pins the emitted arm; a payload-only fix would keep
// the growth check red on this leg while the other two passed.
func TestSelfHostStructEnumFieldPayloadDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum-field payload-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	run := func(t *testing.T, prog, name string, wantWat string) {
		t.Helper()
		wat := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(wat) == 0 {
			t.Fatalf("%s: wasm emitter produced 0 bytes", name)
		}
		if wantWat != "" && !strings.Contains(string(wat), wantWat) {
			t.Fatalf("%s: emitted WAT missing %q", name, wantWat)
		}
		watPath := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("%s exited %d, want 0 (1 = heap grew, so the enum field did not reclaim; 99 = over-release; 90/91 = wrong result)", name, code)
		}
	}

	flat, balanced := heapFlatMain("200000"), balancedMain("200000")
	// $__struct_drop_S is what carries the new k_enum arm; its absence would mean
	// the struct is not swept at all and the growth check proves nothing.
	run(t, churnEnumFieldFresh+flat, "enum-field-fresh-payload-reclaimed", "$__struct_drop_S")
	run(t, churnEnumFieldAliasedPayload+flat, "enum-field-aliased-payload-balanced", "")
	run(t, churnEnumFieldAliasedBox+balanced, "enum-field-aliased-box-balanced", "")
	run(t, churnEnumFieldBaseCopy+balanced, "enum-field-base-copy-balanced", "")
}
