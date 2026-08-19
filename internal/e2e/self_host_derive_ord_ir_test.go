package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `@derive(Ord)` on the self-host IR path. Like the derived `eq`, the
// synthesised `cmp` emitted `self.f.cmp(other.f)` for every field; a scalar
// field's `.cmp()` resolves to core/cmp's `i32.cmp` (etc.), which the self-host
// loader never loads (local + builtin only), so the module bailed to the legacy
// AST emitter. The synthesiser now inlines core/cmp's numeric Ord body for a
// NUMERIC-scalar field/payload (`vn = -1 / 1 / 0` via `<` / `>`), keeping
// `.cmp()` delegation for nominal fields. The inline form matches core/cmp's
// numeric `cmp` exactly, so the native/self-host differential oracle still
// agrees. (A string field's Ord routes through sort.string_cmp, not a primitive
// `<`, so a string-keyed derive(Ord) is the one shape that still falls back —
// it stays correct on the native backends, just not on the self-host IR path.)
var deriveOrdCases = []struct {
	name string
	main string
	want int
}{
	// struct, i32 fields: lexicographic cmp (first differing field wins).
	{"struct-i32", `import "core/cmp";
@derive(cmp.Ord)
struct P { x: i32, y: i32 }
function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 1, y: 3 }; var c = P { x: 1, y: 2 }; if (a.cmp(b) < 0 && b.cmp(a) > 0 && a.cmp(c) == 0) { return 42; } return 0; }`, 42},
	// struct with a mix of numeric widths.
	{"struct-i64", `import "core/cmp";
@derive(cmp.Ord)
struct Q { a: i64, b: i32 }
function main(): i32 { var x = Q { a: 5, b: 1 }; var y = Q { a: 5, b: 2 }; var z = Q { a: 9, b: 0 }; if (x.cmp(y) < 0 && z.cmp(x) > 0 && x.cmp(x) == 0) { return 42; } return 0; }`, 42},
	// nested struct: the outer cmp delegates to the inner struct's own derived
	// `.cmp()` for the nominal field (exercises dv_cmp_stmts's non-numeric arm).
	{"struct-nested", `import "core/cmp";
@derive(cmp.Ord)
struct Inner { v: i32 }
@derive(cmp.Ord)
struct Outer { inner: Inner, tag: i32 }
function main(): i32 { var a = Outer { inner: Inner { v: 5 }, tag: 1 }; var b = Outer { inner: Inner { v: 6 }, tag: 0 }; var c = Outer { inner: Inner { v: 5 }, tag: 1 }; if (a.cmp(b) < 0 && b.cmp(a) > 0 && a.cmp(c) == 0) { return 42; } return 0; }`, 42},
	// enum: variant order by declaration index; same variant compares payload.
	{"enum-numeric", `import "core/cmp";
@derive(cmp.Ord)
enum E { A(i32), B(i32), C }
function main(): i32 { if (A(1).cmp(A(2)) < 0 && A(5).cmp(B(0)) < 0 && C.cmp(A(0)) > 0 && A(3).cmp(A(3)) == 0) { return 42; } return 0; }`, 42},
	// The OPERATOR form: `a < b` is `a.cmp(b) < 0`. The self-host IR path used
	// to compare the two box POINTERS instead, so the answer was the boxes'
	// relative allocation addresses — which for equal values made `a < c` true
	// and `a >= c` false (#6009).
	{"struct-operator", `import "core/cmp";
@derive(cmp.Ord)
struct P { x: i32, y: i32 }
function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 1, y: 5 }; var c = P { x: 1, y: 2 }; if (a < b && b > a && a <= c && a >= c && !(a < c) && !(c > a)) { return 42; } return 0; }`, 42},
	// Enum operands, both freshly constructed and held in locals.
	{"enum-operator", `import "core/cmp";
@derive(cmp.Ord)
enum E { A(i32), B(i32), C }
function main(): i32 { var p = A(3); if (A(1) < A(2) && A(5) < B(0) && C > A(0) && !(A(3) < A(3)) && p <= A(3) && p >= A(3)) { return 42; } return 0; }`, 42},
}

// TestNativeDeriveOrd runs the derived-Ord programs through the native
// interpreter + x86-64 + wasm backends, oracle-checked.
func TestNativeDeriveOrd(t *testing.T) {
	for _, tc := range deriveOrdCases {
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

// TestSelfHostDeriveOrdIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pins routing to "ir" (the synthesiser's inline numeric compare
// keeps the derived cmp IR-eligible), and oracle-checks the exit code.
func TestSelfHostDeriveOrdIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range deriveOrdCases {
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

// TestSelfHostDeriveOrdIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostDeriveOrdIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host derive-ord wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range deriveOrdCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "derive_ord_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("derive-ord wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
