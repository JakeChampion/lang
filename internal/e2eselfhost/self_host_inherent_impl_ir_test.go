package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// inherentImplIRCases exercise INHERENT impl blocks — `impl Type { … }` with no
// `for Trait` (issue #2700) — through the self-host IR path. The parser desugars
// an inherent impl exactly like a trait impl: a receiver-less function becomes
// an associated function (`Type.f(args)`), a `self`-taking one becomes an
// ordinary method, and `Self` rewrites to the impl type — but with no trait to
// match, so constructors/static methods can live on a type without inventing a
// dummy trait. Exit codes are the oracle; routing is pinned to "ir". Mirrors
// self_host_assoc_fn_ir_test.go (which covers the trait-impl form).
var inherentImplIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Inherent associated function (constructor): bind, then sum fields. 3+4=7.
	{"struct-ctor",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } } function main(): i32 { var p: Pt = Pt.make(3, 4); return p.x + p.y; }`, 7},
	// Inherent impl mixing an associated fn (`Self` return) and a `self` method.
	// make(3,4) then p.sum() = 7.
	{"struct-assoc-and-method",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Self { return Pt { x: a, y: b }; } function sum(self: Self): i32 { return self.x + self.y; } } function main(): i32 { var p: Pt = Pt.make(3, 4); return p.sum(); }`, 7},
	// Zero-arg inherent constructor (empty-params path). 0 + 0 + 9 = 9.
	{"struct-zero-arg",
		`struct Pt { x: i32, y: i32 } impl Pt { function origin(): Pt { return Pt { x: 0, y: 0 }; } } function main(): i32 { var p: Pt = Pt.origin(); return p.x + p.y + 9; }`, 9},
	// Two inherent associated fns on one type. make(2,3)=5; scaled(5)={5,10}=15.
	// 5 + 15 = 20.
	{"struct-multi-assoc",
		`struct Pt { x: i32, y: i32 } impl Pt { function make(a: i32, b: i32): Pt { return Pt { x: a, y: b }; } function scaled(a: i32): Pt { return Pt { x: a, y: a + a }; } } function main(): i32 { var p: Pt = Pt.make(2, 3); var q: Pt = Pt.scaled(5); return p.x + p.y + q.x + q.y; }`, 20},
	// Inherent associated function on an ENUM returning the enum (nominal). 7.
	{"enum-ctor",
		`enum E { A(i32), B } impl E { function tag(n: i32): E { if (n > 0) { return A(n); } return B; } } function val(e: E): i32 { match (e) { A(n) => { return n; }, B => { return 99; } } return 0; } function main(): i32 { return val(E.tag(7)); }`, 7},
}

// TestSelfHostInherentImplIRX86_64 routes each inherent-impl case through the
// self-hosted x86-64 driver and asserts the exit code, pinning routing to "ir".
func TestSelfHostInherentImplIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range inherentImplIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostInherentImplIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostInherentImplIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host inherent-impl wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range inherentImplIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "inherent_impl_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("inherent-impl wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
