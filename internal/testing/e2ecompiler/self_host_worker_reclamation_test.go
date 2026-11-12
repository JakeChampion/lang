package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Each worker touches a private one-MiB array. VmSize also detects a region
// that remains mapped after all its reference counts reach zero.
const workerRegionGrowthProg = `import "std/worker";
import "std/string";

function report(tag: string): void {
  match (read_file("/proc/self/status")) {
    Ok(text) => {
      for line in text.split("\n") {
        if (line.starts_with("VmSize:")) { print(tag + line); }
      }
    },
    Err(_) => { eprint("status read failed"); exit(7); }
  }
}

function job(): i32 {
  let bytes: u8[] = __alloc_u8(1048576);
  for i in 0..256 { bytes = bytes.with(i * 4096, 7u8); }
  return bytes[0] as i32;
}

function main(): i32 {
  report("before ");
  for i in 0..%d {
    let w = worker.spawn((): i32 => job());
    if (w.join() != 7) { return 8; }
  }
  report("after ");
  return 0;
}
`

func TestSelfHostWorkerReclaimsJoinedRegions(t *testing.T) {
	cli := buildSelfHostCLI(t)
	sizes := regexp.MustCompile(`(?m)^(before|after) VmSize:\s+(\d+) kB$`)
	leaks := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		for _, rounds := range []int{4, 32} {
			for _, sanitize := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/joins%d/sanitize%v", target, rounds, sanitize), func(t *testing.T) {
					var flags []string
					if sanitize {
						flags = append(flags, "-sanitize")
					}
					bin := compileCPUProfile(t, cli, fmt.Sprintf(workerRegionGrowthProg, rounds), target, "", flags...)
					var cmd *exec.Cmd
					if target == "arm64-linux" {
						_, runner := arm64Tooling(t)
						cmd = runArm64Bin(runner, bin)
					} else {
						cmd = runX86_64Bin(cli.runner, bin)
					}
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					if err := cmd.Run(); err != nil {
						t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
					}
					m := sizes.FindAllStringSubmatch(stdout.String(), -1)
					if len(m) != 2 || m[0][1] != "before" || m[1][1] != "after" {
						t.Fatalf("missing memory measurements: %q", &stdout)
					}
					before, err := strconv.ParseInt(m[0][2], 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					after, err := strconv.ParseInt(m[1][2], 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					// Reject the mapping growth that the original worker retained.
					if delta := after - before; delta >= 1073741824/1024 {
						t.Errorf("joined worker regions remain mapped: VmSize grew %d kB", delta)
					}
					if sanitize {
						if m := leaks.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
							t.Fatalf("unbalanced sanitized run: %s", &stderr)
						}
					}
					t.Logf("VmSize before=%d after=%d kB", before, after)
				})
			}
		}
	}
}

// A zero-length munmap must fail. The next successful removal must balance
// the census once, for both a completed child and an unused region.
const workerUnmapFailureProg = `function main(): i32 {
  unsafe {
    let size: i64 = 1048576i64;
    let region: i64 = __worker_region(size);
    if (region <= 0i64) { return 91; }
    let pid: i32 = proc_fork();
    if (pid < 0) { return 92; }
    if (pid == 0) {
      __heap_adopt(region + 64i64, region + size);
      let root: usize = __send_root(__send_copy(Some("worker-" + 7.to_string())));
      __worker_exit(region, root);
    }
    if (proc_waitpid(pid) != 0) { return 93; }
    let root: usize = __worker_reap(region);
    if (root == 0) { return 94; }
    let old: Option[string] = __send_adopt(root);
    let copied: Option[string] = __send_copy(old);
    __send_root(old);
    if (__worker_unmap(region, 0i64, true) >= 0) { return 95; }
    if (__worker_unmap(region, size, true) != 0) { return 96; }
    match (copied) {
      Some(value) => { if (value != "worker-7") { return 97; } },
      None => { return 98; }
    }
    let unused: i64 = __worker_region(size);
    if (unused <= 0i64) { return 99; }
    if (__worker_unmap(unused, 0i64, false) >= 0) { return 100; }
    if (__worker_reap(unused) != 0) { return 101; }
    if (__worker_unmap(unused, size, false) != 0) { return 102; }
    return 0;
  }
}
`

