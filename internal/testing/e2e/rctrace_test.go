package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var rcTraceLineRe = regexp.MustCompile(`^rctrace ([afu]) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16}) ([0-9a-f]{16})$`)

func buildTracedX86_64(t *testing.T, entry, dir string) (string, error) {
	bin := filepath.Join(dir, "prog")
	cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetX86_64Linux, entry, bin)
	cmd.Env = e2eharness.ChildEnv("FERN_RC_TRACE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("self-host compile: %v\n%s", err, out)
	}
	return bin, nil
}

func runTracedX86_64(t *testing.T, src string) (string, string, int) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	bin, err := buildTracedX86_64(t, entry, dir)
	if err != nil {
		t.Fatal(err)
	}
	return runSplit(t, runX86_64Bin(runner, bin))
}

func unpairedAllocs(t *testing.T, src string) int {
	t.Helper()
	_, stderr, exit := runTracedX86_64(t, src)
	if exit == -1 {
		t.Fatal("the program was killed by a signal")
	}
	n, _, err := pairRcTrace(stderr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRcTraceX86_64LeavesStdoutAndExit(t *testing.T) {
	const src = `function main(): i32 {
  unsafe {
      let i: i32 = 0;
      while (i < 40) {
          let a: usize = __alloc(64);
          __free(a, 64);
          i = i + 1;
      }
      print("done");
      return 7;
  }
}`
	stdout, stderr, code := runTracedX86_64(t, src)
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if stdout != "done\n" {
		t.Errorf("stdout = %q, want %q", stdout, "done\n")
	}
	n, _, err := pairRcTrace(stderr)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d unpaired allocation(s) in a program that frees every block", n)
	}
	if first, _, _ := strings.Cut(stderr, "\n"); !rcTraceLineRe.MatchString(first) {
		t.Errorf("stderr does not open with an rctrace line: %q", first)
	}
}

func TestPairRcTraceRegionRemoval(t *testing.T) {
	line := func(kind string, ptr, size uint64) string {
		return fmt.Sprintf("rctrace %s %016x %016x %016x %016x\n", kind, ptr, size, 1, 2)
	}
	cases := []struct {
		name  string
		trace string
		live  int
		bad   bool
	}{
		{"whole region", line("a", 0x1040, 32) + line("a", 0x1080, 64) + line("u", 0x1000, 0x100), 0, false},
		{"outside stays live", line("a", 0x1040, 32) + line("a", 0x2000, 32) + line("u", 0x1000, 0x100), 1, false},
		{"exclusive end", line("a", 0x1100, 32) + line("u", 0x1000, 0x100), 1, false},
		{"before region", line("a", 0xff0, 8) + line("u", 0x1000, 0x100), 1, false},
		{"partial allocation", line("a", 0x10f0, 32) + line("u", 0x1000, 0x100), 1, false},
		{"later reuse", line("a", 0x1040, 32) + line("u", 0x1000, 0x100) + line("a", 0x1040, 32), 1, false},
		{"no removal", line("a", 0x1040, 32), 1, false},
		{"ordinary free", line("a", 0x1040, 32) + line("f", 0x1040, 32), 0, false},
		{"zero length", line("u", 0x1000, 0), 0, true},
		{"overflow", line("u", ^uint64(0)-15, 32), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := pairRcTrace(tc.trace)
			if (err != nil) != tc.bad || got != tc.live {
				t.Fatalf("live=%d, error=%v; want live=%d, invalid=%v", got, err, tc.live, tc.bad)
			}
		})
	}
}

func TestRcTraceWorkerRegionReclamation(t *testing.T) {
	const source = `import "std/worker";
function main(): i32 {
  for i in 0..4 {
    let w = worker.spawn((): string[] => ["worker-" + i.to_string(), "tail"]);
    let result: string[] = w.join();
    if (result[0] != "worker-" + i.to_string()) { return 9; }
  }
  return 0;
}`
	_, stderr, exit := runTracedX86_64(t, source)
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if got := strings.Count(stderr, "rctrace u "); got != 4 {
		t.Fatalf("region removals=%d, want 4: %s", got, stderr)
	}
	if live, top, err := pairRcTrace(stderr); err != nil || live != 0 {
		t.Fatalf("live=%d, sites=%s, error=%v", live, top, err)
	}
}
