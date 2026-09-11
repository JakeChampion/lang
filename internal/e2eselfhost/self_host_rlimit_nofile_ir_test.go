package e2eselfhost

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// `rlimit_nofile` (#8819) through the SELF-HOST IR path, on each backend that
// emits it. The op takes no operands and pushes the soft RLIMIT_NOFILE as a
// full-width i64 — asmcore.rt_src_rlimit_nofile, reached as
// `call __fn___fern_rlimit_nofile`.
//
// The probe compares against a limit the HARNESS chose, not against a range:
// each run is wrapped in `ulimit -S -n <want>`, so the child's soft limit is a
// number this test set and the hard limit is left where it was. Every way the
// helper can be wrong produces a plausible number otherwise —
//
//   - reading rlim_max (offset 8) instead of rlim_cur gives the hard limit,
//     which `want` is deliberately not,
//   - the wrong resource id gives some other ceiling entirely (RLIMIT_NPROC,
//     RLIMIT_STACK),
//   - the wrong syscall number fails the call, which the helper normalises to
//     i64 max,
//   - letting Linux's all-ones RLIM_INFINITY through unclamped reads as -1,
//   - lowering to nothing pushes whatever was already on the stack.
//
// One thing no RUN can measure: the result's WIDTH. Linux refuses to set
// RLIMIT_NOFILE above /proc/sys/fs/nr_open, so no soft limit this test can
// impose exceeds the i32 range, and every such value survives a sign-extend
// unchanged. Only the unlimited normalisation (i64 max) would tell the two
// apart, and it is unreachable for the same reason. The width is therefore
// pinned STRUCTURALLY instead, by assertNoNarrowingAfter: without the i64
// arms in irlower, `var n: i64 = rlimit_nofile()` lowers as a widened i32 and
// the backend narrows the pushed result right after the call — which would
// turn i64 max into -1 on a host that does report unlimited.
//
// wasm has no leg beyond a refusal: neither preview enforces a resource limit,
// so platforms.fern withholds the builtin on `rlimit`.
func rlimitSelfHostSource(want int64) string {
	return fmt.Sprintf(`function main(): i32 {
    var n: i64 = rlimit_nofile();
    // All-ones RLIM_INFINITY reaching a caller unclamped reads as -1.
    if (n < (0 as i64)) { return 1; }
    // Every kernel enforces a ceiling above the three standard descriptors.
    if (n < (4 as i64)) { return 2; }
    if (n != (%d as i64)) { return 3; }
    return 0;
}`, want)
}

// nofileProbeLimit is a soft RLIMIT_NOFILE this test imposes on the probe: a
// value that is neither the hard limit nor the limit the test process itself
// runs under, so reading either of those instead fails rather than coincides.
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

// runUnderNofile runs argv with the child's soft RLIMIT_NOFILE set to `want`,
// leaving the hard limit alone. A shell's `ulimit` is the only portable way to
// set a child's limit from Go, and `exec` keeps the shell from adding a process
// between it and the probe.
//
// `-S` is load-bearing: a bare `ulimit -n N` sets BOTH limits, which would make
// a helper reading rlim_max agree with one reading rlim_cur and let the whole
// hard-vs-soft question through untested.
func runUnderNofile(want int64, argv ...string) *exec.Cmd {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return exec.Command("/bin/sh", "-c", fmt.Sprintf("ulimit -S -n %d; exec %s", want, strings.Join(quoted, " ")))
}

// assertNoNarrowingAfter fails when `narrow` appears in the instructions that
// immediately follow `call`, which is where a lost i64 width tracking puts the
// backend's 32-bit sign-extend of an already-full-width result.
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

// TestSelfHostRlimitNofileIRX86_64 compiles the probe through the production
// x86-64 IR driver and runs it under a soft limit this test picked.
func TestSelfHostRlimitNofileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	want := nofileProbeLimit(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, runner, driverBin, rlimitSelfHostSource(want), "-ir")
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

// TestSelfHostRlimitNofileIRArm64 runs the same probe through the arm64 IR
// backend under qemu, where getrlimit is asm-generic 163. qemu-user passes the
// process's rlimits through unchanged, so the soft limit set outside it is the
// one the guest reads.
func TestSelfHostRlimitNofileIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	want := nofileProbeLimit(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runSelfHostDriverStdin(t, x86runner, driverBin, rlimitSelfHostSource(want), "-target", "arm64-linux", "-ir")
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

// TestSelfHostRlimitNofileIRWasmRefused pins the wasm answer, which is a
// refusal NAMING THE BUILTIN rather than a fabricated ceiling: neither preview
// enforces resource limits, and both "unlimited" and a plausible 1024 would be
// measurements a component never took.
func TestSelfHostRlimitNofileIRWasmRefused(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cmd := runX86_64Bin(runner, driverBin, "-ir")
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
	if !strings.Contains(stderr.String(), "rlimit_nofile is not supported on the wasm target") {
		t.Errorf("wasm refusal does not name the builtin:\n%s", stderr.String())
	}
}
