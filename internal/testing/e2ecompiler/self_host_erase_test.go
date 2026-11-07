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

// A 48-byte box is 72 bytes with its header, and a raw allocation of that
// size takes the freed block back from the same free list without a
// zero-fill. The control releases the secret as is and must read its
// bytes back; the erase path must read zeros. The word secret is six u64
// elements of 0x4141414141414141, a value read at run time so the literal
// is a heap box, and it is the same 48 bytes.
const eraseSrc = `import "std/crypto";
function secret(n: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    b = b.with(i, 65 as u8);
    i = i + 1;
  }
  return b;
}
function release(n: i32): i32 {
  let b: u8[] = secret(n);
  return b.len();
}
function wipe(n: i32): i32 {
  let b: u8[] = secret(n);
  let len: i32 = b.len();
  crypto.erase(b);
  return len;
}
function secret_words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}
function release_words(v: u64): i32 {
  let w: u64[] = secret_words(v);
  return w.len();
}
function wipe_words(v: u64): i32 {
  let w: u64[] = secret_words(v);
  let len: i32 = w.len();
  crypto.erase(w);
  return len;
}
function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    let k: i32 = 0;
    while (k < 8) {
      let p: usize = __alloc(72);
      let i: i32 = 0;
      while (i < 72) {
        if (__load_u8(p + (i as usize)) == 65 as u8) { found = found + 1; }
        i = i + 1;
      }
      k = k + 1;
    }
    return found;
  }
}
function main(): i32 {
  let mode: string = "release";
  if (args().len() > 1) { mode = args()[1]; }
  let word: u64 = 4702111234474983744u64 + (args().len() as u64) - 1u64;
  if (mode == "erase") {
    wipe(48);
  } else if (mode == "erase-words") {
    wipe_words(word);
  } else if (mode == "release-words") {
    release_words(word);
  } else {
    release(48);
  }
  print(stale().to_string());
  return 0;
}
`

func TestSelfHostEraseZeroesStorage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(eraseSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			args := []string{"-target", target, "-o", bin, src, cli.stdlib}
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = childEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
			}
			run := func(mode string) int {
				t.Helper()
				var c *exec.Cmd
				if target == "arm64-linux" {
					c = runArm64Bin(qemu, bin, mode)
				} else {
					c = runX86_64Bin(cli.runner, bin, mode)
				}
				var stdout, stderr bytes.Buffer
				c.Stdout, c.Stderr = &stdout, &stderr
				if err := c.Run(); err != nil {
					t.Fatalf("%s: %v\nstdout: %s\nstderr: %s", mode, err, stdout.String(), stderr.String())
				}
				n, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
				if err != nil {
					t.Fatalf("%s printed %q", mode, stdout.String())
				}
				return n
			}
			for _, c := range []struct{ control, erase string }{
				{"release", "erase"},
				{"release-words", "erase-words"},
			} {
				if control := run(c.control); control < 48 {
					t.Fatalf("%s read %d secret bytes back; the raw allocation no longer observes the released block", c.control, control)
				}
				if got := run(c.erase); got != 0 {
					t.Fatalf("after %s the raw allocation read %d secret bytes back, want 0", c.erase, got)
				}
			}
		})
	}
}
