package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Each reuse takes the same freed block of 20 MiB again and writes all of it.
// The pad keeps that block in the part of the arena that asks for huge pages.
const forkReclaimProg = `import "std/worker";
import "std/time";
import "std/i64";
import "std/result";

function minflt(): i64 {
  match (read_file("/proc/self/stat")) {
    Ok(text) => {
      match (text.rsplit_once(")")) {
        Some(p) => { return i64.parse_i64(p.1.trim().split(" ")[7]).unwrap_or(-1i64); },
        None => { return -1i64; }
      }
    },
    Err(_) => { return -1i64; }
  }
}

function fill(n: i32, v: u8): u8[] {
  let a: u8[] = __alloc_u8(n);
  for i in 0..n { a = a.with(i, v); }
  return a;
}

function reuse(n: i32, v: u8): i64 {
  let before = minflt();
  let a = fill(n, v);
  let after = minflt();
  for i in 0..n / 4096 {
    if (a[i * 4096] != v || a[i * 4096 + 4095] != v) { exit(9); }
  }
  return after - before;
}

function main(): i32 {
  let pad = fill(4194304, 1u8);
  let n = 16777216;
  let fresh = reuse(n, 2u8);
  let quiet = reuse(n, 3u8);
  let w = worker.spawn((): i32 => {
    sleep_ms(200i64);
    return reuse(n, 5u8) as i32;
  });
  let forked = reuse(n, 4u8);
  let child = w.join();
  if (pad[4194303] != 1u8) { return 8; }
  print("fresh " + fresh.to_string() + " quiet " + quiet.to_string() + " forked " + forked.to_string() + " child " + child.to_string());
  return 0;
}
`

const noForkReclaimProg = `function main(): i32 {
  let a: u8[] = __alloc_u8(1048576);
  return a[7] as i32;
}
`

// After a fork, a freed large block shares its pages with the other process.
// The allocator gives back those pages before it uses the block again, so a
// write to them takes a fault for a zero page in place of a copy of 4 KiB.
func TestSelfHostForkGivesBackSharedLargeBlocks(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	forkSrc, plainSrc := filepath.Join(dir, "fork.fern"), filepath.Join(dir, "plain.fern")
	if err := os.WriteFile(forkSrc, []byte(forkReclaimProg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainSrc, []byte(noForkReclaimProg), 0o644); err != nil {
		t.Fatal(err)
	}
	counts := regexp.MustCompile(`^fresh (\d+) quiet (\d+) forked (\d+) child (\d+)$`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, forkSrc, target))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(asm), "__fern_large_reclaim:") {
				t.Fatal("a program that forks has no __fern_large_reclaim")
			}
			plain, err := os.ReadFile(cli.emit(t, plainSrc, target))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(plain), "__fern_large_reclaim") {
				t.Fatal("a program that does not fork calls __fern_large_reclaim")
			}
			for _, harden := range []bool{false, true} {
				var flags []string
				if harden {
					flags = append(flags, "-harden")
				}
				bin := compileCPUProfile(t, cli, forkReclaimProg, target, "", flags...)
				cmd := runX86_64Bin(cli.runner, bin)
				if target == "arm64-linux" {
					_, runner := arm64Tooling(t)
					cmd = runArm64Bin(runner, bin)
				}
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("harden=%v: %v: stdout %q; stderr %s", harden, err, &stdout, &stderr)
				}
				m := counts.FindStringSubmatch(strings.TrimSpace(stdout.String()))
				if m == nil {
					t.Fatalf("harden=%v: no fault counts in %q", harden, &stdout)
				}
				if target != "x86-64-linux" || len(cli.runner) != 0 {
					continue
				}
				n := make([]int64, 4)
				for i := range n {
					if n[i], err = strconv.ParseInt(m[i+1], 10, 64); err != nil {
						t.Fatal(err)
					}
				}
				fresh, quiet, forked, child := n[0], n[1], n[2], n[3]
				t.Logf("harden=%v: fresh=%d quiet=%d forked=%d child=%d", harden, fresh, quiet, forked, child)
				// The block is 20 MiB, so a copy of each 4 KiB page is about 4,100
				// faults. The two ends of the block that do not fill a huge page can
				// take 1,024 faults more than fresh memory does.
				if quiet > 64 {
					t.Errorf("harden=%v: a reuse with no fork took %d faults: the pages were given back", harden, quiet)
				}
				if forked > fresh+1024 {
					t.Errorf("harden=%v: a reuse after the fork took %d faults, fresh memory %d", harden, forked, fresh)
				}
				if child > fresh+1024 {
					t.Errorf("harden=%v: a reuse in the worker took %d faults, fresh memory %d", harden, child, fresh)
				}
			}
		})
	}
}
