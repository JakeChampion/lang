package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The release build recycles the one block through its free list, so the
// bump stays within a page. Under -sanitize every released block stays in
// the quarantine, so the bump grows by the 88-byte box per release. The
// numbers pin the current policy; a bounded quarantine moves the second.
const quarantineChurnSrc = `function hold(n: i32): i32 {
  let b: u8[] = __alloc_u8(n);
  return b.len();
}
function main(): i32 {
  let i: i32 = 0;
  let total: i32 = 0;
  while (i < 100000) {
    total = total + hold(64);
    i = i + 1;
  }
  print(total.to_string());
  print(__heap_bump_bytes().to_string());
  return 0;
}
`

func TestSelfHostSanitizeQuarantineGrowth(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(quarantineChurnSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bump := func(flags ...string) int64 {
				t.Helper()
				bin := filepath.Join(t.TempDir(), "main")
				args := append(append([]string{}, flags...), "-target", target, "-o", bin, src, cli.stdlib)
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
				if err := c.Run(); err != nil {
					t.Fatalf("%v: %v\n%s", flags, err, stderr.String())
				}
				lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
				if len(lines) != 2 || lines[0] != "6400000" {
					t.Fatalf("%v printed %q", flags, stdout.String())
				}
				n, err := strconv.ParseInt(lines[1], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			if plain := bump(); plain > 4096 {
				t.Errorf("release build bumped %d bytes for 100000 releases of one block; want recycling within a page", plain)
			}
			if san := bump("-sanitize"); san < 100000*88 {
				t.Errorf("-sanitize bumped %d bytes; the unbounded quarantine keeps 88 bytes per release, 8800000 in all", san)
			}
		})
	}
}
