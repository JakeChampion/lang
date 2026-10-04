package e2eharness

// TaskSchedulerProgram drives a task by hand (docs/NET-P3-SUSPENSION-PLAN.md
// §4, slice 2): a function three calls deep parks twice, inside a loop and a
// branch, with a string held across both parks by the outermost frame. The
// scheduler is the program's own loop: each park hands back its token, a
// poll on it is the wait, and the readiness word 7 is what the park answers.
// It then runs the same functions with no task, where suspend blocks on the
// timer and answers 0.
const TaskSchedulerProgram = `import "std/async";
import "std/i32";

// leaf parks twice: once per call. mid calls it inside a loop and a branch;
// top holds a string across the whole thing.
function leaf(n: i32): i32 {
  let r: i32 = async.suspend(timer_fd(1));
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

function top(n: i32): i32 {
  let s: string = "held" + n.to_string();
  let a: i32 = mid(n);
  return a * 100 + s.len();
}

function main(): i32 {
  // Driven by hand: each park hands back its token; a poll on it is the
  // scheduler's wait, and the readiness word 7 is what suspend answers.
  let t: async.Task[i32] = async.task_new(() => top(5));
  let st: async.TaskStatus[i32] = async.task_start(t);
  let parks: i32 = 0;
  let out: i32 = 0 - 1;
  let going: boolean = true;
  while (going) {
    match (st) {
      Done(v) => { out = v; going = false; },
      Suspended(tok) => {
        parks = parks + 1;
        let toks: i32[] = [tok];
        let w: i32 = poll(toks, 0 - 1);
        st = async.task_resume(t, 7);
      },
      Cancelled => { out = 0 - 2; going = false; }
    }
  }
  async.task_free(t);
  // parks=2, out = ((7+5) + (7+15)) * 100 + len("held5") = 3405
  print("parks " + parks.to_string() + " out " + out.to_string());
  // The same functions with no task: suspend blocks on the timer and answers 0.
  print("plain " + top(5).to_string());
  return 0;
}
`

// TaskSchedulerWant is the output under the self-host compiler, which lowers
// the three functions to their resumable form: two parks, then
// ((7+5) + (7+15)) * 100 + len("held5"), and the plain run's
// ((0+5) + (0+15)) * 100 + 5.
const TaskSchedulerWant = "parks 2 out 3405\nplain 2005\n"

// TaskSchedulerFallbackWant is the output under the blocking fallback (the
// Go compiler, §3.6): no task is ever current, so the entry runs to its end
// inside task_start and both runs answer the plain figure.
const TaskSchedulerFallbackWant = "parks 0 out 2005\nplain 2005\n"
