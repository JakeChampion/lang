package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var interpIOProgs = []struct {
	name string
	src  string
}{
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
	{"eprint-goes-to-stderr", `function main(): i32 {
  print("out");
  eprint("err");
  return 0;
}`},
	{"only-stderr", `function main(): i32 {
  eprint("nothing on stdout");
  return 1;
}`},
	{"empty-strings", `function main(): i32 {
  write("");
  print("");
  write("");
  return 0;
}`},
	{"output-then-exit-code", `function main(): i32 {
  print("computed");
  return 42;
}`},
	{"a-computed-string", `function main(): i32 {
  let a: string = "he";
  let b: string = "llo";
  print(a + b);
  return 0;
}`},
	{"putchar-writes-bytes-not-codepoints", `function main(): i32 {
  putchar(65);
  putchar(233);
  putchar(255);
  putchar(0);
  putchar(321);
  putchar(0 - 1);
  putchar(10);
  return 0;
}`},
	{"putchar-interleaved-with-write", `function main(): i32 {
  write("a");
  putchar(66);
  write("c");
  putchar(10);
  return 0;
}`},
	{"from-a-loop-and-a-call", `function shout(s: string): i32 {
  print(s);
  return 1;
}
function main(): i32 {
  let i: i32 = 0;
  let n: i32 = 0;
  while (i < 3) {
    n = n + shout("line");
    i = i + 1;
  }
  return n;
}`},
}

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
