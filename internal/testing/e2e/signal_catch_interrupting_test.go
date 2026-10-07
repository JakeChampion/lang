// `signal_catch_interrupting` (#11698) end to end: a signal it catches ends a
// blocked call, where signal_catch's resumes it.
//
// The test is the signal's sender. Each program prints a line when it is
// about to block, and the test answers that line: it signals the program and
// waits for the next one. The signal is sent again every 200 ms until the
// line comes, because one landing before the program reaches its blocking
// call sets the flag and blocks nothing; the next one interrupts the call.
//
// The read program blocks on a pipe the test holds open and never writes to,
// so only the signal can end the read. Its last leg switches the same signal
// to signal_catch and checks the read then survives it: the program prints
// nothing until the test writes a byte. The waitpid program blocks on a child
// that sleeps until it is killed.
package e2e

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Each failing step returns its own code; a clean run prints the four step
// lines, the last `done`.
const signalInterruptReadSource = `function interrupted(r: Reader): i32 {
    match (r.read_chunk_bytes(16)) {
        Ok(b) => { return 1; },
        Err(e) => {
            match (e) {
                Interrupted => { return 0; },
                _ => { return 2; }
            }
        }
    }
    return 3;
}

function main(): i32 {
    let usr1: i32 = 10;
    if (target_os() == "darwin") { usr1 = 30; }
    if (signal_catch_interrupting(usr1) != 0) { return 10; }
    if (signal_disposition(usr1) != 2) { return 11; }
    if (signal_taken(usr1)) { return 12; }
    // The kernel's EINVAL for what cannot be caught, and for no signal.
    if (signal_catch_interrupting(9) != 0 - 22) { return 13; }
    if (signal_catch_interrupting(0) != 0 - 22) { return 14; }
    if (signal_catch_interrupting(65) != 0 - 22) { return 15; }
    let r: Reader = stdin();
    print("ready");
    let rc: i32 = interrupted(r);
    if (rc != 0) { return 20 + rc; }
    if (!signal_taken(usr1)) { return 24; }
    if (signal_taken(usr1)) { return 25; }
    // The catch stays installed: the next arrival ends the next read.
    print("again");
    rc = interrupted(r);
    if (rc != 0) { return 30 + rc; }
    if (!signal_taken(usr1)) { return 34; }
    // signal_catch puts SA_RESTART back, so the signal no longer ends the
    // read: it returns the byte written after the signal, and the signal is
    // taken all the same.
    if (signal_catch(usr1) != 0) { return 40; }
    print("restart");
    match (r.read_chunk_bytes(16)) {
        Ok(b) => { if (b.len() != 1) { return 41; } },
        Err(e) => { return 42; }
    }
    if (!signal_taken(usr1)) { return 43; }
    print("done");
    return 0;
}
`

// The child sleeps until the parent kills it, so the parent's wait can end
// only by the signal: -4, EINTR.
const signalInterruptWaitSource = `function main(): i32 {
    let usr1: i32 = 10;
    if (target_os() == "darwin") { usr1 = 30; }
    if (signal_catch_interrupting(usr1) != 0) { return 10; }
    let pid: i32 = proc_fork();
    if (pid < 0) { return 11; }
    if (pid == 0) {
        sleep_ms(60000 as i64);
        exit(0);
    }
    print("ready");
    if (proc_waitpid(pid) != 0 - 4) { return 12; }
    if (!signal_taken(usr1)) { return 13; }
    match (signal_send(pid, 9)) {
        Ok(_) => {},
        Err(_) => { return 14; }
    }
    if (proc_waitpid(pid) != 128 + 9) { return 15; }
    print("done");
    return 0;
}
`

func usr1() syscall.Signal {
	if runtime.GOOS == "darwin" {
		return syscall.Signal(30)
	}
	return syscall.SIGUSR1
}

// signalStep is one line the program prints and what the test does on it:
// signal until the next line (`restart` false), or signal once, check the
// read survives, then write the byte that ends it.
type signalStep struct {
	line    string
	restart bool
}

