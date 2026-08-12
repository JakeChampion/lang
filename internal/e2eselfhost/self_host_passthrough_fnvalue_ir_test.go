package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// passthroughFnValueCases exercise a fn value handed to a generic PASSTHROUGH
// — a function that declares a parameter with its own return type, so it gives
// the value straight back (`id[T](x: T): T`, `pick[T](c: boolean, a: T, b: T): T`).
//
// The lift boxes a fn value at such a parameter unconditionally: the callee's
// type is erased, so it cannot call the value, and boxing is what lets the
// result be dispatched env-first. What was missing is that nothing on the read
// side could tell that the CALL is therefore a box too. So an array holding
// `id(<lambda>)` beside a plain lambda element got two representations under one
// binding — a bare fn pointer and an env box — and `xs[i](…)` dispatched both
// the same way. Every array case below compiled clean and SIGSEGV'd; the
// struct-field case bailed instead, because that position calls
// try_fn_field_value directly and the walk never reached the call's arguments.
//
// A `PASSTHRU:<fn>:<argidx>` marker rides the closure_fns list (the same
// convention as `FNPTR:` / `RETCLO2:` / `CLOARR:`) so the read side can answer
// "is this call a box" with "is its passthrough argument one".
//
// nocapture-array is the case that shows capture is not the discriminator: no
// lambda in it captures anything, and it segfaulted just the same, because the
// boxing at the passthrough parameter does not depend on captures either.
//
// tuple-elem-passthrough already worked and is a guard: the tuple path boxes
// EVERY fn-valued element, so both representations agreed there before.
var passthroughFnValueCases = []struct {
	name string
	src  string
	exit int
}{
	{"array-elem-pick-capturing", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; } function main(): i32 { var p: i32 = 5i32; var xs: ((i32) => i32)[] = [((z: i32) => z), pick(true, ((x: i32) => (x + p)), ((y: i32) => y))]; return xs[1i32](1i32) & 63i32; }`, 6},
	{"array-elem-id-capturing", `function id[T](x: T): T { return x; } function main(): i32 { var p: i32 = 5i32; var xs: ((i32) => i32)[] = [((z: i32) => z), id(((x: i32) => (x + p)))]; return xs[1i32](1i32) & 63i32; }`, 6},
	// The passthrough element FIRST: the array's classification reads element 0,
	// so this is the path through the new marker rather than through a boxed
	// sibling.
	{"array-elem-pick-first", `function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; } function main(): i32 { var p: i32 = 5i32; var xs: ((i32) => i32)[] = [pick(true, ((x: i32) => (x + p)), ((y: i32) => y)), ((z: i32) => z)]; return (xs[0i32](1i32) + xs[1i32](2i32)) & 63i32; }`, 8},
	{"array-elem-id-nocapture", `function id[T](x: T): T { return x; } function main(): i32 { var xs: ((i32) => i32)[] = [((z: i32) => (z + 1i32)), id(((x: i32) => (x + 2i32)))]; return (xs[0i32](1i32) + xs[1i32](1i32)) & 63i32; }`, 5},
	{"struct-field-id-capturing", `function id[T](x: T): T { return x; } struct H { f: (i32) => i32 } function main(): i32 { var p: i32 = 5i32; var h: H = H { f: id(((x: i32) => (x + p))) }; return h.f(1i32) & 63i32; }`, 6},
	{"tuple-elem-passthrough", `function id[T](x: T): T { return x; } function main(): i32 { var p: i32 = 5i32; var t: ((i32) => i32, i32) = (id(((x: i32) => (x + p))), 2i32); return t.0(1i32) & 63i32; }`, 6},
}

// TestSelfHostPassthroughFnValueIRX86_64 — the x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostPassthroughFnValueIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range passthroughFnValueCases {
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

// TestSelfHostPassthroughFnValueIRArm64 — the arm64 IR path, the one where the
// self-host compiler produces the finished binary itself.
func TestSelfHostPassthroughFnValueIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range passthroughFnValueCases {
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

// TestSelfHostPassthroughFnValueWasmIR — the wasm leg. The lift and the marker
// live in irlower.fern, which every backend shares.
func TestSelfHostPassthroughFnValueWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host passthrough fn-value wasm IR e2e")
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

	for _, tc := range passthroughFnValueCases {
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
