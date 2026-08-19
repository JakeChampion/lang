package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `@derive(cmp.Eq)` on the self-host IR path. The derive synthesises a
// field-/payload-wise `eq`. Emitting `self.f.eq(other.f)` for EVERY field
// resolves a scalar field's `.eq()` to `core/cmp`'s `i32.eq`, which the
// self-host loader skips (it loads only local + builtin), bailing the whole
// module to the legacy AST emitter. The synthesiser compares a
// SCALAR field with the primitive `==` (a nominal field still delegates to its
// own derived `.eq()`), so a derived `eq` lowers through the IR path with no
// stdlib dependency. `==` equals `core/cmp`'s scalar `eq` (`self == other`), so
// the native/self-host differential oracle still agrees.
var deriveEqCases = []struct {
	name string
	main string
	want int
}{
	// struct, i32 fields: eq true for equal, false when any field differs.
	{"struct-i32", `import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32, y: i32 }
function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 1, y: 2 }; var c = P { x: 1, y: 9 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`, 42},
	// struct with a string field (string is a scalar → `==`) + an i32 field.
	{"struct-string", `import "core/cmp";
@derive(cmp.Eq)
struct S { name: string, n: i32 }
function main(): i32 { var a = S { name: "hi", n: 1 }; var b = S { name: "hi", n: 1 }; var c = S { name: "ho", n: 1 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`, 42},
	// nested struct: the outer derive delegates to the inner struct's own
	// derived `.eq()` for the nominal field (exercising dv_eq_expr's else arm).
	{"struct-nested", `import "core/cmp";
@derive(cmp.Eq)
struct Inner { v: i32 }
@derive(cmp.Eq)
struct Outer { inner: Inner, tag: i32 }
function main(): i32 { var a = Outer { inner: Inner { v: 5 }, tag: 1 }; var b = Outer { inner: Inner { v: 5 }, tag: 1 }; var c = Outer { inner: Inner { v: 6 }, tag: 1 }; if (a.eq(b) && !a.eq(c)) { return 42; } return 0; }`, 42},
	// enum: payload variants compare their (scalar) payload; payload-less
	// variants compare by tag; cross-variant is unequal.
	{"enum-mixed", `import "core/cmp";
@derive(cmp.Eq)
enum E { A(i32), B(string), C }
function main(): i32 { if (A(5).eq(A(5)) && !A(5).eq(A(6)) && C.eq(C) && !A(5).eq(C) && B("x").eq(B("x"))) { return 42; } return 0; }`, 42},
	// The OPERATOR form of the same thing. `==` / `!=` on a struct or enum is
	// structural equality through the derived `eq`, not heap-pointer identity —
	// the self-host IR path used to lower the two box pointers into a plain i32
	// compare, so `a == b` on distinct-but-equal boxes was false and `a != c`
	// was true for every pair (#6009).
	{"struct-operator", `import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32, y: i32 }
function main(): i32 { var a = P { x: 1, y: 2 }; var b = P { x: 1, y: 2 }; var c = P { x: 1, y: 9 }; if (a == b && a != c && !(a == c) && a == a) { return 42; } return 0; }`, 42},
	// Enum operands: a fresh variant construction and a bare unit variant both
	// dispatch under the OWNING enum, so `Line(7) == Line(7)` is payload-wise.
	{"enum-operator", `import "core/cmp";
@derive(cmp.Eq)
enum E { A(i32), B(string), C }
function main(): i32 { if (A(5) == A(5) && A(5) != A(6) && C == C && A(5) != C && B("x") == B("x")) { return 42; } return 0; }`, 42},
	// An enum held in a LOCAL types as its variant, so the dispatch has to
	// redirect variant -> owning enum before naming the method.
	{"enum-local-operator", `import "core/cmp";
@derive(cmp.Eq)
enum E { A(i32), C }
function main(): i32 { var p = A(5); var q = A(5); var r = A(6); if (p == q && p != r) { return 42; } return 0; }`, 42},
}

// TestNativeDeriveEq runs the derived-Eq programs through the native
// interpreter + x86-64 + wasm backends, oracle-checked.
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

// TestSelfHostDeriveEqIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pins routing to "ir" (the synthesiser's scalar `==` keeps the
// derived eq IR-eligible), and oracle-checks the exit code.
func TestSelfHostDeriveEqIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

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

// TestSelfHostDeriveEqIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostDeriveEqIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host derive-eq wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range deriveEqCases {
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
