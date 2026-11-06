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

// A released secret, read back through raw allocations of its block size.
// "small" is a 48-byte array (a 72-byte block) and "large" a 600000-byte
// array, which goes to the large free lists. "link" frees two raw blocks of
// one class and reads the first word of the block it takes back, which held
// the free list link. "rewind" writes a raw block, rewinds the bump past it
// with __heap_release_to, and reads the block that the bump hands out again.
const scrubSrc = `function secret(n: i32): u8[] {
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
function stale(size: i32): i32 {
  unsafe {
    let found: i32 = 0;
    for k in 0..8 {
      let p: usize = __alloc(size);
      for i in 0..size {
        if (__load_u8(p + (i as usize)) == 65 as u8) { found = found + 1; }
      }
    }
    return found;
  }
}
function link(): i64 {
  unsafe {
    let a: usize = __alloc(72);
    let b: usize = __alloc(72);
    __free(a, 72);
    __free(b, 72);
    return __load_i64(__alloc(72));
  }
}
function rewind(): i32 {
  unsafe {
    let m: i64 = __heap_mark();
    let p: usize = __alloc(64);
    for i in 0..64 {
      __store_u8(p + (i as usize), 65 as u8);
    }
    __heap_release_to(m);
    let q: usize = __alloc(64);
    let found: i32 = 0;
    for i in 0..64 {
      if (__load_u8(q + (i as usize)) == 65 as u8) { found = found + 1; }
    }
    return found;
  }
}
function main(): i32 {
  let mode: string = args()[1];
  if (mode == "link") {
    if (link() == 0 as i64) { print("0"); } else { print("1"); }
  } else if (mode == "rewind") {
    print(rewind().to_string());
  } else if (mode == "large") {
    release(600000);
    print(stale(600024).to_string());
  } else {
    release(48);
    print(stale(72).to_string());
  }
  return 0;
}
`

func TestSelfHostHardenScrubsReleasedBlocks(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(scrubSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			build := func(name string, flags ...string) string {
				t.Helper()
				bin := filepath.Join(dir, name)
				args := append(append([]string{"-target", target}, flags...), "-o", bin, src, cli.stdlib)
				cmd := runX86_64Bin(cli.runner, cli.bin, args...)
				cmd.Env = childEnv()
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
				}
				return bin
			}
			run := func(bin, mode string) int {
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
					t.Fatalf("%s %s: %v\nstdout: %s\nstderr: %s", filepath.Base(bin), mode, err, stdout.String(), stderr.String())
				}
				n, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
				if err != nil {
					t.Fatalf("%s %s printed %q", filepath.Base(bin), mode, stdout.String())
				}
				return n
			}
			release := build("release")
			hardened := build("hardened", "-harden")
			for _, c := range []struct {
				mode string
				want int
			}{{"small", 48}, {"large", 600000}, {"link", 1}, {"rewind", 64}} {
				if got := run(release, c.mode); got < c.want {
					t.Errorf("%s: the release build read %d, want at least %d; the probe no longer observes the released block", c.mode, got, c.want)
				}
				if got := run(hardened, c.mode); got != 0 {
					t.Errorf("%s: under -harden the raw allocation read %d, want 0", c.mode, got)
				}
			}
		})
	}
}
