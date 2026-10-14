package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// suspendShapesProgram parks inside task-run code through two routes: a
// suspendable call whose value is used in place (a condition, an operand, an
// argument), and a lifted closure that parks, called through a value of its
// own type. `apply` calls through a value of another type, which can never
// reach the parking closure.
const suspendShapesProgram = `import "std/async";

function wait(): i32 {
  let set: i32[] = [timer_fd(5), 1];
  return async.wait_any(set, 0 - 1);
}

function ready(): boolean {
  return wait() >= 0;
}

function twice(n: i32): i32 {
  return n * 2;
}

@noinline function apply(f: (i32) => i32, n: i32): i32 {
  return f(n);
}

@noinline function through(p: () => i32): i32 {
  return p() + 1;
}

function body(): i32 {
  let total: i32 = 0;
  if (ready()) {
    total = total + 1;
  }
  total = total + 10 * (1 + wait());
  total = total + twice(wait() + 50);
  total = total + apply((x: i32): i32 => x + 1, 1000);
  total = total + through((): i32 => { return wait() + 2000; });
  return total;
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
        st = async.task_resume(t, async.wait_any(w.set, w.timeout_ms));
      },
      Cancelled => {
        return 0 - 2;
      }
    }
  }
  return 0;
}

function main(): i32 {
  let t: async.Task[i32] = async.task_new(() => body());
  print("task " + drive(t).to_string());
  async.task_free(t);
  print("plain " + body().to_string());
  return 0;
}
`

// TestSelfHostSuspendShapes: a task parking at a call whose value is used in
// place answers what the plain run answers, and only a function calling
// through a value of a parking closure's type is rewritten for suspension.
func TestSelfHostSuspendShapes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(suspendShapesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fn        string
		rewritten bool
	}{
		{"body", true},
		{"through", true},
		{"apply", false},
	} {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out.s"), src, cli.stdlib)
		cmd.Env = append(os.Environ(), "FERN_SUSPEND_DUMP="+c.fn)
		msg, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("self-host CLI: %v\n%s", err, msg)
		}
		if got := strings.Contains(string(msg), "== "+c.fn+" before"); got != c.rewritten {
			t.Errorf("%s rewritten for suspension = %v, want %v", c.fn, got, c.rewritten)
		}
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	cmd := exec.Command(bin)
	if len(cli.runner) > 0 {
		cmd = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
	}
	out, err := cmd.CombinedOutput()
	if want := "parks 4\ntask 3113\nplain 3113\n"; err != nil || string(out) != want {
		t.Errorf("%v\n--- got ---\n%s--- want ---\n%s", err, out, want)
	}
}
