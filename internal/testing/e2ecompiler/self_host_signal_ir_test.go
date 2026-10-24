package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const signalDispositionProg = `function main(): i32 {
    if (args().len() == 2) { signal_ignore(13); }
    if (args().len() == 3) { signal_ignore(13); signal_default(13); }
    let i: i32 = 0;
    while (i < 200000) {
        print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");
        i = i + 1;
    }
    return 7;
}`

func runWithClosedStdout(t *testing.T, runner []string, prog string, argv ...string) int {
	t.Helper()
	script := `"$@" | head -c 1 >/dev/null; exit ${PIPESTATUS[0]}`
	full := append([]string{"-c", script, "bash"}, runner...)
	cmd := exec.Command("bash", append(append(full, prog), argv...)...)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

var signalDispositionCases = []struct {
	name string
	argv []string
	want int
}{
	{"default disposition kills the writer", nil, 141},
	{"signal_ignore drops the failing writes", []string{"ignore"}, 7},
	{"signal_default restores the kill", []string{"ignore", "restore"}, 141},
}

const signalReadOpsProg = `function main(): i32 {
    let bit: i64 = 2 as i64;
    signal_mask(1, bit);
    if ((signal_mask(0, 0 as i64) & bit) != (0 as i64)) { return 71; }
    let prev: i64 = signal_mask(0, bit);
    if ((prev & bit) != (0 as i64)) { return 72; }
    if ((signal_mask(0, 0 as i64) & bit) == (0 as i64)) { return 73; }
    let p2: i64 = signal_mask(1, bit);
    if ((p2 & bit) == (0 as i64)) { return 74; }
    if (signal_default(2) != 0) { return 75; }
    if (signal_disposition(2) != 0) { return 76; }
    if (signal_ignore(2) != 0) { return 77; }
    if (signal_disposition(2) != 1) { return 78; }
    if (signal_default(2) != 0) { return 79; }
    if (signal_disposition(2) != 0) { return 80; }
    return 0;
}`

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

func TestSelfHostSignalDispositionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(signalDispositionProg+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the signal-disposition program")
	}
	progBin := buildBin(t, gcc, dir, "signal_disposition", string(asm))

	for _, tc := range signalDispositionCases {
		if got := runWithClosedStdout(t, runner, progBin, tc.argv...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}

	readAsm := runCapture(t, gcc, runner, driverBin, []byte(signalReadOpsProg+"\n"))
	if len(readAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the signal read-ops program")
	}
	readBin := buildBin(t, gcc, dir, "signal_read_ops", string(readAsm))
	if got := runSignalReadOps(t, runner, readBin); got != 0 {
		t.Errorf("signal read ops: exit = %d, want 0 (the exit code names the failing step)", got)
	}
}

func TestSelfHostSignalDispositionIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "signal_arm64_mmc")

	srcFile := filepath.Join(t.TempDir(), "signal_disposition.fern")
	if err := os.WriteFile(srcFile, []byte(signalDispositionProg+"\n"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	out, err := runX86_64Bin(x86runner, mmc, srcFile, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed: %v", err)
	}
	asm := string(out)

	pushed := `\n    mov (x[0-9]+), #134\n(?:.*\n)*?    str \1, \[sp, #-16\]!\n`
	for _, sym := range []string{"__fn___fern_signal_ignore", "__fn___fern_signal_default"} {
		body := extractFuncBody(asm, sym)
		if body == "" {
			t.Fatalf("%s not defined — the Fern helper did not lower for arm64", sym)
		}
		if !matchShape(body, pushed) {
			t.Errorf("%s does not push Linux's rt_sigaction number (134):\n%s", sym, body)
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

	readSrc := filepath.Join(t.TempDir(), "signal_read_ops.fern")
	if err := os.WriteFile(readSrc, []byte(signalReadOpsProg+"\n"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	readOut, err := runX86_64Bin(x86runner, mmc, readSrc, "-target", "arm64-linux").Output()
	if err != nil {
		t.Fatalf("self-host arm64 emit failed for the read ops: %v", err)
	}
	readBin := buildBinArm64(t, gcc, dir, "signal_read_ops", string(readOut))
	if got := runSignalReadOps(t, runner, readBin); got != 0 {
		t.Errorf("signal read ops: exit = %d, want 0 (the exit code names the failing step)", got)
	}
}
