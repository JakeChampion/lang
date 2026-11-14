package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Each function reads four or eight bytes of an array or a string that it
// owns, and the compiler joins the reads into one load. The storage must
// stay in memory until that load. Under -harden a released block is zero,
// so a release that comes too early gives 0.
const wordLoadAnchorSrc = `import "std/i32";
import "std/i64";
import "std/string";

@noinline
function fill(k: i32, n: i32): u8[] {
  let out: u8[] = [];
  for i in 0..n {
    out = out.append((k + i) as u8);
  }
  return out;
}

@noinline
function big4(k: i32): u32 {
  let b = fill(k, 4);
  return b[0] as u32 << 24 as u32 | b[1] as u32 << 16 as u32 | b[2] as u32 << 8 as u32 | b[3] as u32;
}

@noinline
function little4(k: i32): u32 {
  let b = fill(k, 4);
  return b[0] as u32 | b[1] as u32 << 8 as u32 | b[2] as u32 << 16 as u32 | b[3] as u32 << 24 as u32;
}

@noinline
function big8(k: i32): u64 {
  let b = fill(k, 8);
  return b[0] as u64 << 56 as u64 | b[1] as u64 << 48 as u64 | b[2] as u64 << 40 as u64 | b[3] as u64 << 32 as u64
    | b[4] as u64 << 24 as u64 | b[5] as u64 << 16 as u64 | b[6] as u64 << 8 as u64 | b[7] as u64;
}

@noinline
function offset4(k: i32, at: i32): u32 {
  let b = fill(k, 9);
  return b[at] as u32 << 24 as u32 | b[at + 1] as u32 << 16 as u32 | b[at + 2] as u32 << 8 as u32 | b[at + 3] as u32;
}

@noinline
function text4(k: i32): u32 {
  let s = "ab" + k.to_string() + "cd";
  return s[0] as u32 << 24 as u32 | s[1] as u32 << 16 as u32 | s[2] as u32 << 8 as u32 | s[3] as u32;
}

function main(): i32 {
  print(f"big4 {big4(1) as i64}");
  print(f"little4 {little4(1) as i64}");
  print(f"big8 {big8(1) as i64}");
  print(f"offset4 {offset4(1, 5) as i64}");
  print(f"text4 {text4(7) as i64}");
  return 0;
}
`

const wordLoadAnchorWant = "big4 16909060\nlittle4 67305985\nbig8 72623859790382856\noffset4 101124105\ntext4 1633826659\n"

func TestSelfHostWordLoadKeepsItsStorage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(wordLoadAnchorSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			build := runX86_64Bin(cli.runner, cli.bin, "-harden", "-target", target, "-o", bin, src, cli.stdlib)
			build.Env = childEnv()
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI: %v\n%s", err, out)
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
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != wordLoadAnchorWant {
				t.Errorf("the program printed %q, want %q", got, wordLoadAnchorWant)
			}
		})
	}
}
