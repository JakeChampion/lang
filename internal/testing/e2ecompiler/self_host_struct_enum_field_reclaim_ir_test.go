package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructEnumFieldReclaimIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
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
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (137 = heap exhausted → enum field not reclaimed; 99 = over-release)", name, code, want)
		}
	}

	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let t: Tagged = Tagged { e: Rect(i), n: i }; match (t.e) { Rect(v) => { if (v != i) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-enum-field-fresh-gate-churn", 0)

	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let s: Shape = Rect(id([7])[0]); let t: Tagged = Tagged { e: s, n: 1 }; match (t.e) { Rect(v) => { if (v != 7) { bad = 1; } }, _ => { bad = 1; } } match (s) { Rect(w) => { if (w != 7) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-enum-field-aliased-balanced", 0)

	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let s: Shape = Rect(id([9])[0]); let t1: Tagged = Tagged { e: s, n: 1 }; let t2: Tagged = Tagged { ...t1, n: 2 }; match (t2.e) { Rect(v) => { if (v != 9) { bad = 1; } }, _ => { bad = 1; } } match (t1.e) { Rect(w) => { if (w != 9) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-enum-field-base-copy-balanced", 0)

	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, items: i32[] }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let t: Tagged = Tagged { e: Rect(i), items: [1, 2, 3] }; if (t.items.len() != 3) { bad = 1; } match (t.e) { Rect(v) => { if (v != i) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-enum-field-with-array-churn", 0)
}
