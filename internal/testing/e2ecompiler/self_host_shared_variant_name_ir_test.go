package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var sharedVariantNameCases = []struct {
	name string
	src  string
}{
	{"shared-unit-variant", `enum A1 { None, Xx(i32) }
enum B1 { None, Yy(i32) }
function main(): i32 { let a: A1 = A1.None; let b: B1 = B1.None; let r: i32 = 0;
    match (a) { A1.None => { r = r + 1; }, _ => {} }
    match (b) { B1.None => { r = r + 4; }, _ => {} }
    return r; }`},
	{"shared-unit-variant-non-builtin", `enum A3 { Zed, Xx(i32) }
enum B3 { Zed, Yy(i32) }
function main(): i32 { let a: A3 = A3.Zed; let b: B3 = B3.Zed; let r: i32 = 0;
    match (a) { A3.Zed => { r = r + 1; }, _ => {} }
    match (b) { B3.Zed => { r = r + 4; }, _ => {} }
    return r; }`},
	{"shared-payload-variant", `enum A2 { Wrap(i32), P }
enum B2 { Wrap(i32), Q }
function main(): i32 { let a: A2 = A2.Wrap(1); let b: B2 = B2.Wrap(4); let r: i32 = 0;
    match (a) { A2.Wrap(n) => { r = r + n; }, _ => {} }
    match (b) { B2.Wrap(n) => { r = r + n; }, _ => {} }
    return r; }`},
	{"second-enum-only", `enum A9 { None, Xx(i32) }
enum B9 { None, Yy(i32) }
function main(): i32 { let b: B9 = B9.None; match (b) { B9.None => { return 4; }, _ => { return 1; } } }`},
	{"shared-name-different-ordinals", `enum A5 { None, Xx(i32) }
enum B5 { Yy(i32), None }
function main(): i32 { let a: A5 = A5.None; let b: B5 = B5.None; let r: i32 = 0;
    match (a) { A5.None => { r = r + 1; }, _ => {} }
    match (b) { B5.None => { r = r + 4; }, _ => {} }
    return r; }`},
	{"single-enum-control", `enum S1 { None, Xx(i32) }
function main(): i32 { let a: S1 = S1.None; match (a) { S1.None => { return 1; }, _ => { return 9; } } }`},
}

func TestSelfHostSharedVariantNameIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range sharedVariantNameCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, prog)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
