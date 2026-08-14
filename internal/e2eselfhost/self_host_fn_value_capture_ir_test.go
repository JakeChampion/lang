package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fnValueCaptureIRCases pin a closure that CAPTURES a fn-valued local — the
// remaining half of #6256's `<fn>$clo not defined` cluster.
//
// A fn-valued `var` is env-boxed at its binding only when the enclosing body
// uses the name as a VALUE; a name it merely calls is left to the cheaper
// direct-call lift. Inside a nested lambda that distinction does not hold —
// every free var there is copied into the closure's env box (make_clo_func) or
// passed as the lifted call's extra argument (closure_lift_one), and both read
// a fn value with the uniform env-box ABI — so a call-only use one lambda in
// left the fn value unboxed and the escaping lambda reached lower_expr raw,
// asking for a `<fn>$clo` nothing had built. The capture also has to TYPE
// before the box can be built, and cap_type_expr had no arm for a lambda init
// nor for the `__mkclo$` marker the lift leaves in its place.
//
// A nested `function` declaration desugars to `var f = <lambda>`, which is why
// the shape reaches this through ordinary-looking source.
//
// Every case asserts the ANSWER through the value: a reduced program that only
// BUILDS the closure cannot show the crash that invoking it produces.
var fnValueCaptureIRCases = []struct {
	name string
	src  string
	exit int
}{
	// A nested `function` captured by the escaping lambda.
	{"nested-local-fn-captured", `function mk(): (i32) => i32 {
    function lf(x: i32): i32 { return x + 1i32; }
    return ((x0: i32) => (lf(x0) * 2i32));
}
function main(): i32 { return mk()(3i32) & 63i32; }`, 8},
	// The same with the fn-typed local written out, so the capture types from
	// the annotation rather than from the lambda.
	{"annotated-fn-local-captured", `function mk(): (i32) => i32 {
    var lf: (i32) => i32 = ((x: i32) => (x + 1i32));
    return ((x0: i32) => (lf(x0) * 2i32));
}
function main(): i32 { return mk()(3i32) & 63i32; }`, 8},
	// A fn value and a scalar captured by the same closure: the env box carries
	// both, so the box slot order has to hold across the two kinds.
	{"fn-and-scalar-captured", `function mk(n: i32): (i32) => i32 {
    var lf: (i32) => i32 = ((x: i32) => (x * 2i32));
    return ((x0: i32) => (lf(x0) + n));
}
function main(): i32 { return mk(5i32)(4i32) & 63i32; }`, 13},
	// The captured fn value is itself a CAPTURING closure, so the outer box
	// holds a box.
	{"capturing-closure-captured", `function mk(n: i32): (i32) => i32 {
    var inner: (i32) => i32 = ((y: i32) => (y + n));
    return ((x0: i32) => (inner(x0) * 2i32));
}
function main(): i32 { return mk(3i32)(4i32) & 63i32; }`, 14},
	// The capturing lambda sits in an ARRAY of fn values beside a plain one:
	// one container, one dispatch ABI (#5071), so both elements must be boxes.
	{"fn-capture-in-closure-array", `function main(): i32 {
    function lf(x: i32): i32 { return x + 1i32; }
    var fs: ((i32) => i32)[] = [((x0: i32) => (lf(x0) * 2i32)), ((x1: i32) => x1)];
    return fs[0i32](3i32) & 63i32;
}`, 8},
	// The seed shape (fernsmith s0172): the capturing lambda reaches its
	// destination through a generic passthrough, and the captured fn value is a
	// nested `function` declaration.
	{"passthrough-arg-captures-fn-local", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
function main(): i32 {
    function lf(x: i32): boolean { return false; }
    var v1: (i32) => i32 = pick(true, ((x0: i32) => (if (lf(4i32)) { x0 } else { 34i32 })), ((x1: i32) => 5i32));
    return v1(3i32) & 255i32;
}`, 34},
	// Regression guard for the representation this must not move: a fn-valued
	// local that is only ever CALLED, never captured, stays on the direct-call
	// lift rather than being boxed.
	{"call-only-fn-local-unchanged", `function main(): i32 {
    var f: (i32) => i32 = ((x: i32) => (x + 1i32));
    return f(3i32) & 63i32;
}`, 4},
	// The other half of that guard: a call-only local whose lambda CAPTURES is
	// what closure_lift_one param-lifts, and boxing it instead would pre-empt
	// the closure-calls-closure path.
	{"call-only-capturing-local-unchanged", `function main(): i32 {
    var n: i32 = 3i32;
    var f: (i32) => i32 = ((x: i32) => (x + n));
    return f(4i32) & 63i32;
}`, 7},
}

// TestSelfHostFnValueCaptureIRX86_64 drives the production x86-64 IR path.
//
// runCaptureStrictIR rather than runCapture: these shapes reach the right
// answer through the per-function bail too, so an exit code alone cannot tell
// the two routes apart (#6602).
func TestSelfHostFnValueCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnValueCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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

// TestSelfHostFnValueCaptureWasmIR — the wasm leg, which is the one that
// decides the admission. A wasm funcref type is STRUCTURAL, so a captured fn
// value whose signature the flat "fn" tag cannot carry is an `indirect call
// type mismatch` rather than a slow path; cap_fn_sig_wide keeps those on their
// bail, and every case here is inside what the arity-keyed type describes.
func TestSelfHostFnValueCaptureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-value-capture wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fnValueCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

// TestSelfHostFnValueCaptureIRArm64 — the arm64 counterpart. The lift is
// shared, so arm64 picks it up unchanged; running it is what proves that.
func TestSelfHostFnValueCaptureIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 fn-value-capture gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnValueCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
