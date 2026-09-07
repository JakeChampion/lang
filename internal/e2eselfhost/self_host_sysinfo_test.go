package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// The self-host legs of the three machine-facing primitives group C's
// system-information utilities are built on: `uname_field(i)`,
// `getcwd()` and `cpu_count()`. Each asserts the VALUE, against the same
// kernel fact read a second way — sysname is "Linux" on every box here,
// so a body that read the neighbouring utsname field would pass a
// non-empty check. The utsname probe reads uname(2) and is linux-only
// (syscall.Utsname does not exist on darwin), so it lives in
// self_host_sysinfo_linux_test.go and the other OSes skip in
// self_host_sysinfo_other_test.go.

// selfHostSysinfoSource exits 0 only when every field matches; each
// other exit code names the one that did not. `machine` is passed in
// because the arm64 leg runs under qemu, which answers "aarch64" for
// exactly the field a wrong offset is most likely to land on.
func selfHostSysinfoSource(t *testing.T, machine string) string {
	t.Helper()
	u := selfHostUtsname(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return fmt.Sprintf(`function main(): i32 {
    if (uname_field(0) != %q) { return 1; }
    if (uname_field(1) != %q) { return 2; }
    if (uname_field(2) != %q) { return 3; }
    if (uname_field(3) != %q) { return 4; }
    if (uname_field(4) != %q) { return 5; }
    if (uname_field(5) != "") { return 6; }
    if (uname_field(0 - 1) != "") { return 7; }
    if (getcwd() != %q) { return 8; }
    if (cpu_count() != %d) { return 9; }
    return 0;
}
`, u[0], u[1], u[2], u[3], machine, wd, runtime.NumCPU())
}

// TestSelfHostSysinfoIR pins the x86-64 lowering: the three Fern-source
// runtime helpers (asmcore.rt_src_uname_field / _getcwd / _cpu_count)
// compiled through the self-host's own IR path.
func TestSelfHostSysinfoIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("sysinfo test runs only natively (compares the host's own machine facts)")
	}
	src := selfHostSysinfoSource(t, selfHostUtsname(t)[4])
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	for _, want := range []string{"__fn___fern_uname_field", "__fn___fern_getcwd", "__fn___fern_cpu_count"} {
		if !bytes.Contains(asm, []byte(want)) {
			t.Fatalf("%s did not reach the IR runtime path (no %s in asm)", want, want)
		}
	}
	progBin := buildBin(t, gcc, dir, "sysinfo_prog", string(asm))
	run := exec.Command(progBin)
	run.Dir = mustGetwd(t)
	out, _ := run.Output()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("sysinfo probe exited %d (the code names the field); stdout %q", code, strings.TrimSpace(string(out)))
	}
}

// The arm64-linux leg: the same Fern bodies with the asm-generic
// syscall numbers, run under qemu.
func TestSelfHostSysinfoIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("sysinfo test runs only natively (compares the host's own machine facts)")
	}
	src := selfHostSysinfoSource(t, "aarch64")
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_uname_field")) {
		t.Fatal("uname_field did not reach the arm64 IR runtime path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "sysinfo_arm64", string(asm))
	run := runArm64Bin(qemu, bin)
	run.Dir = mustGetwd(t)
	out, _ := run.Output()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("arm64 sysinfo program did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 sysinfo probe exited %d (the code names the field); stdout %q", code, strings.TrimSpace(string(out)))
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return wd
}

// The arm64-darwin leg is an EMIT check, not a run: XNU has no uname
// syscall and no affinity mask, so `uname_field` and `cpu_count` are
// hand-written sysctl bodies there rather than the Fern sources the
// Linux targets compile, and nothing on this host can execute a Mach-O.
// What it pins is that the darwin fork is taken at all — the failure it
// exists to catch is a helper added on the Linux side only, which emits
// a `__syscall3` of the -1 `sysno` returns for a call XNU does not have.
// `getcwd` is deliberately NOT in the darwin list: XNU's `__getcwd` has
// the same shape as Linux's, so one Fern body serves both.
func TestSelfHostSysinfoIRArm64Darwin(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    print(uname_field(4));
    print(getcwd());
    return cpu_count();
}
`
	cmd := exec.Command(driverBin, "-target", "arm64-darwin", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	text := string(asm)
	for _, want := range []string{
		"__fn___fern_uname_field:",
		"__fn___fern_getcwd:",
		"__fn___fern_cpu_count:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("arm64-darwin emit is missing %s", want)
		}
	}
	// BSD sysctl is 202, and both hand-written bodies issue it. A Fern
	// body compiled for darwin instead would carry neither.
	if n := strings.Count(text, "mov x16, #202"); n != 2 {
		t.Errorf("arm64-darwin emit issues sysctl %d times, want 2 (uname_field and cpu_count)", n)
	}
	// The one syscall number XNU does not have. `sysno` answers -1 for
	// it, so its appearance is a Linux body reaching a darwin build.
	if strings.Contains(text, "__syscall3(-1") || strings.Contains(text, "mov x8, #-1") {
		t.Error("arm64-darwin emit issues syscall -1, so a Linux-only body reached this target")
	}
}
