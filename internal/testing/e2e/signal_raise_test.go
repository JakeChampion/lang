package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const signalRaiseSource = `function wait_taken(sig: i32): boolean {
    let n: i32 = 0;
    while (n < 5000) {
        if (signal_taken(sig)) { return true; }
        sleep_ms(1 as i64);
        n = n + 1;
    }
    return false;
}

function main(): i32 {
    let usr1: i32 = 10;
    if (target_os() == "darwin") { usr1 = 30; }
    if (signal_catch(usr1) != 0) { return 2; }
    if (signal_raise(usr1) != 0) { return 3; }
    if (!wait_taken(usr1)) { return 4; }
    // 0 is kill's existence check; a number past the last signal is EINVAL.
    if (signal_raise(0) != 0) { return 5; }
    if (signal_raise(65) != 0 - 22) { return 6; }
    if (signal_raise(0 - 1) != 0 - 22) { return 7; }
    if (args().len() > 1) {
        print("census");
        return 0;
    }
    print("raising");
    signal_raise(15);
    print("survived");
    return 8;
}
`

func checkRaised(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Run()
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("%v, want death by SIGTERM (an exit code names the failing step)\nstdout: %q\nstderr: %q", cmd.ProcessState, stdout.String(), stderr.String())
	}
	if stdout.String() != "raising\n" {
		t.Errorf("stdout = %q, want %q: nothing after the raise runs", stdout.String(), "raising\n")
	}
}

func checkCensusLeg(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	stdout, stderr, code := runSplit(t, cmd)
	if code != 0 || stdout != "census\n" {
		t.Fatalf("exit = %d, stdout %q; want 0 and %q — the code names the step\nstderr: %q", code, stdout, "census\n", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("leak census: allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
	}
}

func TestX86_64SignalRaise(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, signalRaiseSource, []string{"FERN_LEAKCHECK=1"})
	checkCensusLeg(t, runX86_64Bin(runner, bin, "census"))
	checkRaised(t, runX86_64Bin(runner, bin))
}

func TestArm64SignalRaise(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, signalRaiseSource, []string{"FERN_LEAKCHECK=1"})
	checkCensusLeg(t, runArm64Bin(qemu, bin, "census"))
	checkRaised(t, runArm64Bin(qemu, bin))
}

func TestInterpSignalRaise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX signal")
	}
	bin := buildLangBinForInterp(t)
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(signalRaiseSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-interp", p, "census").CombinedOutput()
	if code := exitCodeOf(err); code != 0 || string(out) != "census\n" {
		t.Fatalf("exit = %d, output %q; want 0 and %q — the code names the step", code, out, "census\n")
	}
	checkRaised(t, exec.Command(bin, "-interp", p))
}

func TestArm64DarwinSignalRaise(t *testing.T) {
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, signalRaiseSource, nil)
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	checkRaised(t, exec.Command(bin))
}

func TestWASMSignalRaiseUnsupported(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 40;
    if (signal_raise(15) != 0 - 58) { return 1; }
    n = n + 1;
    return n + 1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm signal_raise: got %d, want 42 (the raise answers -ENOTSUP; #11698)", got)
	}
}
