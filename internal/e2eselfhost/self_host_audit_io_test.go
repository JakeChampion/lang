package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditIOCases isolate stdout/stderr built-in functions and run them
// through the SELF-HOSTED compiler, checking the compiled program's
// stdout and exit code. Self-host arm of the §B audit
// (docs/FEATURE-AUDIT.md); the native arm is the `audit_io_builtins`
// fixture (all four native backends).
//
// `putchar` is fixed on the self-host IR path (#2839 — the x86-64 / arm64 /
// wasm IR backends emit the `__fern_putchar` runtime; guarded by
// self_host_putchar_{,arm64_,wasm_}ir_test.go). It stays held out HERE because
// this audit uses the legacy AST driver (asm_run / asm.fern), which still
// doesn't lower putchar — an AST-only gap that goal 1 leaves to the IR path.
var auditIOCases = []struct {
	name string
	src  string
	out  string // expected stdout
	exit int
}{
	{"print", `function main(): i32 { print("hello"); return 0; }`, "hello\n", 0},
	{"write-raw", `function main(): i32 { write("ab"); write("cd"); return 0; }`, "abcd", 0},
	{"eprint-not-stdout", `function main(): i32 { eprint("err"); print("out"); return 0; }`, "out\n", 0},
	{"len-free", `function main(): i32 { return len("hello") + len([1, 2, 3]); }`, "", 8},
	{"exit-code", `function main(): i32 { exit(42); return 0; }`, "", 42},
}

// TestSelfHostAuditIOX86_64 runs each I/O builtin through the self-hosted
// x86-64 driver, checking the compiled program's stdout + exit code.
func TestSelfHostAuditIOX86_64(t *testing.T) {
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

	for _, tc := range auditIOCases {
		t.Run(tc.name, func(t *testing.T) {
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
			out, _ := cmd.Output()
			if string(out) != tc.out {
				t.Errorf("%s stdout = %q, want %q", tc.name, string(out), tc.out)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
