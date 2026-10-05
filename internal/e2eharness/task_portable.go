package e2eharness

// TaskPortableProgram is the task runtime's gate on every target
// (docs/NET-P3-SUSPENSION-PLAN.md §3.7): the parks nap through `wait_any`
// on an empty set under a bound, which no target needs a descriptor for,
// and the chain holds an i64, an f64 and a string across them, so a save
// area that narrowed a word would answer wrong. main drives the task by
// hand, waiting on each park's bound itself and resuming with what its own
// wait answered (-1, the timeout), then runs the same functions with no
// task.
const TaskPortableProgram = `import "std/async";
import "std/i32";

// leaf parks on a bound alone: the park answers -1, as the plain wait does
// once the bound passes.
function leaf(n: i32): i32 {
  let none: i32[] = [];
  let r: i32 = async.wait_any(none, 1);
  return r + n;
}

function mid(n: i32): i64 {
  let big: i64 = 5000000000i64 + (n as i64);
  let acc: i32 = 0;
  let i: i32 = 0;
  while (i < 2) {
    acc = acc + leaf(n + i);
    i = i + 1;
  }
  return big + (acc as i64);
}

function top(n: i32): i32 {
  let s: string = "held" + n.to_string();
  let h: f64 = 2.5;
  let a: i64 = mid(n);
  return ((a - 5000000000i64) as i32) * 100 + s.len() + ((h * 2.0) as i32);
}

function main(): i32 {
  let t: async.Task[i32] = async.task_new(() => top(5));
  let st: async.TaskStatus[i32] = async.task_start(t);
  let parks: i32 = 0;
  let out: i32 = 0 - 1;
  let going: boolean = true;
  while (going) {
    match (st) {
      Done(v) => { out = v; going = false; },
      Suspended(w) => {
        parks = parks + 1;
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.task_resume(t, woke);
      },
      Cancelled => { out = 0 - 2; going = false; }
    }
  }
  async.task_free(t);
  // parks=2; leaf answers n-1 twice and big carries n: (5 + 4 + 5) * 100 +
  // len("held5") + 5 = 1410
  print("parks " + parks.to_string() + " out " + out.to_string());
  print("plain " + top(5).to_string());
  return 0;
}
`

// TaskPortableWant is the output under the self-host compiler on x86-64,
// arm64 and wasm: two parks, the same figure as the plain run.
const TaskPortableWant = "parks 2 out 1410\nplain 1410\n"
