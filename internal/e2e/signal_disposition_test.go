package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
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
    var i: i32 = 0;
    while (i < 200000) {
        print("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");
        i = i + 1;
    }
    return 7;
}`

// signalDispositionCases is the table every backend below is driven through.
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

// buildNativeSignalProg compiles signalDispositionSrc with `emit` and links it
// with `gcc` plus `extra`, returning the binary's path.
func buildNativeSignalProg(t *testing.T, gcc string, extra []string, emit func(*ast.Program, *checker.Info) (string, error)) string {
	t.Helper()
	prog, err := parser.Parse(signalDispositionSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	args := append([]string{"-static", "-nostdlib"}, extra...)
	args = append(args, asmPath, "-o", binPath)
	if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	return binPath
}

// TestX86_64SignalDisposition drives the three cases through the x86-64
// emitter's rt_sigaction helpers.
func TestX86_64SignalDisposition(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildNativeSignalProg(t, gcc, []string{"-no-pie"}, x86_64.Emit)
	for _, tc := range signalDispositionCases {
		argv := append(append([]string{}, runner...), bin)
		if got := runWithStdoutClosed(t, append(argv, tc.argv...)...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}
}

// TestArm64SignalDisposition drives the same three through the arm64 emitter,
// whose helper shares one syscall row between Linux's 4-argument rt_sigaction
// and Darwin's 3-argument sigaction.
func TestArm64SignalDisposition(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	bin := buildNativeSignalProg(t, gcc, nil, arm64codegen.Emit)
	for _, tc := range signalDispositionCases {
		var argv []string
		if qemu != "" {
			argv = append(argv, qemu)
		}
		argv = append(argv, bin)
		if got := runWithStdoutClosed(t, append(argv, tc.argv...)...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}
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
// Nothing in either WASI world can deliver a signal, so the honest answer is
// to do nothing — the same shape as hostname() answering "" rather than
// failing. What that leaves to assert is that the program still runs: the
// calls have to lower, drop their argument, and leave the operand stack
// balanced, which a wrong stack effect would break long before anything about
// signals mattered. The return value is computed after both calls so a
// mis-balanced stack shows up as the wrong number rather than as a trap only.
func TestWASMSignalDispositionIsANoOp(t *testing.T) {
	src := `function main(): i32 {
    var n: i32 = 40;
    signal_ignore(13);
    n = n + 1;
    signal_default(2);
    return n + 1;
}`
	if got := runWasm(t, src); got != 42 {
		t.Errorf("wasm signal dispositions: got %d, want 42 (both calls are no-ops that consume their argument; #8792)", got)
	}
}

// TestArm64SSASignalDisposition drives the same three cases through the
// SSA-direct arm64 backend, whose helper table is a separate emitter from the
// stack-machine one above and so can regress on its own.
func TestArm64SSASignalDisposition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64-ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	fern := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fern, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(signalDispositionSrc+"\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	bin := filepath.Join(dir, "main.bin")
	// Not a coverage gap to skip over: every construct here is in the subset,
	// and a refusal is a regression in its own right.
	emit := exec.Command(fern, "-target", "arm64-linux", "-backend", "ssa", "-o", bin, src)
	if out, err := emit.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	for _, tc := range signalDispositionCases {
		var argv []string
		if qemu != "" {
			argv = append(argv, qemu)
		}
		argv = append(argv, bin)
		if got := runWithStdoutClosed(t, append(argv, tc.argv...)...); got != tc.want {
			t.Errorf("%s: exit = %d, want %d (#8792)", tc.name, got, tc.want)
		}
	}
}
