package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-host interpreter's OUTPUT, against the native interpreter as oracle.
//
// interp.fern implemented no I/O builtins at all — there was no `print`
// anywhere in its 4,138 lines — so interp_run.fern could report an exit code
// and nothing else. That made it the one playground entry point with no
// self-host counterpart for the output pane (#6643): a program that computes
// the right answer and a program that prints the right answer were
// indistinguishable.
//
// TestSelfHostInterpDriverX86_64 next door asserts exit codes only, which is
// why the gap survived: every case there passes whether or not anything was
// written. These compare all three channels.
//
// # Why the native interpreter is the oracle
//
// print / write / eprint differ only in the newline and the stream, and those
// are exactly the details a reimplementation gets subtly wrong — a trailing
// newline on write, stderr merged into stdout. Asserting against literals
// would pin whatever this implementation happens to do; asserting against
// internal/interp pins that the two agree, which is the property that matters
// for a second implementation of the same language.
//
// # One leg
//
// x86-64 only, unlike the exit-code driver test's two. What is under test is
// evaluator logic — a `name == "print"` arm in interp.fern's call_func — with
// no backend-specific lowering anywhere in it, so an arm64 leg would pay a
// second driver build to re-run the same interpreter branch.

var interpIOProgs = []struct {
	name string
	src  string
}{
	// print appends a newline; write does not. Both on stdout, so the
	// concatenation is what distinguishes them.
	{"print-adds-a-newline", `function main(): i32 {
  print("one");
  print("two");
  return 0;
}`},
	{"write-does-not", `function main(): i32 {
  write("a");
  write("b");
  write("c");
  return 0;
}`},
	{"print-and-write-interleaved", `function main(): i32 {
  write("no");
  write("newline");
  print("");
  print("then a line");
  return 3;
}`},
	// eprint goes to the other stream. A test that merged the two would pass
	// with eprint writing to stdout, so the streams are compared separately.
	{"eprint-goes-to-stderr", `function main(): i32 {
  print("out");
  eprint("err");
  return 0;
}`},
	{"only-stderr", `function main(): i32 {
  eprint("nothing on stdout");
  return 1;
}`},
	// The empty string is its own case on both: print("") is a bare newline,
	// write("") is nothing at all.
	{"empty-strings", `function main(): i32 {
  write("");
  print("");
  write("");
  return 0;
}`},
	// Output survives to the exit: a program that prints and then returns a
	// value must do both, which is the shape a playground actually runs.
	{"output-then-exit-code", `function main(): i32 {
  print("computed");
  return 42;
}`},
	// A string built at runtime rather than a literal, so the argument reaches
	// the builtin as a VString the evaluator produced.
	{"a-computed-string", `function main(): i32 {
  var a: string = "he";
  var b: string = "llo";
  print(a + b);
  return 0;
}`},
	// Inside a loop and a call, so the builtin is reached from more than the
	// top-level statement position.
	{"from-a-loop-and-a-call", `function shout(s: string): i32 {
  print(s);
  return 1;
}
function main(): i32 {
  var i: i32 = 0;
  var n: i32 = 0;
  while (i < 3) {
    n = n + shout("line");
    i = i + 1;
  }
  return n;
}`},
}

// runNativeInterp runs `src` through the Go interpreter, the oracle for the
// three channels.
func runNativeInterp(t *testing.T, fernBin, src string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	cmd := exec.Command(fernBin, "-interp", path)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostInterpIOX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	files := interpDriverFiles(t)
	interpAsm, progDir := compileFilesModload(t, runner, driverBin, files)
	if len(interpAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the interp driver")
	}
	interpBin := buildBin(t, gcc, progDir, "interp", interpAsm)
	fernBin := buildLangBinForInterp(t)

	for _, tc := range interpIOProgs {
		t.Run(tc.name, func(t *testing.T) {
			wantOut, wantErr, wantCode := runNativeInterp(t, fernBin, tc.src)

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(interpBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], interpBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			var out, errb strings.Builder
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()

			if got := out.String(); got != wantOut {
				t.Errorf("stdout differs from the native interpreter:\n got %q\nwant %q", got, wantOut)
			}
			if got := errb.String(); got != wantErr {
				t.Errorf("stderr differs from the native interpreter:\n got %q\nwant %q", got, wantErr)
			}
			if got := cmd.ProcessState.ExitCode(); got != wantCode {
				t.Errorf("exit %d, want %d (native interpreter)", got, wantCode)
			}
		})
	}
}
