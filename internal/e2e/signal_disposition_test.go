package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// #8792: `signal_ignore(sig)` / `signal_default(sig)` set one signal's
// disposition to SIG_IGN / SIG_DFL. `tee` (#8327) is blocked on them — `-i` is
// SIG_IGN on SIGINT and the whole `--output-error` family is the same move on
// SIGPIPE, which turns the death into an EPIPE the write can report.
//
// SIGPIPE is what makes the primitive observable without installing a handler:
// a program whose stdout is closed under it either dies (141 = 128 + SIGPIPE)
// or does not, and nothing else about it changes. The three runs pin the whole
// contract — the default disposition still kills, ignoring lets the program
// reach its own exit, and signal_default puts the kill back. The middle run
// alone would pass against a `signal_ignore` that ignored its argument and
// every signal, which is why the third one is here.
//
// The argument count selects the disposition, so one binary covers all three
// and the runs differ only in argv: nothing in the program's own control flow
// can account for a difference between them.
const signalDispositionSrc = `function main(): i32 {
    if (args().len() == 2) { signal_ignore(13); }
    if (args().len() == 3) { signal_ignore(13); signal_default(13); }
    let i: i32 = 0;
    while (i < 200000) {
        print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");
        i = i + 1;
    }
    return 7;
}`

// signalDispositionCases is the table every leg below is driven through; the
// self-host's x86-64 and arm64 legs are in internal/e2eselfhost's
// self_host_signal_ir_test.go.
var signalDispositionCases = []struct {
	name string
	argv []string
	want int
}{
	{"default disposition kills the writer", nil, 141},
	{"signal_ignore drops the failing writes", []string{"ignore"}, 7},
	{"signal_default restores the kill", []string{"ignore", "restore"}, 141},
}

// runWithStdoutClosed runs `argv` with its stdout on a pipe whose reader takes
// one byte and leaves, and reports the exit status of argv itself rather than
// of the reader — `head`'s own status would be 0 either way. `argv[0]` may be
// an emulator, with the binary after it.
func runWithStdoutClosed(t *testing.T, argv ...string) int {
	t.Helper()
	const script = `"$@" | head -c 1 >/dev/null; exit ${PIPESTATUS[0]}`
	cmd := exec.Command("bash", append([]string{"-c", script, "signal-disposition"}, argv...)...)
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

// TestInterpSignalDisposition drives the same three cases through the
// interpreter, which is the oracle the compiled backends are diffed against.
//
// It runs `fern -interp` as its own process rather than calling the builtins
// in-process: a disposition is per-process state, so setting one inside the
// test binary would outlive the test. Going through the CLI is also the only
// way to observe the thing being asserted — the interpreter shares its process
// with the Go runtime, whose own SIGPIPE handling is what `signal.Ignore` has
// to displace for the second case to reach exit 7.
func TestInterpSignalDisposition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SIGPIPE observable needs a POSIX signal")
	}
	dir := t.TempDir()
	fern := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fern, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(signalDispositionSrc+"\n"), 0o644); err != nil {
		t.Fatalf("write prog.fern: %v", err)
	}
	for _, tc := range signalDispositionCases {
		argv := []string{fern, "-interp", src}
		if len(tc.argv) != 0 {
			argv = append(append(argv, "--"), tc.argv...)
		}
		if got := runWithStdoutClosed(t, argv...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}
}

// TestWASMSignalDispositionIsANoOp: both calls must lower and run on wasm.
//
// Nothing in either WASI world can deliver a signal, so the correct answer is
// to do nothing — the same shape as hostname() answering "" rather than
// failing. What that leaves to assert is that the program still runs: the
// calls have to lower, drop their argument, and leave the operand stack
// balanced, which a wrong stack effect would break long before anything about
// signals mattered. The return value is computed after both calls so a
// mis-balanced stack shows up as the wrong number rather than as a trap only.
func TestWASMSignalDispositionIsANoOp(t *testing.T) {
	src := `function main(): i32 {
    let n: i32 = 40;
    signal_ignore(13);
    n = n + 1;
    signal_default(2);
    return n + 1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm signal dispositions: got %d, want 42 (both calls are no-ops that consume their argument; #8792)", got)
	}
}

// A signal number outside 1..64 must be a no-op in the interpreter, as it is on
// every compiled backend, and must above all RETURN.
//
// This is a hang, not a wrong answer. Go's runtime indexes a 65-entry sigtable
// and `signal.Stop` outside it never returns — measured: 0..64 return, -1 / 65
// / 99 deadlock — and `signal_default` reaches Stop through the Notify/Stop/
// Reset sequence that undoes an earlier Ignore. The compiled backends hand the
// number to rt_sigaction, get EINVAL and ignore it, which is the contract
// `std/signal` documents. Found by review on #8792.
//
// It has to run the interpreter as a SUBPROCESS. An in-process version of this
// test passes with the guard removed: by then the test binary has os/signal's
// goroutine running, so Stop finds it and returns, while a fresh `fern -interp`
// deadlocks. The bug is only visible where it actually happens.
func TestInterpSignalDispositionOutOfRangeDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal numbering")
	}
	dir := t.TempDir()
	fern := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fern, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	for _, sig := range []string{"0 - 1", "0", "65", "99", "1000000"} {
		for _, fn := range []string{"signal_ignore", "signal_default"} {
			src := filepath.Join(dir, "prog.fern")
			body := "function main(): i32 { " + fn + "(" + sig + "); return 7; }\n"
			if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
				t.Fatalf("write prog.fern: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := exec.CommandContext(ctx, fern, "-interp", src).Run()
			timedOut := ctx.Err() != nil
			cancel()
			if timedOut {
				t.Errorf("%s(%s) HUNG — the out-of-range guard is gone (#8792)", fn, sig)
				continue
			}
			if code := exitCodeOf(err); code != 7 {
				t.Errorf("%s(%s) exit = %d, want 7", fn, sig, code)
			}
		}
	}
}
