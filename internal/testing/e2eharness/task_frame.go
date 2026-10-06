package e2eharness

// TaskFrameProgram pins what a parked frame keeps (docs/NET-P3-SUSPENSION-PLAN.md
// §3.5): a function that parks holds `str` views of a parameter's string and
// of a local string, a byte view, and a closure sharing a mutated scalar
// with its frame, and reads every one of them after the park. The save area
// keeps the frame's references, so the owners of the views survive the park,
// and a mutated capture is a heap cell both sides point at, so the closure
// and the frame still share it afterwards. `rounds` parks in a loop and
// reads locals after it across the back edge, in one arm of an if and after
// a break, while an array it built first is dead by then, so a park saves
// only the live ones. The task's figure must equal the plain run's.
const TaskFrameProgram = `import "std/async";
import "std/i32";
import "std/string";

// A str view of a parameter's string, a str view of a local string and a
// byte view, all read after a park.
function hold_views(s: string): i32 {
  let v: str = s.trim();
  let own: string = s + "!!";
  let w: str = own.trim();
  let b: [u8] = s.as_bytes();
  let set: i32[] = [timer_fd(10), 1];
  let r: i32 = async.wait_any(set, 0 - 1);
  return v.len() * 100 + w.len() * 10 + b.len() + r;
}

// A scalar capture mutated on both sides of a park.
function counter(): i32 {
  let n: i32 = 0;
  let bump: () => i32 = (): i32 => { n = n + 1; return n; };
  let set: i32[] = [timer_fd(10), 1];
  let r: i32 = async.wait_any(set, 0 - 1);
  bump();
  bump();
  n = n + 10;
  return bump() * 10 + r;
}

// Locals read after a park on some paths only, and a dead array. Kept a
// call so the suspend pass's dump names it.
@noinline function rounds(k: i32): i32 {
  let total: i32 = 0;
  let far: i32 = k * 7;
  let near: i32 = k + 1;
  let gone: i32[] = [k, k, k];
  total = total + gone.len();
  let i: i32 = 0;
  while (i < 3) {
    let set: i32[] = [timer_fd(5), 1];
    let r: i32 = async.wait_any(set, 0 - 1);
    if (i == 1) {
      total = total + near + r;
    } else {
      total = total + i;
    }
    if (i == 2) {
      break;
    }
    i = i + 1;
  }
  return total * 100 + far;
}

function body(): i32 {
  return hold_views("hello") + counter() * 1000 + rounds(3) * 1000000;
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
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.task_resume(t, woke);
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

// TaskFrameWant is the output under the self-host compiler: the task parks
// once in each of the first two functions and three times in `rounds`, and
// answers the plain run's figure.
const TaskFrameWant = "parks 5\ntask 921130575\nplain 921130575\n"
