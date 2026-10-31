package e2eharness

const TaskCombinatorsProgram = `import "std/async";
import "std/clock";
import "std/i32";
import "std/time";

// nap parks on a timer and answers v; cancelled, it says so and answers -1.
// Its defer runs either way.
function nap(label: string, ms: i32, v: i32): i32 {
  defer print("done " + label);
  let set: i32[] = [timer_fd(ms), 1];
  if let async.Wake.Cancelled = async.wait_any(set, None) {
    print("cancelled " + label);
    return 0 - 1;
  }
  return v;
}

function napping(label: string, ms: i32, v: i32): async.Future[i32] {
  return async.spawn((): i32 => nap(label, ms, v));
}

function mk(v: i32): async.Future[i32] {
  return Ready(v);
}

// later is a future that resolves to v once its timer fires.
function later(ms: i32, v: i32): async.Future[i32] {
  let set: i32[] = [timer_fd(ms), 1];
  return Pending(async.Wait { set: set, until: None }, (wake: async.Wake): async.Future[i32] => mk(v));
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
  match (async.wait(async.race([napping("slow", 400, 1), napping("fast", 20, 2)]))) {
    Some(won) => {
      print("race " + won.0.to_string() + " " + won.1.to_string());
    },
    None => {
      print("race none");
    }
  }
  match (async.wait(async.gather([napping("a", 60, 1), napping("b", 20, 2), napping("c", 40, 3)]))) {
    Some(g) => {
      print("gather " + g[0].to_string() + " " + g[1].to_string() + " " + g[2].to_string());
    },
    None => {
      print("gather none");
    }
  }
  let until: Instant = clock.instant().add(time.duration_millis(100));
  match (async.wait(async.with_deadline(until, [napping("x", 20, 7), napping("y", 500, 8)]))) {
    Some(d) => {
      print("deadline " + show_opt(d[0]) + " " + show_opt(d[1]));
    },
    None => {
      print("deadline none");
    }
  }
  match (async.wait(async.gather([later(60, 4), later(20, 5)]))) {
    Some(f) => {
      print("futures " + f[0].to_string() + " " + f[1].to_string());
    },
    None => {
      print("futures none");
    }
  }
  return 0;
}

function outer(): i32 {
  match (async.wait(async.race([napping("p1", 400, 1), napping("p2", 400, 2)]))) {
    Some(won) => {
      print("outer " + won.0.to_string() + " " + won.1.to_string());
      return won.0;
    },
    None => {
      print("outer none");
    }
  }
  return 0 - 1;
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
        st = async.task_resume(t, async.wait_any(w.set, w.until));
      }
    }
  }
  return 0;
}

function main(): i32 {
  let r: i32 = drive(async.task_new(() => body()));
  // A task cancelled from outside while its race is parked cancels both
  // of the race's children.
  let o: async.Task[i32] = async.task_new(() => outer());
  match (async.task_start(o)) {
    Suspended(w) => {
      let c: async.TaskStatus[i32] = async.task_resume(o, async.Wake.Cancelled);
      print("outer cancelled");
    },
    Done(v) => {
      print("outer done");
    }
  }
  return 0;
}
`

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
	"outer none\n" +
	"outer cancelled\n"
