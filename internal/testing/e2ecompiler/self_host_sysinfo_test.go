package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

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
    match (getcwd()) {
        Ok(wd) => { if (wd != %q) { return 8; } },
        Err(e) => { return 10; }
    }
    if (cpu_count() != %d) { return 9; }
    return 0;
}
`, u[0], u[1], u[2], u[3], machine, wd, runtime.NumCPU())
}

func TestSelfHostSysinfoIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("sysinfo test runs only natively (compares the host's own machine facts)")
	}
	src := selfHostSysinfoSource(t, selfHostUtsname(t)[4])
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin)
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

func TestSelfHostSysinfoIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("sysinfo test runs only natively (compares the host's own machine facts)")
	}
	src := selfHostSysinfoSource(t, "aarch64")
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-target", "arm64-linux")
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

func TestSelfHostSysinfoIRArm64Darwin(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    print(hostname());
    print(uname_field(4));
    match (getcwd()) {
        Ok(wd) => { print(wd); },
        Err(e) => { return 1; }
    }
    return cpu_count();
}
`
	cmd := exec.Command(driverBin, "-target", "arm64-darwin")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	text := string(asm)
	for _, want := range []string{
		"__fn___fern_hostname:",
		"__fn___fern_uname_field:",
		"__fn___fern_getcwd:",
		"__fn___fern_cpu_count:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("arm64-darwin emit is missing %s", want)
		}
	}
	if !strings.Contains(text, "__fern_scratch: .skip 400") {
		t.Error("Darwin sysinfo needs space for its MIB, length and 384-byte value")
	}
	if n := strings.Count(text, "#202"); n != 3 {
		t.Errorf("arm64-darwin emit issues sysctl %d times, want 3 (hostname, uname_field and cpu_count)", n)
	}
	if strings.Contains(text, "__syscall3(-1") || strings.Contains(text, "mov x8, #-1") {
		t.Error("arm64-darwin emit issues syscall -1, so a Linux-only body reached this target")
	}
	for _, want := range []string{"463", "92", "50"} {
		if !arm64Imm(text, want) {
			t.Errorf("arm64-darwin emit does not materialise %s — getcwd's darwin body did not reach it", want)
		}
	}
	if arm64Imm(text, "296") {
		t.Error("arm64-darwin emit issues BSD 296 (vm_pressure_monitor), which is not getcwd")
	}
}
