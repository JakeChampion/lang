package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

var procForkPrograms = []struct {
	name string
	src  string
	want int
}{
	{
		"normal-exit",
		`function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid < 0) { return 91; }
    if (pid == 0) { exit(17); }
    let code: i32 = proc_waitpid(pid);
    if (code != 17) { return 92; }
    return 42;
}`,
		42,
	},
	{
		"signal-death",
		`function worker(): i32 {
    let xs: i32[] = [1, 2, 3];
    let i: i32 = 9;
    return xs[i];
}
function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid < 0) { return 91; }
    if (pid == 0) { return worker(); }
    return proc_waitpid(pid);
}`,
		134,
	},
}

func TestSelfHostProcForkIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

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
		})
	}
}

func TestSelfHostProcForkIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range procForkPrograms {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			for _, want := range []string{"bl __fn___fern_proc_fork", "bl __fn___fern_proc_waitpid"} {
				if !strings.Contains(asm, want) {
					t.Errorf("emitted arm64 asm missing %q", want)
				}
			}
			for _, want := range []string{"220", "260"} {
				if !arm64Imm(asm, want) {
					t.Errorf("emitted arm64 asm does not materialise the syscall number %s", want)
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

func TestSelfHostProcForkWasmRejected(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_run.fern", "drivers/wasm_ir_run.fern")
	drivers := []struct {
		name string
		bin  string
	}{
		{"wasm_run", buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")},
		{"wasm_ir_run", buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")},
	}
	const src = "function main(): i32 { let pid: i32 = proc_fork(); if (pid == 0) { exit(3); } return proc_waitpid(pid); }"
	for _, d := range drivers {
		t.Run(d.name, func(t *testing.T) {
			out, errOut, code := runDriverAllowFail(t, runner, d.bin, src+"\n")
			if code != 1 {
				t.Errorf("driver exited %d, want 1 (reject)", code)
			}
			if !strings.Contains(string(errOut), "required by `proc_fork`; targets providing it") {
				t.Errorf("stderr = %q, want E066 naming proc_fork", errOut)
			}
			if len(out) != 0 {
				t.Errorf("driver emitted %d bytes for an unsupported builtin, want 0", len(out))
			}
		})
	}
}
