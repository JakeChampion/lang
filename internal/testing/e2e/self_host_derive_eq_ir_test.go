package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var deriveEqCases = []struct {
	name string
	main string
	want int
}{
	{"struct-i32", `import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32, y: i32 }
function main(): i32 { let a = P { x: 1, y: 2 }; let b = P { x: 1, y: 2 }; let c = P { x: 1, y: 9 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`, 42},
	{"struct-string", `import "core/cmp";
@derive(cmp.Eq)
struct S { name: string, n: i32 }
function main(): i32 { let a = S { name: "hi", n: 1 }; let b = S { name: "hi", n: 1 }; let c = S { name: "ho", n: 1 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`, 42},
	{"struct-nested", `import "core/cmp";
@derive(cmp.Eq)
struct Inner { v: i32 }
@derive(cmp.Eq)
struct Outer { inner: Inner, tag: i32 }
function main(): i32 { let a = Outer { inner: Inner { v: 5 }, tag: 1 }; let b = Outer { inner: Inner { v: 5 }, tag: 1 }; let c = Outer { inner: Inner { v: 6 }, tag: 1 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`, 42},
	{"enum-mixed", `import "core/cmp";
@derive(cmp.Eq)
enum E { A(i32), B(string), C }
function main(): i32 { if (A(5).eq(A(5)) && !A(5).eq(A(6)) && C.eq(C) && !A(5).eq(C) && B("x").eq(B("x"))) { return 42; } return 0; }`, 42},
	{"struct-operator", `import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32, y: i32 }
function main(): i32 { let a = P { x: 1, y: 2 }; let b = P { x: 1, y: 2 }; let c = P { x: 1, y: 9 }; if (a == b && a != c && !(a == c) && a == a) { return 42; } return 0; }`, 42},
	{"enum-operator", `import "core/cmp";
@derive(cmp.Eq)
enum E { A(i32), B(string), C }
function main(): i32 { if (A(5) == A(5) && A(5) != A(6) && C == C && A(5) != C && B("x") == B("x")) { return 42; } return 0; }`, 42},
	{"enum-local-operator", `import "core/cmp";
@derive(cmp.Eq)
enum E { A(i32), C }
function main(): i32 { let p = A(5); let q = A(5); let r = A(6); if (p == q && p != r) { return 42; } return 0; }`, 42},
}

func TestNativeDeriveEq(t *testing.T) {
	for _, tc := range deriveEqCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostDeriveEqIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range deriveEqCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostDeriveEqIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host derive-eq wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range deriveEqCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "derive_eq_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("derive-eq wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
