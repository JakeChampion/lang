package e2ecompiler

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func rlimitSelfHostSource(want int64) string {
	return fmt.Sprintf(`function main(): i32 {
    let n: i64 = rlimit_nofile();
    // All-ones RLIM_INFINITY reaching a caller unclamped reads as -1.
    if (n < (0 as i64)) { return 1; }
    // Every kernel enforces a ceiling above the three standard descriptors.
    if (n < (4 as i64)) { return 2; }
    if (n != (%d as i64)) { return 3; }
    return 0;
}`, want)
}

func nofileProbeLimit(t *testing.T) int64 {
	t.Helper()
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	for _, want := range []int64{4321, 987, 321, 64} {
		if uint64(want) < lim.Max && uint64(want) != lim.Cur {
			return want
		}
	}
	t.Fatalf("no soft RLIMIT_NOFILE below the hard limit %d and away from the current %d", lim.Max, lim.Cur)
	return 0
}

func runUnderNofile(want int64, argv ...string) *exec.Cmd {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return exec.Command("/bin/sh", "-c", fmt.Sprintf("ulimit -S -n %d; exec %s", want, strings.Join(quoted, " ")))
}

func assertNoNarrowingAfter(t *testing.T, asm []byte, call, narrow string) {
	t.Helper()
	at := bytes.Index(asm, []byte(call))
	if at < 0 {
		t.Fatalf("emitted asm has no %q", call)
	}
	tail := asm[at:]
	if len(tail) > 160 {
		tail = tail[:160]
	}
	if bytes.Contains(tail, []byte(narrow)) {
		t.Errorf("%q narrows the rlimit_nofile result to 32 bits:\n%s", narrow, tail)
	}
}

func TestSelfHostRlimitNofileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	want := nofileProbeLimit(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, rlimitSelfHostSource(want))
	if !bytes.Contains(asm, []byte("call __fn___fern_rlimit_nofile")) {
		t.Fatalf("emitted asm has no `call __fn___fern_rlimit_nofile` — rlimit_nofile did not lower through the x86-64 IR path")
	}
	assertNoNarrowingAfter(t, asm, "call __fn___fern_rlimit_nofile", "movslq %eax, %rax")
	progBin := buildBin(t, gcc, dir, "rlimit_prog", string(asm))
	run := runUnderNofile(want, append(append([]string{}, runner...), progBin)...)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (3 = disagreed with the %d this test imposed)\n%s", code, want, out)
	}
}

func TestSelfHostRlimitNofileIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	want := nofileProbeLimit(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, rlimitSelfHostSource(want), "-target", "arm64-linux")
	if !bytes.Contains(asm, []byte("bl __fn___fern_rlimit_nofile")) {
		t.Fatalf("emitted asm has no `bl __fn___fern_rlimit_nofile` — rlimit_nofile did not lower through the arm64 IR path")
	}
	assertNoNarrowingAfter(t, asm, "bl __fn___fern_rlimit_nofile", "sxtw x0, w0")
	bin := buildBinArm64(t, arm64gcc, dir, "rlimit_prog", string(asm))
	argv := []string{bin}
	if qemu != "" {
		argv = []string{qemu, bin}
	}
	run := runUnderNofile(want, argv...)
	out, _ := run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("probe did not exit normally:\n%s", out)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0 (3 = disagreed with the %d this test imposed)\n%s", code, want, out)
	}
}

func TestSelfHostRlimitNofileIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin)
	cmd.Stdin = strings.NewReader(rlimitSelfHostSource(1024))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm driver did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("wasm driver accepted rlimit_nofile; it enforces no resource limits")
	}
	if !strings.Contains(stderr.String(), "required by `rlimit_nofile`; targets providing it") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
