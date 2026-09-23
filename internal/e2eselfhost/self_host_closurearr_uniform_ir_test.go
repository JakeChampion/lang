package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// closureArrUniformCases pin that every function-array literal reaching one
// destination holds env boxes: a plain lambda array, a capturing one, a
// generic-passthrough element like `id(<lambda>)`, and a `.with`/`.append`
// store into any of them.
//
// While an array of plain lambdas held bare `__lam_N` fn pointers, a function
// with two `return`s could hand back one of each representation, and the
// caller's one dispatch ABI crashed on whichever arm disagreed: compiled clean
// and SIGSEGV'd, invisible to the bail count and the strict-IR gate (#6555).
// Since #10076 every function array holds boxes whatever built it.
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
	// Both returns hold only no-capture lambdas.
	{"all-plain-returns", `function gen(c: boolean): ((i32) => i32)[] { return if (c) { [((x: i32) => x)] } else { [((y: i32) => (y + 1i32))] }; } function main(): i32 { var fs: ((i32) => i32)[] = gen(false); return fs[0i32](1i32) & 63i32; }`, 2},

	// `xs.with(i, v)` / `xs.append(v)`: the clone keeps the receiver's boxes and
	// the stored value is boxed to match, so the destination dispatches
	// env-first. Each case reads the WRITTEN element and an untouched one, so a
	// representation that agrees only at index 0 still fails.
	{"with-lambda-into-plain-fn-array", `function main(): i32 { var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; var w: ((i32) => i32)[] = s.with(0i32, ((b: i32) => (b + 1i32))); return ((w[0i32](5i32) + w[1i32](5i32) + s[0i32](5i32)) & 63i32); }`, 18},
	{"with-capturing-lambda-into-plain-fn-array", `function main(): i32 { var n: i32 = 3i32; var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; var w: ((i32) => i32)[] = s.with(0i32, ((b: i32) => (b + n))); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 15},
	// The value is a LOCAL holding the lambda, not the lambda itself. A
	// lambda-bound local is already an env box, so the receiver has to box even
	// though no lambda appears in the `.with` at all.
	{"with-boxed-local-into-plain-fn-array", `function main(): i32 { var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; var f = ((b: i32) => (b + 1i32)); var w: ((i32) => i32)[] = s.with(0i32, f); return ((w[0i32](5i32) + f(1i32)) & 63i32); }`, 8},
	// A bare module-fn NAME as the value gets the `$wrapN` trampoline box a
	// fn-name array ELEMENT gets, so the clone holds boxes throughout.
	{"with-fn-name-into-closure-array", `function bump(x: i32): i32 { return (x + 1i32); } function main(): i32 { var n: i32 = 2i32; var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + n))]; var w: ((i32) => i32)[] = s.with(0i32, bump); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 13},
	// No fn value in the `.with` at all: the value is an element read out of
	// the receiver.
	{"with-element-of-closure-array", `function main(): i32 { var n: i32 = 2i32; var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + n))]; var w: ((i32) => i32)[] = s.with(0i32, s[1i32]); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 14},
	{"append-capturing-lambda-to-plain-fn-array", `function main(): i32 { var n: i32 = 4i32; var s: ((i32) => i32)[] = [((a: i32) => a)]; var w: ((i32) => i32)[] = s.append(((b: i32) => (b + n))); return ((w[0i32](1i32) + w[1i32](5i32)) & 63i32); }`, 10},
	// A `.with` on a NON-fn array beside a function array.
	{"non-fn-with-beside-fn-array", `function main(): i32 { var xs: i32[] = [1i32, 2i32]; var ys: i32[] = xs.with(0i32, 7i32); var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; return ((s[0i32](ys[0i32]) + s[1i32](ys[1i32])) & 63i32); }`, 11},
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
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
