package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
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
