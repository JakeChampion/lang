package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The self-host twin of TestGenericFnValue: the monomorphiser clones a generic
// named as a value for the callable it is wanted at.
func TestSelfHostGenericFnValue(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.GenericFnValueProgram, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}

// `-check` types every statement without lowering anything, so a generic
// named as a value has to settle to the callable it is wanted at there too,
// or a program the compiler accepts is one the checker refuses.
func TestSelfHostGenericFnValueChecks(t *testing.T) {
	driver, runner := selfHostCheckDriver(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.GenericFnValueProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driver, "-check", src, stdlib)
	} else {
		cmd = exec.Command(runner[0], append(append([]string(nil), runner[1:]...), driver, "-check", src, stdlib)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("self-host -check refused a program it compiles: %v\n%s", err, out)
	}
}
