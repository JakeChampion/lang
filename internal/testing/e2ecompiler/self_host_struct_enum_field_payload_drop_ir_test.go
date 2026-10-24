package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	churnEnumFieldFresh = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    let s: S = S { v: A([9, 8, 7]), n: i };
    let got: i32 = 0;
    match (s.v) { A(p) => { got = p[0]; }, _ => {} }
    return got;
}
function drive(n: i32): i32 {
    let bad: i32 = 0; let i: i32 = 0;
    while (i < n) { if (mk(i) != 9) { bad = 1; } i = i + 1; }
    return bad;
}
`

	churnEnumFieldAliasedBox = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    let e: V = A([1, 2, 3]);
    let s: S = S { v: e, n: i };
    let bad: i32 = 0;
    match (s.v) { A(p) => { if (p.len() != 3) { bad = 1; } }, _ => { bad = 2; } }
    match (e) { A(q) => { if (q[0] != 1) { bad = bad + 4; } }, _ => { bad = bad + 8; } }
    return bad;
}
function drive(n: i32): i32 {
    let bad: i32 = 0; let i: i32 = 0;
    while (i < n) { if (mk(i) != 0) { bad = 1; } i = i + 1; }
    return bad;
}
`

	churnEnumFieldAliasedPayload = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    let a: i32[] = [4, 5, 6];
    let s: S = S { v: A(a), n: i };
    let bad: i32 = 0;
    match (s.v) { A(p) => { if (p.len() != 3) { bad = 1; } }, _ => { bad = 2; } }
    if (a[1] != 5) { bad = bad + 4; }
    return bad;
}
function drive(n: i32): i32 {
    let bad: i32 = 0; let i: i32 = 0;
    while (i < n) { if (mk(i) != 0) { bad = 1; } i = i + 1; }
    return bad;
}
`

	churnEnumFieldBaseCopy = `enum V { A(i32[]), B }
struct S { v: V, n: i32 }
function mk(i: i32): i32 {
    let t1: S = S { v: A([5, 6, 7]), n: i };
    let t2: S = S { ...t1, n: 2 };
    let bad: i32 = 0;
    match (t2.v) { A(p) => { if (p[2] != 7) { bad = 1; } }, _ => { bad = 2; } }
    match (t1.v) { A(q) => { if (q[0] != 5) { bad = bad + 4; } }, _ => { bad = bad + 8; } }
    return bad;
}
function drive(n: i32): i32 {
    let bad: i32 = 0; let i: i32 = 0;
    while (i < n) { if (mk(i) != 0) { bad = 1; } i = i + 1; }
    return bad;
}
`
)

func heapFlatMain(iters string) string {
	return `function main(): i32 {
    if (drive(50) != 0) { return 90; }
    let lo: i32 = (__heap_bump_bytes() as i32);
    if (drive(` + iters + `) != 0) { return 91; }
    let hi: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    let d: i32 = hi - lo;
    if (d != 0) { return 1; }
    return 0;
}`
}

func TestSelfHostStructEnumFieldPayloadDropIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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

	flat := heapFlatMain("200000")
	run(t, churnEnumFieldFresh+flat, "enum-field-fresh-payload-reclaimed")
	run(t, churnEnumFieldAliasedPayload+flat, "enum-field-aliased-payload-balanced")
	run(t, churnEnumFieldAliasedBox+flat, "enum-field-aliased-box-balanced")
	run(t, churnEnumFieldBaseCopy+flat, "enum-field-base-copy-balanced")
}

func TestSelfHostStructEnumFieldPayloadDropIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

	flat := heapFlatMain("20000")
	run(t, churnEnumFieldFresh+flat, "enum-field-fresh-payload-reclaimed")
	run(t, churnEnumFieldAliasedPayload+flat, "enum-field-aliased-payload-balanced")
	run(t, churnEnumFieldAliasedBox+flat, "enum-field-aliased-box-balanced")
	run(t, churnEnumFieldBaseCopy+flat, "enum-field-base-copy-balanced")
}

func TestSelfHostStructEnumFieldPayloadDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum-field payload-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

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

	flat := heapFlatMain("200000")
	run(t, churnEnumFieldFresh+flat, "enum-field-fresh-payload-reclaimed", "$__sem_drop_S")
	run(t, churnEnumFieldAliasedPayload+flat, "enum-field-aliased-payload-balanced", "")
	run(t, churnEnumFieldAliasedBox+flat, "enum-field-aliased-box-balanced", "")
	run(t, churnEnumFieldBaseCopy+flat, "enum-field-base-copy-balanced", "")
}
