package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// printIntIRCases exercise the `print_int(n)` builtin through the IR path on all
// three backends: it lowers to a `print_int` IR op — or `print_i64` when the
// argument is 64-bit — neither a call_direct, so both sidestep the call
// eligibility gate. The register backends route both ops to
// __fn___fern_print_int, the Fern-compiled i64 helper (asmcore.rt_src_print_int);
// wasm splits them across its WASI $__fern_print_int / $__fern_print_int64.
// stdout pins the exact decimal text (no newline), covering zero, negative,
// multiple writes, and the 64-bit range.
var printIntIRCases = []struct {
	name, src, want string
}{
	{"basic", `function main(): i32 { print_int(42); return 0; }`, "42"},
	{"zero", `function main(): i32 { print_int(0); return 0; }`, "0"},
	{"negative", `function main(): i32 { print_int(5 - 12); return 0; }`, "-7"},
	{"multi", `function main(): i32 { print_int(1); print_int(2); print_int(3); return 0; }`, "123"},
	{"computed", `function dbl(n: i32): i32 { return n * 2; } function main(): i32 { print_int(dbl(21)); return 0; }`, "42"},
	// Beyond 2^53, so it also pins that no float rounding creeps in. On the
	// register backends print_i64 had no handler and emitted
	// `# ir: unsupported bin print_i64`, printing nothing.
	{"i64", `function main(): i32 { var n: i64 = 9007199254740993 as i64; print_int(n); return 0; }`, "9007199254740993"},
	// INT64_MIN has no positive magnitude in i64, so the helper takes it in
	// u64. Built by subtraction because the literal itself would overflow.
	{"i64-min", `function main(): i32 { var lo: i64 = (0 as i64) - 9223372036854775807 as i64; lo = lo - (1 as i64); print_int(lo); return 0; }`, "-9223372036854775808"},
}

// TestSelfHostPrintIntIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on) and asserts the program's stdout, proving the
// IR path lowered print_int and linked the helper.
func TestSelfHostPrintIntIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range printIntIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fn___fern_print_int")) {
				t.Fatalf("%s: no call to __fn___fern_print_int — print_int did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, _ := cmd.Output()
			if string(out) != tc.want {
				t.Errorf("%s: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// TestSelfHostPrintIntIRArm64 runs the same cases through the arm64 IR backend
// under qemu (CI-gated). The arm64 op handler calls __fn___fern_print_int, which
// asm_arm64_ir emits from asmcore.rt_src_print_int via the print_int need.
func TestSelfHostPrintIntIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range printIntIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fn___fern_print_int")) {
				t.Fatalf("%s: no bl __fn___fern_print_int — print_int did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "pi_"+tc.name, string(asm))
			out, _ := runArm64Bin(qemu, bin).Output()
			if string(out) != tc.want {
				t.Errorf("print_int arm64 IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// TestSelfHostPrintIntIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostPrintIntIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host print_int wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range printIntIRCases {
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
			if !bytes.Contains(wat, []byte("$__fern_print_int")) {
				t.Fatalf("%s: no $__fern_print_int in wat — print_int did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "pi_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, _ := run.Output()
			if string(out) != tc.want {
				t.Errorf("print_int wasm IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}
