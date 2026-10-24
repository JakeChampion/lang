package e2eharness

import "fmt"

func FetchTaskProgram(port int) string {
	return fmt.Sprintf(`import "std/async";
import "std/fetch";
import "std/http";
import "std/i32";

function base(): string {
  return "http://127.0.0.1:%d";
}

function status(answer: Result[HttpResponse, fetch.FetchError]): i32 {
  match (answer) {
    Ok(resp) => { return resp.status; },
    Err(e) => { return 0 - 1; }
  }
  return 0 - 1;
}

function word(b: boolean): string {
  if (b) {
    return "yes";
  }
  return "no";
}

function slow(): i32 {
  return status(fetch.send(fetch.get(base() + "/slow")));
}

function main(): i32 {
  let t: async.Task[i32] = async.task_new(() => slow());
  let st: async.TaskStatus[i32] = async.task_start(t);
  let parks: i32 = 0;
  let plain_first: boolean = false;
  let out: i32 = 0 - 1;
  let going: boolean = true;
  while (going) {
    match (st) {
      Done(v) => { out = v; going = false; },
      Suspended(w) => {
        parks = parks + 1;
        if (parks == 1) {
          plain_first = status(fetch.send(fetch.get(base() + "/plain"))) == 200;
        }
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.task_resume(t, woke);
      },
      Cancelled => { out = 0 - 2; going = false; }
    }
  }
  async.task_free(t);
  print("slow " + out.to_string() + " parked " + word(parks > 0) + " plain first " + word(plain_first));
  return 0;
}
`, port)
}

const FetchTaskWant = "slow 200 parked yes plain first yes\n"
