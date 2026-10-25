package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// A first element no word holds on every target: it is the result itself.
@noinline function wide_first(n: i32, b: Box2): (u64, Box2) {
  return (n as u64 * 4000000000 as u64, b);
}

@noinline function float_first(n: i32, s: string): (f64, string) {
  return (n as f64 * 0.5, s);
}

// Only the second element is read.
@noinline function second_only(n: i32, s: string): (string, i32) {
  return (s, n * 3);
}

// Kept whole by a caller, so it stays boxed.
@noinline function kept_pair(n: i32, s: string): (i32, string) {
  return (n, s);
}

// Three scalar elements return in scalar lanes.
@noinline function triple(n: i32): (i32, i32, i32) {
  return (n, n + 1, n + 2);
}

// A second element wider than a word returns in scalar lanes.
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
    let wf: (u64, Box2) = wide_first(i, b);
    total = total + (wf.0 / 1000000000 as u64) as i32 + wf.1.n;
    let ff: (f64, string) = float_first(i, s);
    total = total + (ff.0 * 2.0) as i32 + ff.1.len();
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
  if (total != 915) {
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

const pairMethodProg = `import "std/bench";

enum Got { Chunk(string), Empty, Late }

trait Source {
  function next(self: Self, n: i32): Result[Got, i32];
}

struct Feed { s: string, k: i32 }

impl Source for Feed {
  function next(self: Feed, n: i32): Result[Got, i32] {
    if (n % 4 == 3) {
      return Err(n);
    }
    if (n % 4 == 2) {
      return Ok(Empty);
    }
    return Ok(Chunk(self.s));
  }
}

// A trait reached through a dyn value: the dispatch calls each
// implementation with the boxed result, so they stay unpaired.
trait Peer {
  function next(self: Self, n: i32): Result[Got, i32];
}

struct Other { s: string }

impl Peer for Other {
  function next(self: Other, n: i32): Result[Got, i32] {
    if (n % 2 == 0) {
      return Ok(Late);
    }
    return Ok(Chunk(self.s));
  }
}

@noinline function (f: Feed) peek(n: i32): Option[string] {
  if (n % 2 == 0) {
    return Some(f.s);
  }
  return None;
}

function take[T: Source](src: T, n: i32): i32 {
  match (src.next(n)) {
    Ok(Chunk(s)) => {
      return s.len();
    },
    Ok(Empty()) => {
      return 100;
    },
    Ok(Late()) => {
      return 1000;
    },
    Err(e) => {
      return e * 10000;
    }
  }
  return 0;
}

function through(d: dyn Peer, n: i32): i32 {
  match (d.next(n)) {
    Ok(Chunk(s)) => {
      return s.len();
    },
    Ok(Late()) => {
      return 7;
    },
    _ => {
      return 0;
    }
  }
  return 0;
}

function main(): i32 {
  let f: Feed = Feed { s: "hey" + "".to_string(), k: 1 };
  let total: i32 = 0;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 12) {
    total = total + take(f, i);
    match (f.peek(i)) {
      Some(s) => {
        total = total + s.len();
      },
      None => {
        total = total + 1;
      }
    }
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  let o: Other = Other { s: "abcd" + "".to_string() };
  total = total + through(o, 0) + through(o, 1);
  print(total.to_string() + " " + allocated.to_string());
  if (total != 210353) {
    return 1;
  }
  if (allocated != 6) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

const pairLanesProg = `import "std/bench";

enum Step { Go((i32, i64, i32)), Stop, Skip(f64) }

@noinline function wide(n: i32): Option[i64] {
  if (n % 4 == 3) {
    return None;
  }
  return Some(n as i64 * 4000000000 as i64);
}

@noinline function forward(n: i32): Option[i64] {
  return wide(n + 1);
}

@noinline function held(n: i32): Option[i64] {
  let r: Option[i64] = None;
  if (n % 2 == 0) {
    r = Some(n as i64 + 9000000000 as i64);
  }
  return r;
}

@noinline function half(n: i32): Option[f64] {
  if (n % 5 == 0) {
    return None;
  }
  return Some(n as f64 / 2.0);
}

@noinline function find(xs: i64[], want: i64): Option[(i32, i64)] {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] == want) {
      return Some((i, xs[i] * 3000000000 as i64));
    }
    i = i + 1;
  }
  return None;
}

