package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A function returning a variant whose payloads fit a word, and whose every
// caller takes the variant apart, returns its tag and the payload beside it
// (sempair). The program covers a declared enum, Option and Result; returns
// of constructions, of a parameter, of another paired call and of a recursive
// one; and functions that stay boxed: one a caller keeps the variant of, one
// used as a value and one with an i64 payload.
//
// The exit code: 1 for a wrong value, 2 for an allocation in the loop, which
// only matches on what the paired functions return, 99 for a count that went
// under.
const pairReturnProg = `import "std/bench";

enum Three { A(string), B, C(i32) }
struct Box2 { n: i32, s: string }

@noinline function pick(n: i32, s: string): Three {
  if (n % 3 == 0) {
    return A(s);
  }
  if (n % 3 == 1) {
    return B;
  }
  return C(n * 2);
}

@noinline function kept_pick(n: i32, s: string): Three {
  if (n % 3 == 0) {
    return A(s);
  }
  if (n % 3 == 1) {
    return B;
  }
  return C(n * 2);
}

@noinline function through(t: Three): Three {
  return t;
}

@noinline function opt(n: i32, s: string): Option[string] {
  if (n % 2 == 0) {
    return Some(s);
  }
  return None;
}

@noinline function res(n: i32, b: Box2): Result[Box2, i32] {
  if (n % 2 == 0) {
    return Ok(b);
  }
  return Err(n);
}

@noinline function fwd(n: i32, b: Box2): Result[Box2, i32] {
  return res(n + 1, b);
}

// Called once and not @noinline, so it is spliced into main and its body is
// left dead: the return that hands res's variant on whole must not keep res
// boxed.
function fwd_once(n: i32, b: Box2): Result[Box2, i32] {
  return res(n + 2, b);
}

function depth(n: i32): Option[i32] {
  if (n == 0) {
    return Some(0);
  }
  match (depth(n - 1)) {
    Some(d) => {
      return Some(d + 1);
    },
    None => {
      return None;
    }
  }
}

@noinline function valued(n: i32): Option[i32] {
  if (n > 0) {
    return Some(n);
  }
  return None;
}

@noinline function wide(n: i32): Option[i64] {
  if (n > 0) {
    return Some(n as i64 * 4000000000 as i64);
  }
  return None;
}

function score(t: Three): i32 {
  match (t) {
    A(x) => {
      return x.len();
    },
    B => {
      return 100;
    },
    C(k) => {
      return k;
    }
  }
}

function main(): i32 {
  let s: string = "hey" + "".to_string();
  let b: Box2 = Box2 { n: 7, s: s };
  let total: i32 = 0;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 12) {
    match (pick(i, s)) {
      A(x) => {
        total = total + x.len();
      },
      B => {
        total = total + 100;
      },
      C(k) => {
        total = total + k;
      }
    }
    match (opt(i, s)) {
      Some(x) => {
        total = total + x.len();
      },
      None => {
        total = total + 1000;
      }
    }
    match (fwd(i, b)) {
      Ok(x) => {
        total = total + x.n + x.s.len();
      },
      Err(e) => {
        total = total + e * 10000;
      }
    }
    match (fwd_once(i, b)) {
      Ok(x) => {
        total = total + x.n;
      },
      Err(e) => {
        total = total + e * 100;
      }
    }
    match (depth(i)) {
      Some(d) => {
        total = total + d;
      },
      None => {}
    }
    i = i + 1;
  }
  let a1: i64 = bench.alloc_count();
  match (through(kept_pick(5, s))) {
    C(k) => {
      total = total + k;
    },
    _ => {}
  }
  let kept: Three[] = [];
  i = 0;
  while (i < 4) {
    kept = kept.append(kept_pick(i, s));
    i = i + 1;
  }
  let f: (i32) => Option[i32] = valued;
  match (f(5)) {
    Some(v) => {
      total = total + v;
    },
    None => {}
  }
  match (wide(3)) {
    Some(w) => {
      total = total + (w / 1000000000 as i64) as i32;
    },
    None => {}
  }
  for t in kept {
    total = total + score(t);
  }
  print(total.to_string());
  if (total != 371587) {
    return 1;
  }
  if (a1 != a0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

// The pair half of the same pass: a function returning a tuple of two values
// that each fit a word returns the first as its result and the second beside
// it. The program covers a pair built at the return, one
// returned from another paired call, one whose return joins two
// constructions (so the callee projects it), a scalar first with a reference
// second, a caller reading only the second element; and the ones that stay
// boxed: a pair a caller keeps whole, a triple, and an element wider than a
// word. The total is the interpreter's.
const pairTupleProg = `import "std/bench";

