package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Neither interpreter has a loader, so each one refuses a call of a C
// @import function with the same message, and runs no empty body in its
// place.
func TestInterpretersRefuseACImportCall(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := `@import("libc.so", "getpid")
function c_getpid(): i32;

function main(): i32 {
  print((c_getpid() > 0).to_string());
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "the interpreter cannot call `c_getpid`: it imports getpid from libc.so"
	for _, run := range []struct {
		name string
		cmd  *exec.Cmd
	}{
		{"self-host", runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib)},
		{"oracle", exec.Command(oracle, "-interp", src)},
	} {
		var stdout, stderr bytes.Buffer
		run.cmd.Stdout = &stdout
		run.cmd.Stderr = &stderr
		_ = run.cmd.Run()
		if run.cmd.ProcessState.ExitCode() == 0 {
			t.Errorf("%s: exit 0, want a failure", run.name)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: the program ran on: %q", run.name, stdout.String())
		}
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("%s: stderr %q has no %q", run.name, stderr.String(), want)
		}
	}
}
