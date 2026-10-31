package e2eharness

// TaskDeadlineProgram parks two tasks through __task_park_until and reads each
// deadline back through __task_until, as a scheduler does. The first deadline,
// 2^62 ns, is far beyond what 32-bit milliseconds hold. The second is -1: no
// deadline.
const TaskDeadlineProgram = `import "std/async";

const FAR: i64 = 4611686018427387904i64;

function far(): i32 {
  unsafe {
    return __task_park_until([5, 1], FAR);
  }
}

function forever(): i32 {
  unsafe {
    return __task_park_until([6, 1], 0 - 1 as i64);
  }
}

function report(name: string, entry: () => i32): void {
  let t: async.Task[i32] = async.task_new(entry);
  match (async.task_start(t)) {
    Suspended(w) => {
      unsafe {
        print(name + " until " + __task_until(t.id).to_string() + " set " + w.set.len().to_string());
      }
      match (async.task_resume(t, 0)) {
        Done(v) => {
          print(name + " woke " + v.to_string());
        },
        _ => {
          print(name + " parked again");
        }
      }
    },
    Done(v) => {
      print(name + " ran " + v.to_string());
    },
    Cancelled => {}
  }
  async.task_free(t);
}

function main(): i32 {
  report("far", far);
  report("forever", forever);
  return 0;
}
`

// TaskDeadlineWant is the compiled output: each task parks and keeps its deadline.
const TaskDeadlineWant = "far until 4611686018427387904 set 2\nfar woke 0\n" +
	"forever until -1 set 2\nforever woke 0\n"

// TaskDeadlineInterpWant is the interpreters' output: no task parks there, and
// the park answers -1 at once.
const TaskDeadlineInterpWant = "far ran -1\nforever ran -1\n"
