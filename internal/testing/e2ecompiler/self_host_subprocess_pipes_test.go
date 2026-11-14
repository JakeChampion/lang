package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each check is a child that the three pipes of subprocess must serve at the
// same time. The exit status is the number of the check that failed.
const subprocessPipesSrc = `import "std/string";

function main(): i32 {
  let big = subprocess("sh", ["-c", "head -c 300000 /dev/zero | tr '\\0' x"], "");
  if (big.exit_code != 0 || big.stdout.len() != 300000 || big.stdout[299999] != b'x') {
    return 1;
  }
  let input = "0123456789abcdef".repeat(65536);
  let echoed = subprocess("cat", [], input);
  if (echoed.exit_code != 0 || echoed.stdout != input) {
    return 2;
  }
  let both = subprocess("sh", ["-c", "head -c 200000 /dev/zero | tr '\\0' e >&2; echo done"], "");
  if (both.stderr.len() != 200000 || both.stdout != "done\n") {
    return 3;
  }
  let ignored = subprocess("true", [], input);
  if (ignored.exit_code != 0) {
    return 4;
  }
  let small = subprocess("sh", ["-c", "cat; exit 7"], "piped");
  if (small.exit_code != 7 || small.stdout != "piped") {
    return 5;
  }
  return 0;
}
`

func writeSubprocessPipesSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subprocess_pipes.fern")
	if err := os.WriteFile(path, []byte(subprocessPipesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Output past 64 KiB, a child that writes before it reads all of its input,
// a full standard error pipe, and a child that reads no input.
func TestSelfHostSubprocessPipesX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeSubprocessPipesSrc(t)
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(strings.TrimSuffix(mode, "=1"), func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
			if exit != 0 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want 0, and the sanitizer silent\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostSubprocessPipesArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	asm, err := os.ReadFile(cli.emit(t, writeSubprocessPipesSrc(t), "arm64-linux", "FERN_LEAKCHECK=1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "subprocess_pipes", string(asm)))
	var eb strings.Builder
	cmd.Stderr = &eb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, eb.String())
	}
	assertBalancedCensus(t, eb.String())
}