func TestSelfHostWorkerUnmapFailureKeepsCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leaks := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			bin := compileCPUProfile(t, cli, workerUnmapFailureProg, target, "", "-sanitize")
			var cmd *exec.Cmd
			if target == "arm64-linux" {
				_, runner := arm64Tooling(t)
				cmd = runArm64Bin(runner, bin)
			} else {
				cmd = runX86_64Bin(cli.runner, bin)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
			}
			if m := leaks.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
				t.Fatalf("failed munmap changed the census: %s", &stderr)
			}
		})
	}
}

// The closure is the last use of the array, so the body frees that box of
// the parent's image and frees more than it allocates.
const workerImageFreeProg = `import "std/worker";

function sum(xs: i64[]): i64 {
  let t: i64 = 0;
  for x in xs { t += x; }
  return t;
}

function main(): i32 {
  let xs: i64[] = [];
  for i in 0..100 { xs = xs.append(i as i64); }
  let w = worker.spawn((): i64 => sum(xs));
  if (w.join() != 4950) { return 1; }
  return 0;
}
`

// A child that frees more than it allocates still balances the census.
func TestSelfHostWorkerImageFreeKeepsCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leaks := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			bin := compileCPUProfile(t, cli, workerImageFreeProg, target, "", "-sanitize")
			var cmd *exec.Cmd
			if target == "arm64-linux" {
				_, runner := arm64Tooling(t)
				cmd = runArm64Bin(runner, bin)
			} else {
				cmd = runX86_64Bin(cli.runner, bin)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
			}
			if m := leaks.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
				t.Fatalf("unbalanced census: %s", &stderr)
			}
		})
	}
}

// The worker spins without end. The parent prints the pid of the worker and
// then ends, by exit or by a bounds abort, while the worker runs.
const workerOrphanProg = `import "std/worker";

function spin(): i32 {
  let n: i64 = 0;
  while (true) { n += 1; }
  return 0;
}

function main(): i32 {
  let w = worker.spawn((): i32 => spin());
  print(w.pid);
  if (args().len() > 1) {
    let none: i32[] = [];
    print(none[3]);
  }
  exit(0);
  return w.join();
}
`

// No worker runs detached: a process worker ends when its parent ends.
func TestSelfHostWorkerEndsWithItsParent(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		bin := compileCPUProfile(t, cli, workerOrphanProg, target, "")
		for _, how := range []string{"exit", "abort"} {
			t.Run(target+"/"+how, func(t *testing.T) {
				var args []string
				if how == "abort" {
					args = []string{"abort"}
				}
				var cmd *exec.Cmd
				if target == "arm64-linux" {
					_, runner := arm64Tooling(t)
					cmd = runArm64Bin(runner, bin, args...)
				} else {
					cmd = runX86_64Bin(cli.runner, bin, args...)
				}
				out, _ := cmd.Output()
				first, _, _ := strings.Cut(string(out), "\n")
				pid, err := strconv.Atoi(strings.TrimSpace(first))
				if err != nil || pid <= 0 {
					t.Fatalf("no worker pid in %q", out)
				}
				defer func() {
					if p, err := os.FindProcess(pid); err == nil {
						_ = p.Kill()
					}
				}()
				deadline := time.Now().Add(5 * time.Second)
				for {
					stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
					if err != nil {
						return
					}
					after := string(stat[bytes.LastIndexByte(stat, ')')+1:])
					if state := strings.Fields(after); len(state) > 0 && (state[0] == "Z" || state[0] == "X") {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("worker %d still runs after its parent ended: %s", pid, after)
					}
					time.Sleep(50 * time.Millisecond)
				}
			})
		}
	}
}
