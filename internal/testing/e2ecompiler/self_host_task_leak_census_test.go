package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// taskPoolProgram holds 100 tasks live at once, past the task table's first
// 64 slots, frees them all, and runs one more on a reused record.
const taskPoolProgram = `import "std/async";

function work(n: i32): i32 {
  return n * 2;
}

function main(): i32 {
  let ts: async.Task[i32][] = [];
  let i: i32 = 0;
  while (i < 100) {
    let k: i32 = i;
    ts = ts.append(async.task_new(() => work(k)));
    i = i + 1;
  }
  let sum: i32 = 0;
  for t in ts {
    match (async.task_start(t)) {
      Done(v) => { sum = sum + v; },
      Suspended(w) => {},
      Cancelled => {}
    }
  }
  for t in ts {
    async.task_free(t);
  }
  let again: async.Task[i32] = async.task_new(() => work(21));
  match (async.task_start(again)) {
    Done(v) => { sum = sum + v; },
    Suspended(w) => {},
    Cancelled => {}
  }
  async.task_free(again);
  print(sum.to_string());
  return 0;
}
`

// TestSelfHostTaskProgramsBalanceTheLeakCensus: a program that runs tasks
// ends with its leak census balanced on both native targets. The task
// runtime's own blocks are runtime state the census leaves out (#11386), so
// what this pins is that nothing else a task touches leaks. The scheduler
// program parks, so its record takes a wait set and a save area that outgrows
// its first 64 words; the pool program outgrows the first record table.
func TestSelfHostTaskProgramsBalanceTheLeakCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	programs := []struct{ name, src, want string }{
		{"parks", e2eharness.TaskSchedulerProgram, e2eharness.TaskSchedulerWant},
		{"outgrows-the-table", taskPoolProgram, "9942\n"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, p := range programs {
			t.Run(target+"/"+p.name, func(t *testing.T) {
				src := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
					t.Fatal(err)
				}
				env := []string{"FERN_STRICT_IR=1", "FERN_LEAKCHECK=1"}
				var cmd *exec.Cmd
				if target == "arm64-linux" {
					_, qemu := arm64Tooling(t)
					cmd = runArm64Bin(qemu, cli.arm64Binary(t, src, env...))
				} else {
					cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src, env...))
				}
				var out, errb bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errb
				if err := cmd.Run(); err != nil {
					t.Fatalf("run: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
				}
				if out.String() != p.want {
					t.Fatalf("stdout %q, want %q", out.String(), p.want)
				}
				if allocs, frees, live := leakSummaryOf(t, p.name, errb.String()); allocs != frees || live != 0 {
					t.Errorf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
				}
			})
		}
	}
}

// TestSelfHostTaskRuntimeKeepsOnlyItsPoolX86_64: the blocks a task program
// ends with unfreed are exactly the task runtime's pool, which the leak census
// leaves out: the record table, a record per task live at once, and the save
// area and wait set of each record that parked. A block that a growth of the
// table, a save area or a wait set replaces is freed (#11391); the heap
// trace sees it where the census cannot. The parked task's save area outgrows
// its first 64 words, and the 100 live tasks outgrow the first table.
func TestSelfHostTaskRuntimeKeepsOnlyItsPoolX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, p := range []struct {
		name, src string
		pool      int
	}{
		{"parks", e2eharness.TaskSchedulerProgram, 4},
		{"outgrows-the-table", taskPoolProgram, 101},
	} {
		t.Run(p.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1", "FERN_RC_TRACE=1"))
			var errb bytes.Buffer
			cmd.Stderr = &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\nstderr:\n%s", err, errb.String())
			}
			evs, _ := parseHev(t, errb.String())
			live := map[uint64]uint64{}
			for _, e := range evs {
				if e.kind == "a" {
					live[e.ptr] = e.size
				} else {
					delete(live, e.ptr)
				}
			}
			if len(live) != p.pool {
				t.Errorf("%d blocks never freed, want the pool's %d: %v", len(live), p.pool, live)
			}
		})
	}
}
