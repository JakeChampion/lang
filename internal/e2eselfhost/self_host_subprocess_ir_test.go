package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostSubprocessIR pins `subprocess(cmd, args, stdin)` lowering on the
// self-host x86-64 IR path. subprocess spawns a child, pipes its streams, and
// returns a BARE ProcessResult struct {stdout, stderr, exit_code} — unwrapped by
// a Result, unlike stat.
//
// The helper is Fern now (#2649, asmcore.rt_src_subprocess): three pipes, a
// fork, dup3 + execve with the /bin and /usr/bin fallbacks in the child, and
// stdin feed / stream drain / wait4 in the parent, all over the existing
// __syscall3 / __syscall4 / __raw_* floor. The symbol check is pinned to the
// full mangled name: `__fern_subprocess` is a PREFIX of `__fn___fern_subprocess`,
// so the old spelling passed on both sides of the migration and proved nothing.
//
// This exercises the bare-struct-RESULT typing: expr_struct_type types
// `subprocess(..)` as ProcessResult (no match needed), so `var r = subprocess(..)`
// binds r and r.stdout / r.exit_code resolve against the injected struct. The
// program runs /bin/echo (stdout capture), /bin/cat (stdin piping), and a
// nonexistent binary (spawn failure -> exit_code 127), exiting 0 only if all
// three resolve correctly. Two more cover what the original left untested and
// this migration rewrote: stderr capture (kept separate from stdout) and a
// non-zero exit status, both through a BARE `sh` so the /bin PATH fallback is
// exercised rather than an absolute path.
func TestSelfHostSubprocessIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("subprocess IR test runs only natively (forks host binaries)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    var r = subprocess("/bin/echo", ["hello"], "");
    if (r.exit_code != 0) { return 1; }
    if (r.stdout != "hello\n") { return 2; }
    var c = subprocess("/bin/cat", [], "piped-input");
    if (c.exit_code != 0) { return 3; }
    if (c.stdout != "piped-input") { return 4; }
    var n = subprocess("/nonexistent_binary_xyz", [], "");
    if (n.exit_code != 127) { return 5; }
    var e = subprocess("sh", ["-c", "echo oops 1>&2"], "");
    if (e.exit_code != 0) { return 6; }
    if (e.stdout != "") { return 7; }
    if (e.stderr != "oops\n") { return 8; }
    var x = subprocess("sh", ["-c", "exit 3"], "");
    if (x.exit_code != 3) { return 9; }
    return 0;
}`

	cmd := exec.Command(driverBin, "-ir")
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
		t.Errorf("subprocess IR program exited %d, want 0 (echo stdout / cat stdin-pipe / missing=127)", code)
	}
}

// TestSelfHostSubprocessIRArm64 is the arm64-Linux half of the migration. The
// helper is the SAME asmcore.rt_src_subprocess source as x86-64, with only the
// syscall numbers substituted — pipe2 59, dup3 24, execve 221, wait4 260 and
// the clone(SIGCHLD, …) spelling of fork, all of which the asm-content checks
// pin so a mis-numbered syscall fails here rather than hanging under qemu.
//
// Darwin does NOT share this body: XNU has no pipe2 and its pipe() reports the
// second fd in x1, which no __syscall* intrinsic can see. That target keeps the
// hand-written __fn___fern_subprocess (self_host_macho_test.go covers it).
func TestSelfHostSubprocessIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    var r = subprocess("/bin/echo", ["hello"], "");
    if (r.exit_code != 0) { return 1; }
    if (r.stdout != "hello\n") { return 2; }
    var c = subprocess("/bin/cat", [], "piped-input");
    if (c.exit_code != 0) { return 3; }
    if (c.stdout != "piped-input") { return 4; }
    var n = subprocess("/nonexistent_binary_xyz", [], "");
    if (n.exit_code != 127) { return 5; }
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
	// The syscall numbers arrive as ordinary pushed operands popped into x8, so
	// each shows as `mov x0, #N` at the call site rather than `mov x8, #N`.
	for _, want := range []string{"mov x0, #59", "mov x0, #24", "mov x0, #221", "mov x0, #260", "mov x0, #220"} {
		if !strings.Contains(asm, want) {
			t.Errorf("emitted arm64 asm missing %q", want)
		}
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, arm64gcc, dir, "subprocess_prog", asm))
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != 0 {
		t.Errorf("self-host arm64 binary exited %d, want 0 (echo stdout / cat stdin-pipe / missing=127)", got)
	}
}

// TestSelfHostSubprocessNeedGatedArm64 pins that the subprocess helper is
// need-gated on arm64. It used to sit inside the bare `heap` gate, so every
// allocating program carried ~400 instructions nothing branched to — the same
// unconditional emission the Reader bodies had (#6921).
func TestSelfHostSubprocessNeedGatedArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	// Allocates (a heap string array) but never spawns.
	const noSpawn = `function main(): i32 {
    var xs: string[] = ["a", "b"];
    return xs.len() - 2;
}`
	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(noSpawn+"\n"), "-target", "arm64-linux"))
	if !strings.Contains(asm, "__fern_alloc") {
		t.Fatal("the no-spawn program did not emit the heap runtime — the gate under test is vacuous")
	}
	if strings.Contains(asm, "__fn___fern_subprocess") {
		t.Error("a heap-using program that never spawns still emits the subprocess helper")
	}
}
