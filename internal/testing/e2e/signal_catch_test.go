package e2e

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const signalCatchSource = `function wait_taken(sig: i32): boolean {
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
    // Nothing is taken before anything is caught.
    if (signal_taken(usr1)) { return 1; }
    if (signal_catch(usr1) != 0) { return 2; }
    if (signal_disposition(usr1) != 2) { return 3; }
    // Catching sets nothing by itself.
    if (signal_taken(usr1)) { return 4; }
    match (set_process_group(0, 0)) {
        Ok(_) => {},
        Err(_) => { return 5; }
    }
    match (signal_send(0, usr1)) {
        Ok(_) => {},
        Err(_) => { return 6; }
    }
    if (!wait_taken(usr1)) { return 7; }
    // The poll cleared it.
    if (signal_taken(usr1)) { return 8; }
    // And the catch is still installed: a second arrival is taken too.
    match (signal_send(0, usr1)) {
        Ok(_) => {},
        Err(_) => { return 9; }
    }
    if (!wait_taken(usr1)) { return 10; }
    if (signal_taken(usr1)) { return 11; }
    // SIGKILL cannot be caught, and numbers outside 1..64 name no signal:
    // the kernel's EINVAL, and nothing to take.
    if (signal_catch(9) != 0 - 22) { return 12; }
    if (signal_catch(0) != 0 - 22) { return 13; }
    if (signal_catch(65) != 0 - 22) { return 14; }
    if (signal_taken(0) || signal_taken(65) || signal_taken(0 - 1)) { return 15; }
    // signal_default takes the catch back off.
    if (signal_default(usr1) != 0) { return 16; }
    if (signal_disposition(usr1) != 0) { return 17; }
    print("caught");
    return 0;
}
`

func signalCatchCensus(t *testing.T, stdout, stderr string, code int) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalCatchSource)\nstdout: %q\nstderr: %q", code, stdout, stderr)
	}
	if stdout != "caught\n" {
		t.Errorf("stdout = %q, want %q", stdout, "caught\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("leak census: allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
	}
}

func TestX86_64SignalCatch(t *testing.T) {
	stdout, stderr, code := runLeakCheckX86_64(t, signalCatchSource)
	signalCatchCensus(t, stdout, stderr, code)
}

func TestArm64SignalCatch(t *testing.T) {
	stdout, stderr, code := runLeakCheckArm64(t, signalCatchSource)
	signalCatchCensus(t, stdout, stderr, code)
}

func TestInterpSignalCatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX signal")
	}
	out, code := runInterpExitCode(t, signalCatchSource)
	if code != 0 || out != "caught\n" {
		t.Fatalf("exit = %d, output %q; want 0 and %q — the code names the step (see signalCatchSource)", code, out, "caught\n")
	}
}

func TestArm64DarwinSignalCatch(t *testing.T) {
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, signalCatchSource, nil)
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	out, err := exec.Command(bin).CombinedOutput()
	if code := exitCodeOf(err); code != 0 || string(out) != "caught\n" {
		t.Fatalf("exit = %d, output %q; want 0 and %q — the code names the step (see signalCatchSource)", code, out, "caught\n")
	}
}

func TestWASMSignalCatchTakesNothing(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 40;
    if (signal_catch(10) != 0) { return 1; }
    n = n + 1;
    if (signal_taken(10)) { return 2; }
    return n + 1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm signal_catch / signal_taken: got %d, want 42 (the catch answers 0, nothing is taken; #9243)", got)
	}
}

func TestWASMSignalCatchCapability(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { signal_catch(10); if (signal_taken(10)) { return 1; } return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	if vs := platforms.Enforce(prog, "wasm32-wasi"); len(vs) != 0 {
		t.Errorf("wasm32-wasi refused %q on %q; wasi-cli grants signal", vs[0].Builtin, vs[0].Capability)
	}
	vs := platforms.Enforce(prog, "wasm32-wasi-http")
	if len(vs) == 0 {
		t.Fatal("wasm32-wasi-http accepted signal_catch; the proxy world has no process to deliver a signal to")
	}
	for _, v := range vs {
		if v.Capability != "signal" || !strings.HasPrefix(v.Builtin, "signal_") {
			t.Errorf("wasm32-wasi-http refused %q on %q, want the pair on signal", v.Builtin, v.Capability)
		}
	}
}
