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

// A record of a `@secret` struct zeroes each numeric array that it holds
// alone when it is released (SEC-025). The arrays here are 48 bytes, which
// is 72 bytes with the header, and a raw allocation of 72 bytes takes a
// freed block of that class back without a zero-fill. The control is the same
// record without the attribute: it must read the secret bytes back. "shared"
// returns the byte array from the key, so that array must keep its bytes.
const secretSrc = `@secret
struct Key { bytes: u8[], words: u64[], n: i32 }

struct Plain { bytes: u8[], words: u64[], n: i32 }

function fill(n: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  for i in 0..n {
    b = b.with(i, 65 as u8);
  }
  return b;
}

function words(v: u64): u64[] {
  return [v, v, v, v, v, v];
}

@noinline
function key(v: u64): i32 {
  let k = Key { bytes: fill(48), words: words(v), n: 1 };
  return k.bytes.len() + k.words.len();
}

@noinline
function plain(v: u64): i32 {
  let k = Plain { bytes: fill(48), words: words(v), n: 1 };
  return k.bytes.len() + k.words.len();
}

@noinline
function shared(v: u64): u8[] {
  let b = fill(48);
  let k = Key { bytes: b, words: words(v), n: 1 };
  let n = k.n;
  return b;
}

function stale(): i32 {
  unsafe {
    let found: i32 = 0;
    for k in 0..8 {
      let p: usize = __alloc(72);
      for i in 0..72 {
        if (__load_u8(p + (i as usize)) == 65 as u8) { found += 1; }
      }
    }
    return found;
  }
}

function main(): i32 {
  let mode: string = args()[1];
  let word: u64 = 4702111234474983744u64 + (args().len() as u64) - 2u64;
  if (mode == "secret") {
    let n = key(word);
  } else if (mode == "shared") {
    let b = shared(word);
    print(b[0].to_string() + " " + b[47].to_string());
  } else {
    let n = plain(word);
  }
  print(stale().to_string());
  return 0;
}
`

func TestSelfHostSecretRecordWipesItsArrays(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(secretSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			args := []string{"-target", target, "-o", bin, src, cli.stdlib}
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = childEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
			}
			run := func(mode string) []string {
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
				return strings.Fields(stdout.String())
			}
			count := func(mode string, fields []string) int {
				t.Helper()
				n, err := strconv.Atoi(fields[len(fields)-1])
				if err != nil {
					t.Fatalf("%s printed %q", mode, fields)
				}
				return n
			}
			if got := count("plain", run("plain")); got < 48 {
				t.Fatalf("plain read %d secret bytes back; the raw allocation no longer observes the released block", got)
			}
			if got := count("secret", run("secret")); got != 0 {
				t.Fatalf("after the release of a @secret record the raw allocation read %d secret bytes back, want 0", got)
			}
			if got := run("shared"); len(got) != 3 || got[0] != "65" || got[1] != "65" {
				t.Fatalf("shared printed %q; the array that the caller still holds must keep its bytes", got)
			}
		})
	}
}
