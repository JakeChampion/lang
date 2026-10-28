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

// A released block goes to the -sanitize quarantine, and a write into it
// after the release is reported when the quarantine evicts it.
const quarantineWriteAfterFreeSrc = `function main(): i32 {
  let keep: usize = 0;
  let i: i32 = 0;
  unsafe {
    keep = __alloc(64);
    __free(keep, 64);
    __store_u8(keep, 7);
    while (i < 40000) {
      let p: usize = __alloc(64);
      __free(p, 64);
      i = i + 1;
    }
  }
  print("survived");
  return 0;
}
`

// Sixteen blocks of one class are released and taken again 32 times. A
// free list that hands out its head gives the last released block first
// every time; the hardened list does so only by chance.
const quarantinePlacementSrc = `function main(): i32 {
  let ptrs: usize[] = [];
  let i: i32 = 0;
  unsafe {
    while (i < 16) {
      ptrs = ptrs.append(__alloc(64));
      i = i + 1;
    }
    let lifo: i32 = 0;
    let round: i32 = 0;
    while (round < 32) {
      i = 0;
      while (i < 16) {
        __free(ptrs[i], 64);
        i = i + 1;
      }
      let again: usize[] = [];
      i = 0;
      while (i < 16) {
        again = again.append(__alloc(64));
        i = i + 1;
      }
      if (again[0] == ptrs[15]) {
        lifo = lifo + 1;
      }
      ptrs = again;
      round = round + 1;
    }
    print(lifo.to_string());
  }
  return 0;
}
`

// Seventy large blocks come from the bump. The byte after the block named
// on the command line is read: under -harden the 64th block ends at a
// page the process cannot touch.
const quarantineGuardSrc = `import "std/i32";
import "std/result";
function main(): i32 {
  let which: i32 = i32.parse_i32_radix(args()[1], 10).unwrap_or(0);
  let blocks: usize[] = [];
  let i: i32 = 0;
  unsafe {
    while (i < 70) {
      blocks = blocks.append(__alloc(1048576));
      i = i + 1;
    }
    let end: usize = blocks[which] + 1048576;
    let v: i32 = __load_u8(end);
    print("read " + v.to_string());
  }
  return 0;
}
`

// Two hundred one-MiB strings are built and released. The release of a
// large block is accounted before the quarantine takes it, so the census
// balances, and the budget bounds the arena.
const quarantineLargeChurnSrc = `import "std/string";
function big(n: i32): i32 {
  let s: string = "x".repeat(n);
  return s.len();
}
function main(): i32 {
  let i: i32 = 0;
  let total: i64 = 0i64;
  while (i < 200) {
    total = total + big(1048576) as i64;
    i = i + 1;
  }
  print(total.to_string());
  print(__heap_bump_bytes().to_string());
  return 0;
}
`

// Every block shape the free paths release: string boxes with a data
// block, views, grown and fused strings, and the arrays that hold them.
const quarantineStringBlocksSrc = `import "std/string";
function main(): i32 {
  let total: i32 = 0;
  let round: i32 = 0;
  while (round < 64) {
    let line: string = "alpha,beta,gamma,delta,epsilon,zeta,eta,theta,iota,kappa,lambda,mu";
    let parts: string[] = line.split(",");
    let joined: string = parts.join("+") + round.to_string();
    let grown: string = "";
    let i: i32 = 0;
    while (i < parts.len()) {
      grown = grown + parts[i];
      i = i + 1;
    }
    total = total + parts.len() + joined.len() + grown.len();
    round = round + 1;
  }
  print(total.to_string());
  return 0;
}
`

type quarantineRun struct {
	stdout, stderr string
	code           int
	signaled       bool
}

// quarantineBuild compiles src for target with flags and runs the image
// with args, through QEMU for arm64.
func quarantineBuild(t *testing.T, cli *selfHostCLI, qemu, target, src string, flags []string, args ...string) quarantineRun {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "main")
	cargs := append(append([]string{}, flags...), "-target", target, "-o", bin, path, cli.stdlib)
	cmd := runX86_64Bin(cli.runner, cli.bin, cargs...)
	cmd.Env = childEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", cargs, err, out)
	}
	var c *exec.Cmd
	if target == "arm64-linux" {
		c = runArm64Bin(qemu, bin, args...)
	} else {
		c = runX86_64Bin(cli.runner, bin, args...)
	}
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	r := quarantineRun{stdout: stdout.String(), stderr: stderr.String()}
	if c.ProcessState != nil {
		r.code = c.ProcessState.ExitCode()
		r.signaled = !c.ProcessState.Exited()
	} else if err != nil {
		t.Fatalf("run %v: %v", flags, err)
	}
	return r
}