struct Box2 { n: i32, s: string }

// A pair built at the return.
@noinline function pair(n: i32, b: Box2): (Box2, boolean) {
  return (b, n % 2 == 0);
}

// A pair returned from another paired call.
@noinline function pair_fwd(n: i32, b: Box2): (Box2, boolean) {
  return pair(n + 1, b);
}

// A pair whose return is a join of two constructions, so the callee
// projects it rather than reading a construction.
@noinline function picked(n: i32, b: Box2): (Box2, boolean) {
  let p: (Box2, boolean) = (b, false);
  if (n % 2 == 0) {
    p = (b, true);
  }
  return p;
}

// A scalar first and a reference second.
@noinline function scalar_first(n: i32, s: string): (i32, string) {
  if (n % 2 == 0) {
    return (n, s);
  }
  return (0 - n, s);
}

// Only the second element is read.
@noinline function second_only(n: i32, s: string): (string, i32) {
  return (s, n * 3);
}

// Kept whole by a caller, so it stays boxed.
@noinline function kept_pair(n: i32, s: string): (i32, string) {
  return (n, s);
}

// Three elements stay boxed.
@noinline function triple(n: i32): (i32, i32, i32) {
  return (n, n + 1, n + 2);
}

// A second element wider than a word stays boxed.
@noinline function wide_second(n: i32): (i32, i64) {
  return (n, n as i64 * 4000000000 as i64);
}

