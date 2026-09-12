package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// signalDispositionProg writes 200k lines to stdout and exits 7. Run with its
// stdout closed under it, the exit code says which disposition SIGPIPE was left
// in: 141 (128 + SIGPIPE) when the kernel kills it, 7 when the signal is
// ignored and the failing writes are simply dropped.
//
// The argument count selects the disposition, so one binary covers all three
// cases and the three runs differ only in argv — nothing about the program's
// own control flow can account for a difference between them.
const signalDispositionProg = `function main(): i32 {
    if (args().len() == 2) { signal_ignore(13); }
    if (args().len() == 3) { signal_ignore(13); signal_default(13); }
    var i: i32 = 0;
    while (i < 200000) {
        print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");
        i = i + 1;
    }
    return 7;
}`

// runWithClosedStdout runs prog (with extra argv) reading one byte of its
// output and then closing the pipe, and reports the exit status the shell saw
// for prog itself rather than for the reader. `runner` is the emulator prefix
// an aarch64 binary needs on an x86 host — qemu-user re-raises the guest's
// fatal signal on itself, so the disposition still reaches the shell — and is
// empty for a binary that runs natively.
func runWithClosedStdout(t *testing.T, runner []string, prog string, argv ...string) int {
	t.Helper()
	script := `"$@" | head -c 1 >/dev/null; exit ${PIPESTATUS[0]}`
	full := append([]string{"-c", script, "bash"}, runner...)
	cmd := exec.Command("bash", append(append(full, prog), argv...)...)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

// signalDispositionCases is the three-run contract both legs below assert: the
// default disposition still kills the process, ignoring SIGPIPE lets it run to
// its own exit, and signal_default puts the killing disposition back. The
// middle run alone would pass against an emitter that ignored the signal
// number and ignored everything, which is why the third is here.
var signalDispositionCases = []struct {
	name string
	argv []string
	want int
}{
	{"default disposition kills the writer", nil, 141},
	{"signal_ignore drops the failing writes", []string{"ignore"}, 7},
	{"signal_default restores the kill", []string{"ignore", "restore"}, 141},
}

// signalReadOpsProg exercises the two read ops the SIGPIPE contract cannot —
// the previous-mask return of signal_mask and the disposition enum — always
// as TRANSITIONS from a known state, because the binary inherits both from
// whatever exec'd it. SIGINT (2) is every kernel, so its bit works on both
// legs; the exit code names the failing step.
const signalReadOpsProg = `function main(): i32 {
    var bit: i64 = 2 as i64;
    signal_mask(1, bit);
    if ((signal_mask(0, 0 as i64) & bit) != (0 as i64)) { return 71; }
    var prev: i64 = signal_mask(0, bit);
    if ((prev & bit) != (0 as i64)) { return 72; }
    if ((signal_mask(0, 0 as i64) & bit) == (0 as i64)) { return 73; }
    var p2: i64 = signal_mask(1, bit);
    if ((p2 & bit) == (0 as i64)) { return 74; }
    if (signal_default(2) != 0) { return 75; }
    if (signal_disposition(2) != 0) { return 76; }
    if (signal_ignore(2) != 0) { return 77; }
    if (signal_disposition(2) != 1) { return 78; }
    if (signal_default(2) != 0) { return 79; }
    if (signal_disposition(2) != 0) { return 80; }
    return 0;
}`

// runSignalReadOps runs a program compiled from signalReadOpsProg to its own
// exit, with `runner` as the emulator prefix an aarch64 binary needs on an
// x86 host.
func runSignalReadOps(t *testing.T, runner []string, prog string) int {
	t.Helper()
	cmd := exec.Command(prog)
	if len(runner) != 0 {
		full := append(append([]string{}, runner...), prog)
		cmd = exec.Command(full[0], full[1:]...)
	}
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatal("signal read-ops program did not run")
	}
	return cmd.ProcessState.ExitCode()
}

// #8792: signal_ignore / signal_default must lower on the self-host x86-64 IR
// path, with native's shape — one i32 in, nothing a caller reads out.
func TestSelfHostSignalDispositionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host signal-disposition test runs host-native only (needs a real SIGPIPE)")
	}
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(signalDispositionProg+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the signal-disposition program")
	}
	progBin := buildBin(t, gcc, dir, "signal_disposition", string(asm))

	for _, tc := range signalDispositionCases {
		if got := runWithClosedStdout(t, nil, progBin, tc.argv...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}

	// The read ops make no SIGPIPE and cannot be probed by the disposition
	// contract, so they get their own run through the same driver.
	readAsm := runCapture(t, gcc, runner, driverBin, []byte(signalReadOpsProg+"\n"))
	if len(readAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the signal read-ops program")
	}
	readBin := buildBin(t, gcc, dir, "signal_read_ops", string(readAsm))
	if got := runSignalReadOps(t, nil, readBin); got != 0 {
		t.Errorf("signal read ops: exit = %d, want 0 (the exit code names the failing step)", got)
	}
}

// #8827: the arm64 sibling. Both dispositions reach aarch64 through
// asmcore.rt_src_signal_disposition, whose syscall number comes from
// asmcore.sysno — and that table had no arm64-linux `sigaction` row, so the
// helper issued -1, every sigaction failed with ENOSYS, and both dispositions
// were silent no-ops. Nothing upstream could see it: the helper was emitted,
// the op called it, and only the effect was missing. That is why this leg runs
// the binary instead of only reading the asm, and why the x86-64 test above
// stayed green throughout.
//
// It surfaced as tee(1): the self-host build died of SIGPIPE in every
// --output-error mode and of SIGINT under -i, where the native build did not.
func TestSelfHostSignalDispositionIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("needs a native x86 host to run the aarch64-emitting driver")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "signal_arm64_mmc")

	srcFile := filepath.Join(t.TempDir(), "signal_disposition.fern")
	if err := os.WriteFile(srcFile, []byte(signalDispositionProg+"\n"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	out, err := exec.Command(mmc, srcFile, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)

	// The number reaches the trap as a value pushed for __syscall4 rather than
	// the `mov x8, #N` a reader (or darwinize) would look for, so a wrong
	// sysno row is invisible everywhere but here and in the run below. Scope
	// it to each helper's own body: `mov x0, #134` proves nothing file-wide.
	for _, sym := range []string{"__fn___fern_signal_ignore", "__fn___fern_signal_default"} {
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Fatalf("%s not defined — the Fern helper did not lower for arm64", sym)
		}
		if !strings.Contains(body, "    mov x0, #134\n    str x0, [sp, #-16]!\n") {
			t.Errorf("%s does not push Linux's rt_sigaction number (134)", sym)
		}
	}

	progBin := buildBinArm64(t, gcc, dir, "signal_disposition", asm)
	var runner []string
	if qemu != "" {
		runner = []string{qemu}
	}
	for _, tc := range signalDispositionCases {
		if got := runWithClosedStdout(t, runner, progBin, tc.argv...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8827)", tc.name, got, tc.want)
		}
	}

	// The read ops go through the same arm64 driver and binary path, under
	// qemu where this host is x86.
	readSrc := filepath.Join(t.TempDir(), "signal_read_ops.fern")
	if err := os.WriteFile(readSrc, []byte(signalReadOpsProg+"\n"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	readOut, err := exec.Command(mmc, readSrc, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed for the read ops: %v", err)
	}
	readBin := buildBinArm64(t, gcc, dir, "signal_read_ops", string(readOut))
	if got := runSignalReadOps(t, runner, readBin); got != 0 {
		t.Errorf("signal read ops: exit = %d, want 0 (the exit code names the failing step)", got)
	}
}
