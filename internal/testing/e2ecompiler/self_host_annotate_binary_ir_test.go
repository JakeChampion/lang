package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var annotateBinaryCases = []struct {
	name string
	src  string
}{
	{"overload_add_struct", `import "core/ops";
struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
impl ops.Add for V {}
function main(): i32 {
  let p: V = V { x: 20 };
  let q: V = V { x: 22 };
  return (p + q).x;
}`},
	{"method_call_control", `struct V { x: f64, y: f64 }
function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
function main(): i32 {
  let p: V = V { x: 3.0, y: 4.0 };
  let q: V = V { x: 2.0, y: 5.0 };
  let d: f64 = p.mul(q);
  return (d * 2.0) as i32;
}`},
	{"plain_f64_arithmetic", `function main(): i32 {
  let a: f64 = 3.5;
  let b: f64 = 2.0;
  return ((a * b) + 1.0) as i32;
}`},
	{"plain_i64_arithmetic", `function main(): i32 {
  let a: i64 = 5000000000i64;
  return ((a + 7i64) % 100i64) as i32;
}`},
}

func TestSelfHostAnnotateBinaryX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateBinaryCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a composite operator overload must type from ExprBinary.ty / ExprUnary.ty", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostAnnotateBinaryWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-binary wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateBinaryCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

var annotateBinaryRejectCases = []struct {
	name string
	code string
	src  string
}{
	{"binary_without_method", "E009", `struct W { x: i32 }
function main(): i32 {
  let a: W = W { x: 1 };
  let b: W = W { x: 2 };
  let c: W = a + b;
  return c.x;
}`},
	{"binary_with_a_scalar_result", "E021", `import "core/ops";
struct V { x: f64, y: f64 }
function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
impl ops.Mul for V {}
function main(): i32 {
  let p: V = V { x: 3.0, y: 4.0 };
  return (p * p) as i32;
}`},
	{"unary_without_method", "E009", `struct W { x: i32 }
function main(): i32 {
  let a: W = W { x: 1 };
  let c: W = -a;
  return c.x;
}`},
}

func TestSelfHostAnnotateBinaryRejectsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateBinaryRejectCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			out, _ := runX86_64Bin(runner, fernBin, "-check", mainPath, stdlibRoot).CombinedOutput()
			if !strings.Contains(string(out), tc.code) {
				t.Errorf("%s: want %s from the self-host checker, got:\n%s", tc.name, tc.code, out)
			}
		})
	}
}
