package e2eharness

// TaskCombinatorsProgram is slice 6's gate (docs/NET-P3-SUSPENSION-PLAN.md
// §4): the task combinators inside a task. A task runs a race, a gather and
// a with_deadline over entries that park on timers, and a gather over
// futures whose waits go through RealDriver.poll_ready, driven by hand from
// main; a second task is cancelled from outside while its race is parked.
// Every entry has a defer, which runs whether it finished or was cancelled.
const TaskCombinatorsProgram = `import "std/async";
import "std/i32";
import "std/time";

// nap parks on a timer and answers v; cancelled, it says so and answers -1.
// Its defer runs either way.
function nap(label: string, ms: i32, v: i32): i32 {
  defer print("done " + label);
  let set: i32[] = [timer_fd(ms), 1];
  let r: i32 = async.wait_any(set, 0 - 1);
  if (r == async.cancelled()) {
    print("cancelled " + label);
    return 0 - 1;
  }
  return v;
}

function mk(v: i32): async.Future[i32] {
  return Ready(v);
}

// later is a future that resolves to v once its timer fires.
function later(ms: i32, v: i32): async.Future[i32] {
  return Pending(timer_fd(ms), (woken: i32) => mk(v));
}

function show_opt(o: Option[i32]): string {
  match (o) {
    Some(v) => {
      return v.to_string();
    },
    None => {
      return "none";
    }
  }
}

function body(): i32 {
  let race: (() => i32)[] = [() => nap("slow", 400, 1), () => nap("fast", 20, 2)];
  let (w, v) = async.race_tasks(race, 0 - 1);
  print("race " + w.to_string() + " " + v.to_string());
  let all: (() => i32)[] = [() => nap("a", 60, 1), () => nap("b", 20, 2), () => nap("c", 40, 3)];
  let g: i32[] = async.gather_tasks(all, 0 - 1);
  print("gather " + g[0].to_string() + " " + g[1].to_string() + " " + g[2].to_string());
  let timed: (() => i32)[] = [() => nap("x", 20, 7), () => nap("y", 500, 8)];
  let d: Option[i32][] = async.with_deadline_tasks(time.duration_millis(100), timed);
  print("deadline " + show_opt(d[0]) + " " + show_opt(d[1]));
  let fs: async.Future[i32][] = [later(60, 4), later(20, 5)];
  let f: i32[] = async.gather(fs, 0 - 1);
  print("futures " + f[0].to_string() + " " + f[1].to_string());
  return 0;
}

function outer(): i32 {
  let race: (() => i32)[] = [() => nap("p1", 400, 1), () => nap("p2", 400, 2)];
  let (w, v) = async.race_tasks(race, 0 - 1);
  print("outer " + w.to_string() + " " + v.to_string());
  return w;
}

// drive runs a task by hand: each park's wait is the scheduler's own wait.
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
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.task_resume(t, woke);
      },
      Cancelled => {
        print("parks " + parks.to_string());
        return 0 - 2;
      }
    }
  }
  return 0;
}

function main(): i32 {
  let t: async.Task[i32] = async.task_new(() => body());
  let r: i32 = drive(t);
  async.task_free(t);
  // A task cancelled from outside while its race is parked cancels both
  // of the race's children.
  let o: async.Task[i32] = async.task_new(() => outer());
  let st: async.TaskStatus[i32] = async.task_start(o);
  match (st) {
    Suspended(w) => {
      let c: async.TaskStatus[i32] = async.task_cancel(o);
      print("outer cancelled");
    },
    Done(v) => {
      print("outer done");
    },
    Cancelled => {}
  }
  async.task_free(o);
  return 0;
}
`

// TaskCombinatorsWant is the output under the self-host compiler: the fast
// entry wins the race and the slow one is cancelled through its defer, the
// gather's entries finish in timer order, the deadline cancels the late
// entry, the future gather parks per round (8 parks in all), and the outer
// cancellation reaches both of its race's children.
const TaskCombinatorsWant = "done fast\n" +
	"cancelled slow\n" +
	"done slow\n" +
	"race 1 2\n" +
	"done b\n" +
	"done c\n" +
	"done a\n" +
	"gather 1 2 3\n" +
	"done x\n" +
	"cancelled y\n" +
	"done y\n" +
	"deadline 7 none\n" +
	"futures 4 5\n" +
	"parks 8\n" +
	"cancelled p1\n" +
	"done p1\n" +
	"cancelled p2\n" +
	"done p2\n" +
	"outer -1 -1\n" +
	"outer cancelled\n"
