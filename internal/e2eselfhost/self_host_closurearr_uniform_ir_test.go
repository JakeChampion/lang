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
// The rule is now per FUNCTION: if anything in it forces a box — a returned
// array literal, or a `.with`/`.append` storing a fn value into an array — every
// fn-valued array literal in that function boxes, and every fn value written
// into one is boxed to match. all-plain-returns-stay-bare and
// non-fn-with-leaves-fn-array-bare are the guards on the other side: a function
// with nothing to box keeps the bare fn-pointer representation, where env-first
// dispatch would be the same crash in the other direction.
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

	// `xs.with(i, v)` / `xs.append(v)` is the same disagreement one container
	// further out again: the clone they produce has the receiver's element ABI,
	// but the lift boxes a fn value at every method-argument position. So an
	// array of plain lambdas — bare `__lam_N` fn pointers — got a closure BOX
	// written into element i, and calling that element jumped to the box
	// address. The rule reaches the store now: a fn-value `.with`/`.append`
	// anywhere in the function puts its fn-value array literals on the env-box
	// ABI, the stored value is boxed to match, and the destination binding
	// inherits the receiver's closure-array mark so it dispatches env-first.
	// Each case reads the WRITTEN element and an untouched one, so a
	// representation that agrees only at index 0 still fails.
	{"with-lambda-into-plain-fn-array", `function main(): i32 { var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; var w: ((i32) => i32)[] = s.with(0i32, ((b: i32) => (b + 1i32))); return ((w[0i32](5i32) + w[1i32](5i32) + s[0i32](5i32)) & 63i32); }`, 18},
	{"with-capturing-lambda-into-plain-fn-array", `function main(): i32 { var n: i32 = 3i32; var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; var w: ((i32) => i32)[] = s.with(0i32, ((b: i32) => (b + n))); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 15},
	// The value is a LOCAL holding the lambda, not the lambda itself. A
	// lambda-bound local is already an env box, so the receiver has to box even
	// though no lambda appears in the `.with` at all.
	{"with-boxed-local-into-plain-fn-array", `function main(): i32 { var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; var f = ((b: i32) => (b + 1i32)); var w: ((i32) => i32)[] = s.with(0i32, f); return ((w[0i32](5i32) + f(1i32)) & 63i32); }`, 8},
	// The mismatch in the other direction: a boxed receiver, and a bare
	// module-fn NAME as the value. The name gets the `$wrapN` trampoline box a
	// fn-name array ELEMENT gets, so the clone holds boxes throughout.
	{"with-fn-name-into-closure-array", `function bump(x: i32): i32 { return (x + 1i32); } function main(): i32 { var n: i32 = 2i32; var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + n))]; var w: ((i32) => i32)[] = s.with(0i32, bump); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 13},
	// No fn value in the `.with` at all — the value is an element read out of
	// the receiver — so only the destination's inherited mark can be wrong.
	{"with-element-of-closure-array", `function main(): i32 { var n: i32 = 2i32; var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + n))]; var w: ((i32) => i32)[] = s.with(0i32, s[1i32]); return ((w[0i32](5i32) + w[1i32](5i32)) & 63i32); }`, 14},
	{"append-capturing-lambda-to-plain-fn-array", `function main(): i32 { var n: i32 = 4i32; var s: ((i32) => i32)[] = [((a: i32) => a)]; var w: ((i32) => i32)[] = s.append(((b: i32) => (b + n))); return ((w[0i32](1i32) + w[1i32](5i32)) & 63i32); }`, 10},
	// The guard on the store side: a `.with` on a NON-fn array stores no fn
	// value, so the fn-value literal beside it keeps bare fn pointers.
	{"non-fn-with-leaves-fn-array-bare", `function main(): i32 { var xs: i32[] = [1i32, 2i32]; var ys: i32[] = xs.with(0i32, 7i32); var s: ((i32) => i32)[] = [((a: i32) => a), ((c: i32) => (c + 2i32))]; return ((s[0i32](ys[0i32]) + s[1i32](ys[1i32])) & 63i32); }`, 11},
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
