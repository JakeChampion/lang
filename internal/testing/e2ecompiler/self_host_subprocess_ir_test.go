package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostSubprocessIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("subprocess IR test runs only natively (forks host binaries)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    let r = subprocess("/bin/echo", ["hello"], "");
    if (r.exit_code != 0) { return 1; }
    if (r.stdout != "hello\n") { return 2; }
    let c = subprocess("/bin/cat", [], "piped-input");
    if (c.exit_code != 0) { return 3; }
    if (c.stdout != "piped-input") { return 4; }
    let n = subprocess("/nonexistent_binary_xyz", [], "");
    if (n.exit_code != 127) { return 5; }
    if (n.stderr != "/nonexistent_binary_xyz: No such file or directory\n") { return 10; }
    let e = subprocess("sh", ["-c", "echo oops 1>&2"], "");
    if (e.exit_code != 0) { return 6; }
    if (e.stdout != "") { return 7; }
    if (e.stderr != "oops\n") { return 8; }
    let x = subprocess("sh", ["-c", "exit 3"], "");
    if (x.exit_code != 3) { return 9; }
    return 0;
}`

	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fn___fern_subprocess:") {
		t.Fatal("subprocess did not reach the Fern runtime helper (no __fn___fern_subprocess in asm)")
	}
	if strings.Contains(string(asm), ".Lsp_ccmd") {
		t.Error("the hand-written x86-64 __fern_subprocess body is still emitted (.Lsp_ccmd present)")
	}
	progBin := buildBin(t, gcc, dir, "subprocess_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("subprocess IR program exited %d, want 0 (echo stdout / cat stdin-pipe / missing=127 and its errno on stderr)", code)
	}
}

func TestSelfHostSubprocessIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    let r = subprocess("/bin/echo", ["hello"], "");
    if (r.exit_code != 0) { return 1; }
    if (r.stdout != "hello\n") { return 2; }
    let c = subprocess("/bin/cat", [], "piped-input");
    if (c.exit_code != 0) { return 3; }
    if (c.stdout != "piped-input") { return 4; }
    let n = subprocess("/nonexistent_binary_xyz", [], "");
    if (n.exit_code != 127) { return 5; }
    if (n.stderr != "/nonexistent_binary_xyz: No such file or directory\n") { return 10; }
    return 0;
}`

	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(src+"\n"), "-target", "arm64-linux"))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if !strings.Contains(asm, "__fn___fern_subprocess:") {
		t.Fatal("emitted arm64 asm has no Fern __fn___fern_subprocess helper")
	}
	if strings.Contains(asm, ".Lsp_ccmd") {
		t.Error("the hand-written arm64 __fn___fern_subprocess body is still emitted (.Lsp_ccmd present)")
	}
	for _, want := range []string{"59", "24", "221", "260", "220"} {
		if !arm64Imm(asm, want) {
			t.Errorf("emitted arm64 asm does not materialise the syscall number %s", want)
		}
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, arm64gcc, dir, "subprocess_prog", asm))
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 0 {
		t.Errorf("self-host arm64 binary exited %d, want 0 (echo stdout / cat stdin-pipe / missing=127 and its errno on stderr)", got)
	}
}

func TestSelfHostSubprocessNeedGatedArm64(t *testing.T) {
	arm64gcc, arm64runner := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const noSpawn = `function main(): i32 {
    let xs: string[] = ["a", "b"];
    return xs.len() - 2;
}`
	const dynamic = `function main(): i32 {
    let xs: string[] = [];
    for a in args() { xs = xs.append(a); }
    return xs.len() - args().len();
}`
	for _, tc := range []struct {
		name, src string
		heap      bool
	}{{"static", noSpawn, false}, {"dynamic", dynamic, true}} {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux"))
			if got := strings.Contains(asm, "__fern_alloc"); got != tc.heap {
				t.Fatalf("heap runtime present = %v, want %v\n%s", got, tc.heap, asm)
			}
			if strings.Contains(asm, "__fn___fern_subprocess") {
				t.Error("a program that never spawns still emits the subprocess helper")
			}
			cmd := runArm64Bin(arm64runner, buildBinArm64(t, arm64gcc, t.TempDir(), "no_spawn", asm))
			cmd.Args = append(cmd.Args, "a", "bb", "ccc")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("no-spawn program: %v\n%s", err, out)
			}
		})
	}
}
