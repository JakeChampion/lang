package e2ecompiler

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// subprocessDrainSrc sends and receives more than a pipe holds (#15923). Each
// check that fails returns its own exit code. A runtime that writes all of the
// input before it reads the output does not finish check 2.
const subprocessDrainSrc = `import "std/string";

function pattern(): string {
  let s = "0123456789abcdef";
  let i = 0;
  while (i < 16) {
    s = s + s;
    i += 1;
  }
  return s;
}

function main(): i32 {
  let big = pattern();
  if (big.len() != 1048576) { return 1; }
  let a = subprocess("cat", [], big);
  if (a.exit_code != 0 || a.stdout != big || a.stderr != "") { return 2; }
  let b = subprocess("sh", ["-c", "cat 1>&2"], big);
  if (b.exit_code != 0 || b.stdout != "" || b.stderr != big) { return 3; }
  let c = subprocess("sh", ["-c", "head -c 1048576 /dev/zero; cat"], big);
  if (c.exit_code != 0 || c.stdout.len() != 2097152 || c.stdout[0] != 0 || !c.stdout.ends_with(big)) { return 4; }
  let d = subprocess("sh", ["-c", "head -c 1048576 /dev/zero 1>&2; echo open"], "");
  if (d.exit_code != 0 || d.stdout != "open\n" || d.stderr.len() != 1048576) { return 5; }
  let e = subprocess("head", ["-c", "5"], big);
  if (e.exit_code != 0 || e.stdout != "01234") { return 6; }
  let f = subprocess("sh", ["-c", "kill -PIPE $$; exit 7"], "");
  if (f.exit_code != 141) { return 7; }
  return 0;
}
`

func writeSubprocessDrainSrc(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/subprocess_drain.fern"
	if err := os.WriteFile(path, []byte(subprocessDrainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runSubprocessDrain runs cmd, and stops it when the parent and the child wait
// on each other.
func runSubprocessDrain(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	var eb strings.Builder
	cmd.Stderr = &eb
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(2*time.Minute, func() { _ = cmd.Process.Kill() })
	_ = cmd.Wait()
	if !timer.Stop() {
		t.Fatalf("the program did not finish in 2 minutes\n%s", eb.String())
	}
	return eb.String(), cmd.ProcessState.ExitCode()
}

// Check 6 needs the parent to survive SIGPIPE, and check 7 needs the parent to
// give its child the signal mask it had before the call.
func TestSelfHostSubprocessDrainX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeSubprocessDrainSrc(t)
	for _, mode := range []string{"plain", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(strings.TrimSuffix(mode, "=1"), func(t *testing.T) {
			var env []string
			if mode != "plain" {
				env = []string{mode}
			}
			stderr, exit := runSubprocessDrain(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src, env...)))
			if exit != 0 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want 0, and the sanitizer silent\n%s", exit, stderr)
			}
			if mode == "FERN_LEAKCHECK=1" {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}

func TestSelfHostSubprocessDrainArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := writeSubprocessDrainSrc(t)
	for _, mode := range []string{"plain", "FERN_LEAKCHECK=1"} {
		t.Run(strings.TrimSuffix(mode, "=1"), func(t *testing.T) {
			var env []string
			if mode != "plain" {
				env = []string{mode}
			}
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", env...))
			if err != nil {
				t.Fatal(err)
			}
			stderr, exit := runSubprocessDrain(t, runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "subprocess_drain", string(asm))))
			if exit != 0 {
				t.Fatalf("exit = %d, want 0\n%s", exit, stderr)
			}
			if mode == "FERN_LEAKCHECK=1" {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}