@noinline function decode(buf: [u8], at: i32): Result[(i64, i32), i32] {
  if (at >= buf.len()) {
    return Err(1);
  }
  let v: i64 = buf[at] as i64;
  if (v > 200) {
    return Err(2);
  }
  return Ok((v * 3000000000 as i64, at + 1));
}

@noinline function step(n: i32): Step {
  if (n % 3 == 0) {
    return Go((n, n as i64 * 5000000000 as i64, 0 - n));
  }
  if (n % 3 == 1) {
    return Stop;
  }
  return Skip(n as f64 * 0.25);
}

function main(): i32 {
  let xs: i64[] = [5 as i64, 6 as i64, 7 as i64];
  let buf: u8[] = [3 as u8, 250 as u8, 7 as u8];
  let total: i64 = 0;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 12) {
    match (wide(i)) {
      Some(w) => {
        total = total + w;
      },
      None => {
        total = total + 1;
      }
    }
    match (forward(i)) {
      Some(w) => {
        total = total + w / 2;
      },
      None => {}
    }
    match (held(i)) {
      Some(w) => {
        total = total + w;
      },
      None => {
        total = total + 3;
      }
    }
    match (half(i)) {
      Some(h) => {
        total = total + (h * 4.0) as i64;
      },
      None => {}
    }
    match (find(xs, (i % 4 + 5) as i64)) {
      Some(p) => {
        total = total + p.1 + p.0 as i64;
      },
      None => {
        total = total + 11;
      }
    }
    match (decode(buf, i % 4)) {
      Ok(r) => {
        total = total + r.0 + r.1 as i64;
      },
      Err(e) => {
        total = total + e as i64 * 100;
      }
    }
    match (step(i)) {
      Go(g) => {
        total = total + g.1 + (g.0 * g.2) as i64;
      },
      Stop => {
        total = total + 7;
      },
      Skip(f) => {
        total = total + (f * 8.0) as i64;
      }
    }
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + allocated.to_string());
  if (total != 690000001061 as i64) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