// driveSignalProgram runs cmd through `steps` and returns its stdout lines,
// its stderr and its exit code.
func driveSignalProgram(t *testing.T, cmd *exec.Cmd, steps []signalStep) ([]string, string, int) {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got []string
	fail := func(format string, args ...any) {
		t.Helper()
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf(format+"\nstdout so far: %q\nstderr: %q", append(args, got, stderr.String())...)
	}
	next := func(signal bool) (string, bool) {
		deadline := time.After(30 * time.Second)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		if signal {
			cmd.Process.Signal(usr1())
		}
		for {
			select {
			case l, ok := <-lines:
				if ok {
					got = append(got, l)
				}
				return l, ok
			case <-tick.C:
				if signal {
					cmd.Process.Signal(usr1())
				}
			case <-deadline:
				fail("no line within 30 s")
			}
		}
	}
	if l, ok := next(false); !ok || l != steps[0].line {
		fail("first line %q, want %q", l, steps[0].line)
	}
	for k, step := range steps {
		want := "done"
		if k+1 < len(steps) {
			want = steps[k+1].line
		}
		if step.restart {
			cmd.Process.Signal(usr1())
			select {
			case l, ok := <-lines:
				fail("the restarting catch let the read end (line %q, open %v)", l, ok)
			case <-time.After(500 * time.Millisecond):
			}
			io.WriteString(stdin, "x")
			if l, ok := next(false); !ok || l != want {
				fail("after the byte: %q, want %q", l, want)
			}
			continue
		}
		if l, ok := next(true); !ok || l != want {
			fail("after %q: %q, want %q", step.line, l, want)
		}
	}
	stdin.Close()
	for range lines {
	}
	cmd.Wait()
	return got, stderr.String(), cmd.ProcessState.ExitCode()
}

var (
	readSteps = []signalStep{{line: "ready"}, {line: "again"}, {line: "restart", restart: true}}
	waitSteps = []signalStep{{line: "ready"}}
)

// checkCensusRun checks a leak-census run: exit 0, and a census that balances.
func checkCensusRun(t *testing.T, stderr string, code int) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step\nstderr: %q", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("leak census: allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
	}
}

func TestX86_64SignalCatchInterruptingRead(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, signalInterruptReadSource, []string{"FERN_LEAKCHECK=1"})
	_, stderr, code := driveSignalProgram(t, runX86_64Bin(runner, bin), readSteps)
	checkCensusRun(t, stderr, code)
}

func TestX86_64SignalCatchInterruptingWait(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, signalInterruptWaitSource, []string{"FERN_LEAKCHECK=1"})
	_, stderr, code := driveSignalProgram(t, runX86_64Bin(runner, bin), waitSteps)
	checkCensusRun(t, stderr, code)
}

// Under qemu-user the host signal reaches the emulator, which delivers it to
// the guest's handler; a host read or wait4 the emulator is blocked in for
// the guest fails with EINTR, which qemu passes on unless the guest's
// sigaction asked for SA_RESTART.
func TestArm64SignalCatchInterruptingRead(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, signalInterruptReadSource, []string{"FERN_LEAKCHECK=1"})
	_, stderr, code := driveSignalProgram(t, runArm64Bin(qemu, bin), readSteps)
	checkCensusRun(t, stderr, code)
}

func TestArm64SignalCatchInterruptingWait(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, signalInterruptWaitSource, []string{"FERN_LEAKCHECK=1"})
	_, stderr, code := driveSignalProgram(t, runArm64Bin(qemu, bin), waitSteps)
	checkCensusRun(t, stderr, code)
}

// The oracle, through `fern -interp` in its own process. The interpreter
// cannot fork, so only the read program runs here.
func TestInterpSignalCatchInterruptingRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX signal")
	}
	bin := buildLangBinForInterp(t)
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(signalInterruptReadSource), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := driveSignalProgram(t, exec.Command(bin, "-interp", p), readSteps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step\nstderr: %q", code, stderr)
	}
}

// The Mach-O builds carry the catch with SA_RESTART left out of XNU's flags
// word, which only an Apple Silicon host can run; anywhere else the builds
// themselves are what is checked.
func TestArm64DarwinSignalCatchInterrupting(t *testing.T) {
	for name, src := range map[string]string{"read": signalInterruptReadSource, "wait": signalInterruptWaitSource} {
		t.Run(name, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, src, nil)
			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
				return
			}
			steps := readSteps
			if name == "wait" {
				steps = waitSteps
			}
			_, stderr, code := driveSignalProgram(t, exec.Command(bin), steps)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 — the code names the step\nstderr: %q", code, stderr)
			}
		})
	}
}

// wasm: nothing can deliver a signal, so the catch answers 0 as signal_catch
// does and nothing is ever taken.
func TestWASMSignalCatchInterruptingTakesNothing(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 40;
    if (signal_catch_interrupting(2) != 0) { return 1; }
    n = n + 1;
    if (signal_taken(2)) { return 2; }
    return n + 1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm signal_catch_interrupting: got %d, want 42 (the catch answers 0, nothing is taken; #11698)", got)
	}
}

// The CLI world grants the catch and the proxy world refuses it on `signal`,
// as for signal_catch.
func TestWASMSignalCatchInterruptingCapability(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return signal_catch_interrupting(2); }`)
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
	if len(vs) != 1 || vs[0].Builtin != "signal_catch_interrupting" || vs[0].Capability != "signal" {
		t.Errorf("wasm32-wasi-http: %+v, want signal_catch_interrupting refused on signal", vs)
	}
}