func TestSelfHostQuarantinePolicy(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			t.Run("write-after-free", func(t *testing.T) {
				r := quarantineBuild(t, cli, qemu, target, quarantineWriteAfterFreeSrc, []string{"-sanitize", "-quarantine", "1M"})
				if r.code != 124 || !strings.Contains(r.stderr, "fern-sanitizer: write-after-free (a quarantined block was written)") {
					t.Fatalf("exit %d, want 124 with the write-after-free report\n%s%s", r.code, r.stdout, r.stderr)
				}
				plain := quarantineBuild(t, cli, qemu, target, quarantineWriteAfterFreeSrc, nil)
				if plain.code != 0 || !strings.Contains(plain.stdout, "survived") {
					t.Fatalf("release build: exit %d\n%s%s", plain.code, plain.stdout, plain.stderr)
				}
			})
			t.Run("placement", func(t *testing.T) {
				plain := quarantineBuild(t, cli, qemu, target, quarantinePlacementSrc, nil)
				if plain.code != 0 || strings.TrimSpace(plain.stdout) != "32" {
					t.Fatalf("release build: exit %d, printed %q; want the head every time\n%s", plain.code, plain.stdout, plain.stderr)
				}
				hard := quarantineBuild(t, cli, qemu, target, quarantinePlacementSrc, []string{"-harden"})
				n, err := strconv.Atoi(strings.TrimSpace(hard.stdout))
				if hard.code != 0 || err != nil {
					t.Fatalf("-harden: exit %d, printed %q\n%s", hard.code, hard.stdout, hard.stderr)
				}
				if n >= 32 {
					t.Fatalf("-harden handed out the head on all 32 rounds; a random pick among eight does so with probability 8^-32")
				}
			})
			t.Run("guard-page", func(t *testing.T) {
				faulted := quarantineBuild(t, cli, qemu, target, quarantineGuardSrc, []string{"-harden"}, "63")
				if faulted.code == 0 || strings.Contains(faulted.stdout, "read ") {
					t.Fatalf("-harden: the byte after the 64th large block was readable: exit %d\n%s%s", faulted.code, faulted.stdout, faulted.stderr)
				}
				for _, which := range []string{"62", "64"} {
					ok := quarantineBuild(t, cli, qemu, target, quarantineGuardSrc, []string{"-harden"}, which)
					if ok.code != 0 || !strings.Contains(ok.stdout, "read ") {
						t.Fatalf("-harden: block %s: exit %d\n%s%s", which, ok.code, ok.stdout, ok.stderr)
					}
				}
				plain := quarantineBuild(t, cli, qemu, target, quarantineGuardSrc, nil, "63")
				if plain.code != 0 || !strings.Contains(plain.stdout, "read ") {
					t.Fatalf("release build: exit %d\n%s%s", plain.code, plain.stdout, plain.stderr)
				}
			})
			t.Run("string-blocks", func(t *testing.T) {
				for _, flags := range [][]string{{"-sanitize"}, {"-sanitize", "-quarantine", "1M"}, {"-harden"}} {
					r := quarantineBuild(t, cli, qemu, target, quarantineStringBlocksSrc, flags)
					if r.code != 0 || strings.TrimSpace(r.stdout) != "8630" {
						t.Fatalf("%v: exit %d, printed %q\n%s", flags, r.code, r.stdout, r.stderr)
					}
					if flags[0] != "-sanitize" {
						continue
					}
					summary := leakSummaryLine(r.stderr)
					var allocs, frees, live int64
					if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
						t.Fatalf("%v: parse %q: %v", flags, summary, err)
					}
					if allocs == 0 || allocs != frees || live != 0 {
						t.Errorf("%v: unbalanced census: %s", flags, summary)
					}
				}
			})
			t.Run("large-churn", func(t *testing.T) {
				r := quarantineBuild(t, cli, qemu, target, quarantineLargeChurnSrc, []string{"-sanitize", "-quarantine", "4M"})
				lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
				if r.code != 0 || len(lines) != 2 || lines[0] != "209715200" {
					t.Fatalf("exit %d, printed %q\n%s", r.code, r.stdout, r.stderr)
				}
				bump, err := strconv.ParseInt(lines[1], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				if bump > 8<<20 {
					t.Errorf("the arena grew by %d bytes under a 4M quarantine; want the budget plus one block", bump)
				}
				summary := leakSummaryLine(r.stderr)
				var allocs, frees, live int64
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatalf("parse %q: %v", summary, err)
				}
				if allocs == 0 || allocs != frees || live != 0 {
					t.Errorf("unbalanced census: %s", summary)
				}
			})
		})
	}
}

func TestSelfHostQuarantineFlag(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{[]string{"-quarantine", "1M"}, "needs -sanitize"},
		{[]string{"-sanitize", "-quarantine", "512K"}, "0 for no bound, or a size from 1M to 16G"},
		{[]string{"-sanitize", "-quarantine", "32G"}, "0 for no bound, or a size from 1M to 16G"},
		{[]string{"-sanitize", "-quarantine", "lots"}, "0 for no bound, or a size from 1M to 16G"},
	} {
		args := append(append([]string{}, tc.flags...), "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "main"), src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), tc.want) {
			t.Errorf("%v: err %v, want a refusal naming %q\n%s", tc.flags, err, tc.want, out)
		}
	}
}
