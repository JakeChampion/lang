package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const compilerCensusProgram = `function label(n: i32): string {
  if (n % 2 == 0) {
    return "an even number of items";
  }
  return "an odd number of items";
}

function sum(xs: i32[]): i32 {
  let t: i32 = 0;
  for x in xs {
    t = t + x;
  }
  return t;
}

function main(): i32 {
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 10) {
    xs = xs.append(i);
    i = i + 1;
  }
  print(label(xs.len()) + " in the list");
  return sum(xs);
}
`

// TestSelfHostCompilerFreesWhatItAllocatesX86_64 builds the self-host
// compiler with FERN_LEAKCHECK and requires every compile it runs to free
// what it allocated: each output form of each target, `-g`, and one of the
// compiler's own modules. A string builder the emitter takes the text of and
// never frees is the leak this caught: one per stack-op arm, per leaf body
// and per runtime round, and the 1 MiB instruction buffer of a binary.
func TestSelfHostCompilerFreesWhatItAllocatesX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)

	compiler := filepath.Join(dir, "fern-leakcheck")
	stage0 := e2eharness.Stage0Compiler(t)
	err := withBuildMemoryMB(e2eharness.DriverBuildWeightMB("fern.fern"), func() error {
		cmd := exec.Command(stage0, "-target", e2eharness.TargetX86_64Linux, "-o", compiler,
			filepath.Join(dir, "fern.fern"), stdlib)
		cmd.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build the leakcheck compiler: %v\n%s", err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	prog := filepath.Join(dir, "census_prog.fern")
	if err := os.WriteFile(prog, []byte(compilerCensusProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	x86Bin := filepath.Join(out, "prog-x86")
	for _, c := range []struct {
		name string
		args []string
		src  string
	}{
		{"x86-64 binary", []string{"-target", "x86-64-linux", "-o", x86Bin}, prog},
		{"x86-64 asm", []string{"-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(out, "x86.s")}, prog},
		{"x86-64 binary -g", []string{"-target", "x86-64-linux", "-g", "-o", filepath.Join(out, "x86-g")}, prog},
		{"arm64 binary", []string{"-target", "arm64-linux", "-o", filepath.Join(out, "arm64")}, prog},
		{"arm64 asm", []string{"-target", "arm64-linux", "-emit", "asm", "-o", filepath.Join(out, "arm64.s")}, prog},
		{"arm64-android binary", []string{"-target", "arm64-android", "-o", filepath.Join(out, "android")}, prog},
		{"arm64-darwin binary", []string{"-target", "arm64-darwin", "-o", filepath.Join(out, "darwin")}, prog},
		{"wasm core module", []string{"-target", "wasm32-wasi", "-emit", "core-module", "-o", filepath.Join(out, "prog.wasm")}, prog},
		{"x86-64 asm of checker.fern", []string{"-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(out, "checker.s")}, filepath.Join(dir, "checker.fern")},
	} {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			cmd := runnerCommand(runner, compiler, append(c.args, c.src, stdlib)...)
			cmd.Env = e2eharness.ChildEnv()
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("compile failed: %v\n%s", err, stderr.String())
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr.String()), &allocs, &frees, &live); err != nil || allocs == 0 {
				t.Fatalf("no leakcheck summary from the compiler: %v\n%s", err, stderr.String())
			}
			if allocs != frees || live != 0 {
				t.Errorf("compiler census allocs=%d frees=%d live_bytes=%d, want every allocation freed", allocs, frees, live)
			}
		})
	}

	run := runnerCommand(runner, x86Bin)
	got, _ := run.Output()
	if code := run.ProcessState.ExitCode(); code != 45 || string(got) != "an even number of items in the list\n" {
		t.Errorf("compiled program exited %d with %q, want 45 and its line", code, got)
	}
}

// runnerCommand runs bin under runner, the emulator prefix x86_64Tooling
// returns (empty on an x86-64 host).
func runnerCommand(runner []string, bin string, args ...string) *exec.Cmd {
	argv := append(append(append([]string{}, runner...), bin), args...)
	return exec.Command(argv[0], argv[1:]...)
}
