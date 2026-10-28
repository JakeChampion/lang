package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const suspendFoldProgram = `import "std/time";
import "std/async";

function wait(): i32 {
  let none: i32[] = [];
  let woke: i32 = async.wait_any(none, Some(time.duration_millis(2 as i64)));
  return 0;
}

@noinline function scan(xs: i32[]): i32 {
  let total: i32 = 0;
  for x in xs {
    if (x % 3 == 0) {
      total = total + x;
    } else if (x % 5 == 0) {
      if (x > 50) {
        break;
      }
      total = total - 1;
    } else {
      continue;
    }
  }
  let w: i32 = wait();
  for x in xs {
    if (x > 40) {
      total = total + 100;
    }
  }
  return total + w + wait();
}

function drive(t: async.Task[i32]): i32 {
  let st: async.TaskStatus[i32] = async.task_start(t);
  let parks: i32 = 0;
  while (true) {
    match (st) {
      Done(v) => {
        print("parks " + parks.to_string());
        return v;
      },
      Suspended(w) => {
        parks = parks + 1;
        st = async.task_resume(t, async.wait_any(w.set, w.timeout));
      },
      Cancelled => {
        return 0 - 2;
      }
    }
  }
  return 0;
}

function main(): i32 {
  let xs: i32[] = [3, 7, 10, 9, 55, 12];
  let t: async.Task[i32] = async.task_new(() => scan(xs));
  let a: i32 = drive(t);
  print("task " + a.to_string());
  async.task_free(t);
  let b: i32 = scan(xs);
  print("plain " + b.to_string());
  if (a != 111 || b != 111) {
    return 1;
  }
  return 0;
}
`

// A loop or an if with no suspension site in it is skipped whole on a rewind,
// so the transform guards it once with the code around it and adds no guard
// inside it. The old transform added 31 ifs to scan, 2 of them for each
// construct; the fold adds 12.
func TestSelfHostSuspendSiteFreeConstructs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(suspendFoldProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	asm := filepath.Join(t.TempDir(), "out.s")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", asm, src, cli.stdlib)
	cmd.Env = append(os.Environ(), "FERN_SUSPEND_DUMP=scan")
	msg, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("self-host CLI: %v\n%s", err, msg)
	}
	// The mode check is a load of the task state, not a call.
	if text, err := os.ReadFile(asm); err != nil || strings.Contains(string(text), "call __fn___fern_task_mode") {
		t.Errorf("a suspendable function calls __fern_task_mode (read: %v)", err)
	}
	before, after := suspendIfs(string(msg), "before"), suspendIfs(string(msg), "after")
	if before == 0 || after-before > 14 {
		t.Errorf("the transform took scan from %d ifs to %d; at most 14 more are guards it needs", before, after)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	run := exec.Command(bin)
	if len(cli.runner) > 0 {
		run = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
	}
	out, err := run.CombinedOutput()
	if want := "parks 2\ntask 111\nplain 111\n"; err != nil || string(out) != want {
		t.Errorf("%v\n--- got ---\n%s--- want ---\n%s", err, out, want)
	}
	if stderr, exit := cli.exitOf(t, suspendFoldProgram, "arm64-linux"); exit != 0 {
		t.Errorf("arm64-linux: exit %d\n%s", exit, stderr)
	}
	component := cli.wasmComponent(t, src)
	if out, err := exec.Command(e2eharness.Wasmtime(t), "run", "-S", "inherit-network", component).CombinedOutput(); err != nil {
		t.Errorf("wasm32-wasi: %v\n%s", err, out)
	}
}

// suspendIfs counts the if ops in one stage of a FERN_SUSPEND_DUMP listing.
func suspendIfs(dump, stage string) int {
	n, in := 0, false
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "== ") {
			in = strings.Contains(line, " "+stage+" ")
			continue
		}
		if f := strings.Fields(line); in && len(f) >= 2 && f[1] == "if" {
			n++
		}
	}
	return n
}