const pairSplitProg = `import "std/bench";

struct Sealed { record: u8[], next: string }
struct Mixed { n: i32, s: string }
struct Run { server: string, out: u8[] }

enum Step { Go(Run), Wait(Sealed), Done(i32) }
enum Fault { Bad(i32) }
enum Wrapped { Inner(Fault), Odd(string) }

@noinline function seal(n: i32, buf: u8[], s: string): Result[Sealed, string] {
  if (n % 5 == 4) {
    return Err(s);
  }
  return Ok(Sealed { record: buf, next: s });
}

@noinline function pair(n: i32, s: string): Result[(string, i32), i32] {
  if (n % 3 == 2) {
    return Err(n);
  }
  return Ok((s, n * 2));
}

// The payload is a parameter, so the callee reads its fields out.
@noinline function mixed(n: i32, m: Mixed): Option[Mixed] {
  if (n % 2 == 1) {
    return None;
  }
  return Some(m);
}

@noinline function fwd(n: i32, buf: u8[], s: string): Result[Sealed, string] {
  return seal(n + 1, buf, s);
}

@noinline function step(server: string, out: u8[], n: i32): Result[Run, string] {
  if (n > 1000) {
    return Err(server);
  }
  return Ok(Run { server: server, out: out });
}

// A payload carried round a loop, as std/tls's __server_drain does.
@noinline function drain(s: string, o: u8[], k: i32): Result[Run, string] {
  let r: Run = step(s, o, 0)?;
  let i: i32 = 0;
  while (i < k) {
    r = step(r.server, r.out, i)?;
    i = i + 1;
  }
  return Ok(Run { server: r.server, out: r.out });
}

// Named like Sealed's field, which must not count as a use of the function.
@noinline function next(n: i32, s: string, buf: u8[]): Step {
  if (n % 3 == 0) {
    return Go(Run { server: s, out: buf });
  }
  if (n % 3 == 1) {
    return Wait(Sealed { record: buf, next: s });
  }
  return Done(n);
}

// Called only where a caller keeps the payload, so it keeps one word.
@noinline function kept(n: i32, s: string): Result[Mixed, i32] {
  if (n < 0) {
    return Err(n);
  }
  return Ok(Mixed { n: n, s: s });
}

// An instance of a generic re-wraps its argument's payload.
function wrap[T](r: Result[T, Fault]): Result[T, Wrapped] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Inner(e));
    }
  }
  return Err(Odd("unreachable"));
}

@noinline function built(n: i32, buf: u8[]): Result[Sealed, Fault] {
  if (n > 2) {
    return Err(Bad(n));
  }
  return Ok(Sealed { record: buf, next: "x" + "".to_string() });
}

function main(): i32 {
  let s: string = "hey" + "".to_string();
  let buf: u8[] = [1 as u8, 2 as u8, 3 as u8];
  let m: Mixed = Mixed { n: 7, s: s };
  let total: i32 = 0;
  let i: i32 = 0;
  let a0: i64 = bench.alloc_count();
  while (i < 20) {
    match (seal(i, buf, s)) {
      Ok(x) => {
        total = total + x.record.len() + x.next.len();
      },
      Err(e) => {
        total = total + e.len() * 10;
      }
    }
    match (pair(i, s)) {
      Ok(p) => {
        total = total + p.0.len() + p.1;
      },
      Err(e) => {
        total = total + e * 100;
      }
    }
    match (mixed(i, m)) {
      Some(x) => {
        total = total + x.n + x.s.len();
      },
      None => {
        total = total + 1;
      }
    }
    match (fwd(i, buf, s)) {
      Ok(x) => {
        total = total + x.record.len();
      },
      Err(e) => {
        total = total + e.len();
      }
    }
    match (drain(s, buf, i % 4)) {
      Ok(r) => {
        total = total + r.server.len() + r.out.len();
      },
      Err(e) => {
        total = total + e.len();
      }
    }
    match (next(i, s, buf)) {
      Go(r) => {
        total = total + r.server.len() * 1000;
      },
      Wait(w) => {
        total = total + w.record.len() * 10000;
      },
      Done(d) => {
        total = total + d;
      }
    }
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  let held: Mixed[] = [];
  i = 0;
  while (i < 3) {
    match (kept(i, s)) {
      Ok(x) => {
        held = held.append(x);
      },
      Err(_) => {}
    }
    match (wrap(built(i, buf))) {
      Ok(x) => {
        total = total + x.record.len() + x.next.len();
      },
      Err(_) => {
        total = total + 50;
      }
    }
    i = i + 1;
  }
  for h in held {
    total = total + h.n + h.s.len();
  }
  print(total.to_string() + " " + allocated.to_string());
  if (total != 237595) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

// A wrapper that returns its split callee's payload whole is split too, so
// neither builds the pair.
const pairForwardProg = `import "std/bench";

enum Fail { Bad(string), Short }

@noinline function read(buf: u8[], at: i32): Result[(string, i32), Fail] {
  if (at >= buf.len()) {
    return Err(Short);
  }
  if (buf[at] == 0) {
    return Err(Bad("zero"));
  }
  return Ok(("abc", at + buf[at] as i32));
}

// The wrapper hands the callee's payload on whole, as qpack.__sec_str does.
@noinline function forward(buf: u8[], at: i32): Result[(string, i32), Fail] {
  match (read(buf, at)) {
    Ok(r) => {
      return Ok(r);
    },
    Err(e) => {
      return Err(e);
    }
  }
}

