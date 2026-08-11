package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// closureArrUniformCases pin the representation a fn-VALUED array carries when
// more than one literal reaches the same destination.
//
// A closure array has two representations, and the choice was made per LITERAL:
// an array of plain lambdas keeps bare `__lam_N` fn pointers (#3574), an array
// holding anything boxed — a capturing lambda, or a generic-passthrough element
// like `id(<lambda>)` — is given uniform env boxes and its consumers dispatch
// env-first. A function with two `return`s could therefore hand back one of
// each, and the caller's single binding got one dispatch ABI for both: it
// env-first-dispatched whichever arm ran. That compiled clean and SIGSEGV'd,
// so neither the bail count nor the strict-IR gate could see it (#6555).
//
// The rule is now per FUNCTION: if any returned array literal boxes, every
// fn-valued array literal in that function boxes. all-plain-returns-stay-bare is
// the guard on the other side — a function whose returns hold only plain lambdas
// keeps the bare fn-pointer representation, where env-first dispatch would be
// the same crash in the other direction.
var closureArrUniformCases = []struct {
	name string
	src  string
	exit int
}{
	// The two returns disagree: a plain lambda array, and an array whose element
	// is a passthrough call carrying a capturing lambda. Reduced from fernsmith
	// seed 215 — and then rewritten to CALL the value, which the reduced seed
	// never does, so it could not have shown the crash.
	{"cross-return-plain-and-passthrough", `function id[T](x: T): T { return x; } function gen(c: boolean, p1: i32): ((i32) => i32)[] { return if (c) { [((x: i32) => x)] } else { [id(((y: i32) => (y + p1)))] }; } function main(): i32 { var fs: ((i32) => i32)[] = gen(false, 5i32); return fs[0i32](1i32) & 63i32; }`, 6},
	// The same disagreement one container in: the arms of a value-position if
	// bound to a LOCAL, one holding a passthrough element and one a plain
	// lambda. This one bailed rather than crashing — the arm-array rewrite
	// counted only a direct capturing lambda, so nothing boxed at all.
	{"arm-array-passthrough-element", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; } function main(): i32 { var p: i32 = 4i32; var fs: ((i32) => i32)[] = (if (true) { [pick(true, ((x: i32) => (x + p)), ((y: i32) => y))] } else { [((z: i32) => z)] }); return fs[0i32](1i32) & 63i32; }`, 5},
	// The guard: nothing boxed anywhere, so both returns keep bare fn pointers.
	{"all-plain-returns-stay-bare", `function gen(c: boolean): ((i32) => i32)[] { return if (c) { [((x: i32) => x)] } else { [((y: i32) => (y + 1i32))] }; } function main(): i32 { var fs: ((i32) => i32)[] = gen(false); return fs[0i32](1i32) & 63i32; }`, 2},
}

// TestSelfHostClosureArrUniformIRX86_64 — the x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostClosureArrUniformIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureArrUniformCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostClosureArrUniformIRArm64 — the arm64 IR path.
func TestSelfHostClosureArrUniformIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureArrUniformCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostClosureArrUniformWasmIR — the wasm leg; the rule lives in
// irlower.fern, which every backend shares.
func TestSelfHostClosureArrUniformWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-array uniformity wasm IR e2e")
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

	for _, tc := range closureArrUniformCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := run.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}