function main(): i32 {
  let s: string = "hey" + "".to_string();
  let b: Box2 = Box2 { n: 7, s: s };
  let total: i32 = 0;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 12) {
    let p: (Box2, boolean) = pair_fwd(i, b);
    total = total + p.0.n;
    if (p.1) {
      total = total + 1;
    }
    let k: (Box2, boolean) = picked(i, b);
    total = total + k.0.s.len();
    if (k.1) {
      total = total + 10;
    }
    let q: (i32, string) = scalar_first(i, s);
    total = total + q.0 + q.1.len();
    total = total + second_only(i, s).1;
    i = i + 1;
  }
  let a1: i64 = bench.alloc_count();
  let kept: (i32, string)[] = [];
  i = 0;
  while (i < 4) {
    kept = kept.append(kept_pair(i, s));
    i = i + 1;
  }
  for t in kept {
    total = total + t.0 + t.1.len();
  }
  let tr: (i32, i32, i32) = triple(5);
  total = total + tr.0 + tr.1 + tr.2;
  let w: (i32, i64) = wide_second(3);
  total = total + w.0 + (w.1 / 1000000000 as i64) as i32;
  print(total.to_string());
  if (total != 465) {
    return 1;
  }
  if (a1 != a0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

// The pairing under the suspend pass: a function that may park returns in
// two words like any other, since the pass keeps the read of the word with
// the call's store, ahead of the mode test it adds after the call. The
// program parks inside a task through a paired pair and a paired Result,
// which must answer right after the rewind, and calls both, and a function
// the classifier counts as able to park for its call through a value, in a
// loop that must allocate nothing. The total is the interpreter's.
const pairSuspendProg = `import "std/async";
import "std/bench";

struct Box2 { n: i32, s: string }

// Parks on a bound alone when asked, then returns a pair.
@noinline function parked_pair(n: i32, b: Box2, park: boolean): (Box2, boolean) {
  let k: i32 = n;
  if (park) {
    let none: i32[] = [];
    k = k + async.wait_any(none, 1) + 2;
  }
  return (b, k % 2 == 0);
}

// Parks when asked, then returns a Result.
@noinline function parked_res(n: i32, b: Box2, park: boolean): Result[Box2, i32] {
  let k: i32 = n;
  if (park) {
    let none: i32[] = [];
    k = k + async.wait_any(none, 1) + 2;
  }
  if (k % 2 == 0) {
    return Ok(b);
  }
  return Err(k);
}

// Calls through a value, so it counts as able to park once the program has
// a parking closure in it.
@noinline function via(n: i32, f: (i32) => i32, b: Box2): (Box2, boolean) {
  return (b, f(n) % 2 == 0);
}

function half(n: i32): i32 {
  return n / 2;
}

function body(b: Box2): i32 {
  let total: i32 = 0;
  let f: (i32) => i32 = half;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 12) {
    let p: (Box2, boolean) = parked_pair(i, b, false);
    total = total + p.0.n;
    if (p.1) {
      total = total + 1;
    }
    match (parked_res(i, b, false)) {
      Ok(x) => {
        total = total + x.n + x.s.len();
      },
      Err(e) => {
        total = total + e * 100;
      }
    }
    let v: (Box2, boolean) = via(i, f, b);
    total = total + v.0.n;
    if (v.1) {
      total = total + 1;
    }
    i = i + 1;
  }
  let a1: i64 = bench.alloc_count();
  if (a1 != a0) {
    return 0 - 2;
  }
  let q: (Box2, boolean) = parked_pair(3, b, true);
  total = total + q.0.s.len();
  if (q.1) {
    total = total + 1000;
  }
  match (parked_res(4, b, true)) {
    Ok(x) => {
      total = total + x.n * 10000;
    },
    Err(e) => {
      total = total + e * 10000;
    }
  }
  return total;
}

function drive(t: async.Task[i32]): i32 {
  let st: async.TaskStatus[i32] = async.task_start(t);
  let parks: i32 = 0;
  while (true) {
    match (st) {
      Done(v) => {
        if (parks != 2) {
          return 0 - 3;
        }
        return v;
      },
      Suspended(w) => {
        parks = parks + 1;
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.task_resume(t, woke);
      },
      Cancelled => {
        return 0 - 4;
      }
    }
  }
  return 0 - 4;
}

function main(): i32 {
  let s: string = "hey" + "".to_string();
  let b: Box2 = Box2 { n: 7, s: s };
  let t: async.Task[i32] = async.task_new(() => body(b));
  let total: i32 = drive(t);
  async.task_free(t);
  print(total.to_string());
  if (total == 0 - 2) {
    return 2;
  }
  if (total != 54843) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

func TestSelfHostPairReturn(t *testing.T) {
	cli := buildSelfHostCLI(t)
	progs := []struct{ name, src string }{{"variant", pairReturnProg}, {"pair", pairTupleProg}, {"suspend", pairSuspendProg}}
	for _, prog := range progs {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(prog.name+"/"+target, func(t *testing.T) {
				var stderr string
				var code int
				if target == "wasm32-wasi" && prog.name == "suspend" {
					// The task runtime imports wasi:io's poll, which only the
					// component form provides.
					src := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(src, []byte(prog.src), 0o644); err != nil {
						t.Fatal(err)
					}
					stderr, code = runWasmCensus(t, cli.wasmComponent(t, src, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1"))
				} else {
					stderr, code = cli.exitOf(t, prog.src, target, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
				}
				if code != 0 {
					t.Errorf("exit %d, want 0 (1: a wrong value, 2: the loop allocated, 99: a count went under)\n%s", code, stderr)
				}
				if !strings.Contains(stderr, "live_bytes=0") {
					t.Errorf("%q, want every block freed", stderr)
				}
			})
		}
	}
}
