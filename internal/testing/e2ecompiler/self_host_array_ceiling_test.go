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

// An array box holds at most 2^28 - 1 words, so its payload stays below
// 2^31 - 8 bytes and every length and byte count stays inside an i32. The
// first program asks for one word more and must be refused before any
// memory is touched. The others fill a box to the ceiling and append once,
// which must be refused at the grow instead of silently passing 2^31.
var arrayCeilingCases = []struct {
	name   string
	src    string
	stdout string
	memMB  int
}{
	{"bytes-past-ceiling", `function main(): i32 {
  let xs: u8[] = __alloc_u8(2147483641);
  print(xs.len().to_string());
  return 0;
}
`, "", 0},
	{"bytes-grow-past-ceiling", `function main(): i32 {
  let xs: u8[] = __alloc_u8(2147483640);
  print(xs.len().to_string());
  let ys: u8[] = xs.append(7 as u8);
  print(ys.len().to_string());
  return 0;
}
`, "2147483640\n", 2200},
	{"words-grow-past-ceiling", `function main(): i32 {
  let xs: i64[] = __alloc_i64(268435455);
  print(xs.len().to_string());
  let ys: i64[] = xs.append(7i64);
  print(ys.len().to_string());
  return 0;
}
`, "268435455\n", 2200},
}

func TestSelfHostArrayCeiling(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, tc := range arrayCeilingCases {
		t.Run(tc.name, func(t *testing.T) {
			run := func() error {
				for _, target := range []string{"x86-64-linux", "arm64-linux"} {
					t.Run(target, func(t *testing.T) {
						dir := t.TempDir()
						src := filepath.Join(dir, "main.fern")
						if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
							t.Fatal(err)
						}
						bin := filepath.Join(dir, "main")
						args := []string{"-target", target, "-o", bin, src, cli.stdlib}
						cmd := runX86_64Bin(cli.runner, cli.bin, args...)
						cmd.Env = childEnv()
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
						}
						var c *exec.Cmd
						if target == "arm64-linux" {
							c = runArm64Bin(qemu, bin)
						} else {
							c = runX86_64Bin(cli.runner, bin)
						}
						var stdout, stderr bytes.Buffer
						c.Stdout, c.Stderr = &stdout, &stderr
						_ = c.Run()
						code := c.ProcessState.ExitCode()
						if code != 134 || !strings.HasPrefix(stderr.String(), "fern: allocation size out of range\n") || stdout.String() != tc.stdout {
							t.Fatalf("exit %d, stdout %q, stderr %q; want 134 with the allocation size cause after %q", code, stdout.String(), stderr.String(), tc.stdout)
						}
					})
				}
				return nil
			}
			if tc.memMB == 0 {
				_ = run()
				return
			}
			if err := e2eharness.WithBuildMemoryMB(tc.memMB, run); err != nil {
				t.Fatal(err)
			}
		})
	}
}
