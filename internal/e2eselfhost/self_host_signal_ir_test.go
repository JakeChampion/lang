package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
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
// for prog itself rather than for the reader.
func runWithClosedStdout(t *testing.T, prog string, argv ...string) int {
	t.Helper()
	script := `"$0" "$@" | head -c 1 >/dev/null; exit ${PIPESTATUS[0]}`
	cmd := exec.Command("bash", append([]string{"-c", script, prog}, argv...)...)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

// #8792: signal_ignore / signal_default must lower on the self-host x86-64 IR
// path, with native's shape — one i32 in, nothing a caller reads out.
//
// The three runs pin the whole contract: the default disposition still kills
// the process, ignoring SIGPIPE lets it run to its own exit, and
// signal_default puts the killing disposition back. The middle run alone would
// pass against an emitter that ignored the signal number and ignored
// everything, which is why the third is here.
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

	for _, tc := range []struct {
		name string
		argv []string
		want int
	}{
		{"default disposition kills the writer", nil, 141},
		{"signal_ignore drops the failing writes", []string{"ignore"}, 7},
		{"signal_default restores the kill", []string{"ignore", "restore"}, 141},
	} {
		if got := runWithClosedStdout(t, progBin, tc.argv...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}
}
