package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// procExecAsPrograms pin `proc_exec_as(path, argv, envp)` on the self-host IR
// path (#9090). Differential against the NATIVE backend rather than the interp
// oracle: the interpreter's exec answers -ENOSYS, so it cannot judge a real
// execve.
//
//   - argv0-and-envp: `sh -c SCRIPT` with no command_name operand takes $0 from
//     the shell's own argv[0], so the script reads back slot 0 exactly as the
//     caller wrote it. FERNX is in no parent environment and HOME is in every
//     one, so the pair proves the caller's envp REPLACED the process's rather
//     than adding to it. (PATH is not asserted on: `sh` invents a default for
//     that one when handed an environment without it.)
//   - failure-and-empty-envp: a failing execve RETURNS, as -errno, and an empty
//     envp is a well-formed vector — the NULL terminator alone — not a missing
//     one.
var procExecAsPrograms = []struct {
	name string
	src  string
	want int
}{
	{
		"argv0-and-envp",
		`function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 70; }
    if (pid == 0) {
        var script: string = "test \"$0\" = myname || exit 61; test \"$FERNX\" = 42 || exit 62; test -z \"${HOME+set}\" || exit 63; exit 29";
        var rc: i32 = proc_exec_as("/bin/sh", ["myname", "-c", script], ["FERNX=42"]);
        return 71;
    }
    return proc_waitpid(pid);
}`,
		29,
	},
	{
		"failure-and-empty-envp",
		`function main(): i32 {
    var rc: i32 = proc_exec_as("/nonexistent/binary", ["x"], ["A=1"]);
    if (rc >= 0) { return 70; }
    var pid: i32 = proc_fork();
    if (pid < 0) { return 71; }
    if (pid == 0) {
        var r2: i32 = proc_exec_as("/bin/sh", ["sh", "-c", "test -z \"${HOME+set}\" && exit 31"], []);
        return 72;
    }
    return proc_waitpid(pid);
}`,
		31,
	},
}

// The x86-64 leg. The program has to route through the IR path rather than
// bail, and the emitted asm has to carry the Fern runtime helper — a call to a
// symbol nothing defines assembles and links against nothing, which is what a
// missing `need` looks like.
func TestSelfHostProcExecAsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range procExecAsPrograms {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
				t.Fatalf("routed through %q path, want \"ir\"", path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if !strings.Contains(string(asm), "__fn___fern_proc_exec_as:") {
				t.Fatal("emitted asm has no Fern __fn___fern_proc_exec_as helper")
			}
			progBin := buildBin(t, gcc, dir, "procexecas_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
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

// The arm64 half. The helper is Fern (#2649), so the stack-ABI prefix is what
// distinguishes it: the syscall number arrives as a pushed operand popped into
// x8, hence `mov x0, #221` rather than `mov x8, #221`.
func TestSelfHostProcExecAsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range procExecAsPrograms {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			for _, want := range []string{"bl __fn___fern_proc_exec_as", "mov x0, #221"} {
				if !strings.Contains(asm, want) {
					t.Errorf("emitted arm64 asm missing %q", want)
				}
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, arm64gcc, dir, "procexecas_"+tc.name, asm))
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("self-host arm64 binary exited %d, want %d", got, tc.want)
			}
		})
	}
}

// The wasm half is a refusal. A component is one instance with no process image
// to replace, so this is an error ENDPOINT like proc_exec's rather than a
// deferral — deferring it emits a call against nothing and surfaces as an
// opaque `unknown func` from the loader instead of a diagnostic naming the
// feature.
func TestSelfHostProcExecAsWasmRejected(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern", "wasm_ir_run.fern")
	drivers := []struct {
		name string
		bin  string
		args []string
	}{
		{"wasm_run", buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run"), nil},
		{"wasm_ir_run", buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run"), []string{"-ir"}},
	}
	const src = `function main(): i32 { return proc_exec_as("/bin/true", ["true"], []); }`
	for _, d := range drivers {
		t.Run(d.name, func(t *testing.T) {
			out, errOut, code := runDriverAllowFail(t, runner, d.bin, src+"\n", d.args...)
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			if !strings.Contains(string(errOut), "proc_exec_as is not supported on the wasm target") {
				t.Errorf("stderr = %q, want the unsupported-builtin diagnostic naming proc_exec_as", errOut)
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for an unsupported builtin, want 0", len(out))
			}
		})
	}
}
