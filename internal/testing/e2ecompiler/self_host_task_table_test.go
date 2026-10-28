package e2ecompiler

import (
	"strings"
	"testing"
)

// A task id comes off a free list, so a new task costs the same with 40,000
// tasks alive as with none. The program exits with the ratio of the two
// costs, capped at 100.
const taskTableProgram = `import "std/async";

function work(n: i32): i32 {
  return n;
}

function churn(rounds: i32): i64 {
  let start: i64 = monotonic_ns();
  for i in 0..rounds {
    let t: async.Task[i32] = async.task_new(() => work(i));
    async.task_free(t);
  }
  return monotonic_ns() - start;
}

function main(): i32 {
  let idle: i64 = churn(20000);
  let live: async.Task[i32][] = [];
  for i in 0..40000 {
    live = live.append(async.task_new(() => work(i)));
  }
  let busy: i64 = churn(20000);
  for t in live {
    async.task_free(t);
  }
  let ratio: i64 = busy / (idle + 1);
  eprint("idle " + idle.to_string() + " ns, busy " + busy.to_string() + " ns");
  if (ratio > 100) {
    return 100;
  }
  return ratio as i32;
}
`

func TestSelfHostTaskTableConstant(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, ratio := cli.exitOf(t, taskTableProgram, target)
			t.Log(strings.TrimSpace(stderr))
			if ratio > 8 {
				t.Errorf("a new task costs %dx more with 40,000 tasks alive than with none", ratio)
			}
		})
	}
}
