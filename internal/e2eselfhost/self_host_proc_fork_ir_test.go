package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// procForkPrograms are the two shapes that pin `proc_fork()` / `proc_waitpid(pid)`
// on the self-host IR path (#5686). Both are differential against the NATIVE
// backend rather than the interp oracle: the interpreter's `proc_fork` never
// forks (it answers -ENOSYS so `tcp_serve_supervised` degrades to single-process
// serving), so it cannot judge a real fork.
//
//   - normal-exit: the child exits 17, the parent reaps it and checks the
//     decoded status — the `(status >> 8) & 0xff` arm of the wait4 decode.
//   - signal-death: the child trips the array-bounds abort, so the parent must
//     see 128 + SIGABRT = 134 — the other arm, and the reason the decode exists
//     (a crashing worker has to surface as its shell-convention code).
var procForkPrograms = []struct {
	name string
	src  string
	want int
}{
	{
		"normal-exit",
		`function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 91; }
    if (pid == 0) { exit(17); }
    var code: i32 = proc_waitpid(pid);
    if (code != 17) { return 92; }
    return 42;
}`,
		42,
	},
	{
		"signal-death",
		`function worker(): i32 {
    var xs: i32[] = [1, 2, 3];
    var i: i32 = 9;
    return xs[i];
}
function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 91; }
    if (pid == 0) { return worker(); }
    return proc_waitpid(pid);
}`,
		134,
	},
}

// TestSelfHostProcForkIRX86_64 pins #5686: the crash-only supervision pair
// lowers on the self-host x86-64 IR path. Before this they had no IR op and no
// emitter interception at all, so a call fell through to the generic user-call
// path and emitted `call __fn_proc_fork` against a symbol nothing defines —
// which is why `std/tcp` (whose `tcp_serve_supervised` calls both) could not be
// self-host compiled. Because they are real ops now, a fork-using module is
// IR-ELIGIBLE rather than routing to the AST emitter.
func TestSelfHostProcForkIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range procForkPrograms {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
				t.Fatalf("routed through %q path, want \"ir\"", path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if !strings.Contains(string(asm), "__fn___fern_proc_fork:") {
				t.Fatal("emitted asm has no Fern __fn___fern_proc_fork helper")
			}
			progBin := buildBin(t, gcc, dir, "procfork_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			got := cmd.ProcessState.ExitCode()
			if got != tc.want {
				t.Errorf("self-host binary exited %d, want %d", got, tc.want)
			}
			// Differential against the native backend — the oracle for a
			// builtin the interpreter deliberately cannot execute.
			if _, native := compileAndRunX86_64(t, tc.src); native != tc.want {
				t.Errorf("native backend exited %d, want %d (oracle disagrees — fix the test, not the backend)", native, tc.want)
			}
		})
	}
}

// TestSelfHostProcForkIRArm64 is the arm64 half. arm64 Linux has no bare
// fork(2): proc_fork is `clone(SIGCHLD, 0, 0, 0, 0)` (asm-generic 220), whose
// return shape is already the builtin's contract. The asm-content checks pin
// that dispatch and that real body, so a regression to a stubbed or
// mis-numbered syscall fails here rather than hanging under qemu.
func TestSelfHostProcForkIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range procForkPrograms {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			// Both are Fern since #2649, so both carry the stack-ABI prefix and
			// both syscall numbers arrive as ordinary pushed operands popped into
			// x8 — hence `mov x0, #N` rather than `mov x8, #N`. clone is the
			// five-argument __syscall5 form; wait4 is __syscall4.
			for _, want := range []string{"bl __fn___fern_proc_fork", "bl __fn___fern_proc_waitpid", "mov x0, #220", "mov x0, #260"} {
				if !strings.Contains(asm, want) {
					t.Errorf("emitted arm64 asm missing %q", want)
				}
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, arm64gcc, dir, "procfork_"+tc.name, asm))
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("self-host arm64 binary exited %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSelfHostProcForkWasmRejected pins the wasm half: a component has no
// process model, so fork/reap are error ENDPOINTS (like subprocess / timer_fd),
// not deferrals to the AST emitter — which would emit a call against nothing and
// surface as an opaque `unknown func` from the loader instead of a diagnostic
// naming the feature.
func TestSelfHostProcForkWasmRejected(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern",
		"wasm_run.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	drivers := []struct {
		name string
		bin  string
		args []string
	}{
		{"wasm_run", buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run"), nil},
		{"wasm_ir_run", buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run"), []string{"-ir"}},
	}
	const src = "function main(): i32 { var pid: i32 = proc_fork(); if (pid == 0) { exit(3); } return proc_waitpid(pid); }"
	for _, d := range drivers {
		t.Run(d.name, func(t *testing.T) {
			out, errOut, code := runDriverAllowFail(t, runner, d.bin, src+"\n", d.args...)
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			if !strings.Contains(string(errOut), "proc_fork is not supported on the wasm target") {
				t.Errorf("stderr = %q, want the unsupported-builtin diagnostic naming proc_fork", errOut)
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for an unsupported builtin, want 0", len(out))
			}
		})
	}
}
