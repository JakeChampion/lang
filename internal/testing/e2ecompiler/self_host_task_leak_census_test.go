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

// callSchedulerProgram is the scheduler program as a call of two parts
// (async.call_start): the same two parks driven by hand through call_of and
// call_resume, a second call cancelled at its first park, and a call that
// returns at once, which builds no closure and no record.
const callSchedulerProgram = `import "std/async";
import "std/i32";

// leaf parks once per call; mid calls it twice; top holds a string across
// both, and takes a second argument so it runs as a call of two parts.
function leaf(n: i32): i32 {
  let set: i32[] = [timer_fd(1), 1];
  let r: i32 = async.wait_any(set, 0 - 1);
  return r + n;
}

function mid(n: i32): i32 {
  let acc: i32 = 0;
  let i: i32 = 0;
  while (i < 2) {
    if (i == 1) {
      acc = acc + leaf(n + 10);
    } else {
      acc = acc + leaf(n);
    }
    i = i + 1;
  }
  return acc;
}

function top(n: i32, k: i32): i32 {
  let s: string = "held" + n.to_string();
  let a: i32 = mid(n);
  return a * k + s.len();
}

function add(a: i32, b: i32): i32 {
  return a + b;
}

function main(): i32 {
  // Driven by hand as the serve loop drives a handler: the first park hands
  // back the wait that names the record, and every resume goes through the
  // same trampoline the start used.
  let st: async.TaskStatus[i32] = async.call_start(top, 5, 100);
  let c: async.Call[i32, i32, i32] = async.Call { id: 0, f: top, a: 5, b: 100 };
  let parks: i32 = 0;
  let out: i32 = 0 - 1;
  let going: boolean = true;
  while (going) {
    match (st) {
      Done(v) => { out = v; going = false; },
      Suspended(w) => {
        if (parks == 0) {
          c = async.call_of(w, top, 5, 100);
        }
        parks = parks + 1;
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.call_resume(c, 7);
      },
      Cancelled => { out = 0 - 2; going = false; }
    }
  }
  if (parks > 0) {
    async.call_free(c);
  }
  // parks=2, out = ((7+5) + (7+15)) * 100 + len("held5") = 3405
  print("parks " + parks.to_string() + " out " + out.to_string());
  // A call cancelled at its first park runs on to its end and is Cancelled.
  match (async.call_start(top, 5, 100)) {
    Suspended(w) => {
      let c2: async.Call[i32, i32, i32] = async.call_of(w, top, 5, 100);
      match (async.call_cancel(c2)) {
        Cancelled => { print("cancelled"); },
        _ => { print("not cancelled"); }
      }
      async.call_free(c2);
    },
    _ => { print("no park"); }
  }
  // A call that returns at once has nothing to hold and nothing to free.
  match (async.call_start(add, 2, 3)) {
    Done(v) => { print("done " + v.to_string()); },
    _ => { print("parked"); }
  }
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
		{"parks-as-a-call", callSchedulerProgram, "parks 2 out 3405\ncancelled\ndone 5\n"},
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
