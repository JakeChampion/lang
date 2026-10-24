package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostBorrowedFieldRetainIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (99 = over-release; other non-zero = a value read after the drop was wrong)", name, code, want)
		}
	}

	run(t, `struct H { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function mk(deps: string[]): H { return H { deps: deps }; }
function build(pre: string): i32 { let live: string[] = deps_of(pre); let h: H = mk(live); return h.deps.len() + live.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 6) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"borrowed-field-caller-drops", 0)

	run(t, `struct H { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mk(deps: string[]): H { return H { deps: deps }; }
function build(pre: string): i32 {
    let live: string[] = deps_of(pre);
    let seen: i32 = 0;
    if (live.len() > 0) { let h: H = mk(live); seen = h.deps.len(); }
    let junk1: string = fill(40);
    let junk2: string[] = deps_of(pre);
    if (junk1.len() < 0 || junk2.len() < 0) { return 0; }
    return seen + live.len() + live[0].len() + live[2].len();
}
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 92) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"borrowed-field-local-outlives-struct", 0)

	run(t, `struct H { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mk(deps: string[]): H { return H { deps: deps }; }
function build(pre: string): i32 {
    let live: string[] = deps_of(pre);
    let a: H = mk(live);
    let b: H = mk(live);
    let junk: string = fill(40);
    if (junk.len() < 0) { return 0; }
    return a.deps.len() + b.deps.len() + live[1].len();
}
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 49) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"borrowed-field-two-structs-one-array", 0)
}