function main(): i32 {
  let buf: u8[] = [1 as u8, 2 as u8, 0 as u8, 3 as u8];
  let total: i32 = 0;
  let a0: i64 = bench.alloc_count();
  let i: i32 = 0;
  while (i < 20) {
    match (forward(buf, i % 5)) {
      Ok(p) => {
        total = total + p.0.len() + p.1;
      },
      Err(Bad(why)) => {
        total = total + why.len() * 100;
      },
      Err(Short()) => {
        total = total + 1000;
      }
    }
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + allocated.to_string());
  if (total != 5676) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

// A 64-bit payload, alone or as either part of a split pair, beside an
// error that holds a string: it goes back in its slot's 64-bit lane.
const pairWideProg = `import "std/bench";

enum Fail { Bad(string), Short }

// A 64-bit part goes back in its slot's lane beside the error's word.
@noinline function first_wide(buf: u8[], at: i32): Result[(i64, i32), Fail] {
  if (at >= buf.len()) {
    return Err(Short);
  }
  if (buf[at] == 0) {
    return Err(Bad("zero"));
  }
  return Ok((buf[at] as i64 << 40, at + 1));
}

@noinline function second_wide(buf: u8[], at: i32): Result[(string, i64), Fail] {
  if (at >= buf.len()) {
    return Err(Short);
  }
  return Ok(("ab", 0 - (buf[at] as i64 << 33)));
}

// A 64-bit payload alone goes back in the first lane.
@noinline function single(buf: u8[], at: i32): Result[i64, Fail] {
  if (at >= buf.len()) {
    return Err(Short);
  }
  return Ok(buf[at] as i64 << 36);
}

@noinline function forward(buf: u8[], at: i32): Result[(i64, i32), Fail] {
  match (first_wide(buf, at)) {
    Ok(r) => {
      return Ok(r);
    },
    Err(e) => {
      return Err(e);
    }
  }
}

function main(): i32 {
  let buf: u8[] = [1 as u8, 2 as u8, 0 as u8, 3 as u8];
  let total: i64 = 0;
  let a0: i64 = bench.alloc_count();
  let i: i32 = 0;
  while (i < 20) {
    match (forward(buf, i % 5)) {
      Ok(p) => {
        total = total + (p.0 >> 40) * 1000 + p.1 as i64;
      },
      Err(Bad(why)) => {
        total = total + why.len() as i64 * 100000;
      },
      Err(Short()) => {
        total = total + 7;
      }
    }
    match (second_wide(buf, i % 5)) {
      Ok(p) => {
        total = total + p.0.len() as i64 - (p.1 >> 33);
      },
      Err(_) => {
        total = total + 9;
      }
    }
    match (single(buf, i % 5)) {
      Ok(v) => {
        total = total + (v >> 36) * 3;
      },
      Err(_) => {
        total = total + 11;
      }
    }
    i = i + 1;
  }
  let allocated: i64 = bench.alloc_count() - a0;
  print(total.to_string() + " " + allocated.to_string());
  if (total != 1624264) {
    return 1;
  }
  if (allocated != 0) {
    return 2;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  return 0;
}
`

func TestSelfHostPairReturn(t *testing.T) {
	cli := buildSelfHostCLI(t)
	progs := []struct{ name, src string }{{"variant", pairReturnProg}, {"pair", pairTupleProg}, {"suspend", pairSuspendProg}, {"method", pairMethodProg}, {"lanes", pairLanesProg}, {"split", pairSplitProg}, {"forward", pairForwardProg}, {"wide", pairWideProg}}
	for _, prog := range progs {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(prog.name+"/"+target, func(t *testing.T) {
				var stderr string
				var code int
				if target == "wasm32-wasi" && prog.name == "suspend" {
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
