# Deterministic asynchronous execution

This module runs futures with a deterministic virtual clock.

## Model

A simulation owns a virtual clock, a seeded random state, and scripted readiness. Futures can progress against that state without wall-clock delays.
The virtual clock starts at the instant `sim.at(DURATION_ZERO)`. Every time a test gives the simulation is an `Instant` on that clock.

## Contracts

Tests control readiness and time advancement. Seed sweeps repeat a property over deterministic states so a test can reproduce a failed seed.

## Executable example

```fern test name=std-sim-example
import "std/sim";

function main(): i32 {
  let a = sim.new(42);
  let b = sim.new(42);
  if (a.random_i32() != b.random_i32()) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Sim>>

<<4 pub function new(seed: i64): Sim>>

<<5 function __sim_rows(table: string): string[][]>>

<<6 pub function (s: Sim) random_i32(): i32>>

<<7 impl async.Driver for Sim>>

<<8 pub function (s: Sim) advance_to(to: Instant): boolean>>

<<9 function __next_due[T](drv: Sim, out: async.TaskStatus[T][], cancel_at: Option[Instant][]): i32>>

<<10 pub function future_at[T](at: Instant, v: T): async.Future[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs futures with a deterministic virtual clock.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i32";
import "core/int";
import "std/async";
import "std/rand";
import "std/time";
```

### `3 pub struct Sim`

```fern
<<3 pub struct Sim>>=
pub struct Sim { clock_ns: Cell[i64], rng: Cell[i64], watched: Cell[string], readiness: Cell[string] }
```

### `4 pub function new(seed: i64): Sim`

```fern
<<4 pub function new(seed: i64): Sim>>=
pub function new(seed: i64): Sim {
  return Sim { clock_ns: cell_new(0 as i64), rng: cell_new(rand.rng_seed(seed)), watched: cell_new(""), readiness: cell_new("") };
}

// The virtual instant `offset` after the simulation starts.
pub function at(offset: Duration): Instant {
  return Instant { sec: offset.sec, nsec: offset.nsec };
}

// The virtual time since the simulation started.
pub function (s: Sim) elapsed(): Duration {
  return time.duration_nanos(s.clock_ns.get());
}

pub function (s: Sim) ready_at(fd: i32, at: Instant, bits: i32): i32 {
  s.readiness.set(s.readiness.get() + int.int_to_string(fd) + ":" + int.int_to_string(__ms_of(at)) + ":" + int.int_to_string(bits) + ";");
  return 0;
}

// Readiness tokens and table rows hold virtual times in whole milliseconds.
function __ms_of(at: Instant): i32 {
  return (time.instant_nanos(at) / 1000000) as i32;
}
```

### `5 function __sim_rows(table: string): string[][]`

```fern
<<5 function __sim_rows(table: string): string[][]>>=
function __sim_rows(table: string): string[][] {
  let rows: string[][] = [];
  for entry in table.split(";") {
    if (entry.len() > 0) {
      rows = rows.append(entry.split(":"));
    }
  }
  return rows;
}

function __sim_field(row: string[], i: i32): i32 {
  match (i32.parse_i32_radix(row[i], 10)) {
    Ok(v) => {
      return v;
    },
    Err(_) => {
      return 0;
    }
  }
}

function __sim_interest(s: Sim, fd: i32): i32 {
  for row in __sim_rows(s.watched.get()) {
    if (__sim_field(row, 0) == fd) {
      return __sim_field(row, 1);
    }
  }
  return 0;
}

function __sim_rewatch(s: Sim, fd: i32, row: string): i32 {
  let out: string = "";
  for r in __sim_rows(s.watched.get()) {
    if (__sim_field(r, 0) != fd) {
      out = out + r[0] + ":" + r[1] + ";";
    }
  }
  s.watched.set(out + row);
  return 0;
}

function __roll(drv: Sim, n: i32): i32 {
  let step = rand.rng_below(drv.rng.get(), n);
  drv.rng.set(step.0);
  return step.1;
}
```

### `6 pub function (s: Sim) random_i32(): i32`

```fern
<<6 pub function (s: Sim) random_i32(): i32>>=
pub function (s: Sim) random_i32(): i32 {
  let step = rand.rng_next(s.rng.get());
  s.rng.set(step.0);
  return step.1 as i32;
}

pub function (s: Sim) rng_state(): i64 {
  return s.rng.get();
}

pub function sweep_seeds(n: i64, prop: (i64) => boolean): i64 {
  let s: i64 = 1;
  while (s <= n) {
    if (!prop(s)) {
      return s;
    }
    s = s + 1;
  }
  return 0;
}
```

### `7 impl async.Driver for Sim`

```fern
<<7 impl async.Driver for Sim>>=
// The virtual instant the simulation is at.
pub function (s: Sim) now(): Instant {
  return time.instant_from_nanos(s.clock_ns.get());
}

impl async.Driver for Sim {
  function watch(self: Sim, fd: i32, interest: i32): i32 {
    return __sim_rewatch(self, fd, int.int_to_string(fd) + ":" + int.int_to_string(interest & 3) + ";");
  }
  function unwatch(self: Sim, fd: i32): i32 {
    __sim_rewatch(self, fd, "");
    let kept: string = "";
    for r in __sim_rows(self.readiness.get()) {
      if (__sim_field(r, 0) != fd) {
        kept = kept + r[0] + ":" + r[1] + ":" + r[2] + ";";
      }
    }
    self.readiness.set(kept);
    return 0;
  }
  function wait_into(self: Sim, own events: i32[], timeout: Option[Duration]): i32[] {
    let timeout_ms: i32 = time.timeout_millis(timeout);
    let room: i32 = events.len() / 2;
    if (room <= 0) {
      return async.end_pairs(events, 0);
    }
    let now_ms: i32 = (self.clock_ns.get() / 1000000) as i32;
    let best: i32 = -1;
    for r in __sim_rows(self.readiness.get()) {
      if ((__sim_field(r, 2) & __sim_interest(self, __sim_field(r, 0))) != 0) {
        let at: i32 = __sim_field(r, 1);
        if (at < now_ms) {
          at = now_ms;
        }
        if (best < 0 || at < best) {
          best = at;
        }
      }
    }
    if (timeout_ms >= 0 && (best < 0 || now_ms + timeout_ms < best)) {
      self.clock_ns.set(self.clock_ns.get() + timeout_ms as i64 * 1000000);
      return async.end_pairs(events, 0);
    }
    if (best < 0) {
      return async.end_pairs(events, 0);
    }
    self.clock_ns.set(best as i64 * 1000000);
    let kept: string = "";
    let n: i32 = 0;
    for r in __sim_rows(self.readiness.get()) {
      let fd: i32 = __sim_field(r, 0);
      let at: i32 = __sim_field(r, 1);
      let bits: i32 = __sim_field(r, 2);
      if (at < now_ms) {
        at = now_ms;
      }
      if (at == best && (bits & __sim_interest(self, fd)) != 0 && n < room) {
        events = events.with(n * 2, fd).with(n * 2 + 1, bits);
        n = n + 1;
      } else {
        kept = kept + r[0] + ":" + r[1] + ":" + r[2] + ";";
      }
    }
    self.readiness.set(kept);
    return async.end_pairs(events, n);
  }
  function watch_signal(self: Sim, sig: i32): i32 {
    return self.watch(0 - sig, 1);
  }
  function unwatch_signal(self: Sim, sig: i32): i32 {
    return self.unwatch(0 - sig);
  }
  function watch_parent(self: Sim): i32 {
    return 0;
  }
  function close(self: Sim): i32 {
    return 0;
  }
}
```

### `8 pub function (s: Sim) advance_to(to: Instant): boolean`

Inside a task, the clock moves only when the scheduler resumes the task. So
`advance_to` parks until `to`, and `run_tasks` moves the clock to the earliest
instant that some task waits for. A pair in a wait set never becomes ready
under the simulation: a task waits only for a deadline or a cancellation.

```fern
<<8 pub function (s: Sim) advance_to(to: Instant): boolean>>=
// False when the calling task is cancelled before `to`.
pub function (s: Sim) advance_to(to: Instant): boolean {
  if (async.in_task()) {
    let none: i32[] = [];
    if let async.Wake.Cancelled = async.wait_any(none, Some(to)) {
      return false;
    }
  }
  let to_ns: i64 = time.instant_nanos(to);
  if (to_ns > s.clock_ns.get()) {
    s.clock_ns.set(to_ns);
  }
  return true;
}

// Task `i` is cancelled at `cancel_at[i]` when that is present.
pub function run_tasks[T](drv: Sim, entries: (() => T)[], cancel_at: Option[Instant][]): async.TaskStatus[T][] {
  let tasks: async.Task[T][] = [];
  let out: async.TaskStatus[T][] = [];
  for e in entries {
    let t: async.Task[T] = async.task_new(e);
    tasks = tasks.append(t);
    out = out.append(async.task_start(t));
  }
  let next: i32 = __next_due(drv, out, cancel_at);
  while (next >= 0) {
    out = out.with(next, __run_due(drv, tasks[next], out[next], __cancel_at(cancel_at, next)));
    next = __next_due(drv, out, cancel_at);
  }
  return out;
}

// Drives `f` on the virtual clock, with no task. None when `f` ends cancelled
// or waits with no deadline.
pub function block_on[T](drv: Sim, f: async.Future[T]): Option[T] {
  let cur: async.Future[T] = f;
  while (true) {
    match (cur) {
      Ready(v) => {
        return Some(v);
      },
      Cancelled => {
        return None;
      },
      Pending(w, k) => {
        let Some(at) = w.until else {
          let gone: async.Future[T] = k(async.Wake.Cancelled);
          return None;
        };
        let moved: boolean = drv.advance_to(at);
        cur = k(async.Wake.TimedOut);
      }
    }
  }
  return None;
}
```

### `9 function __next_due[T](drv: Sim, out: async.TaskStatus[T][], cancel_at: Option[Instant][]): i32`

```fern
<<9 function __next_due[T](drv: Sim, out: async.TaskStatus[T][], cancel_at: Option[Instant][]): i32>>=
function __cancel_at(cancel_at: Option[Instant][], i: i32): Option[Instant] {
  if (i < cancel_at.len()) {
    return cancel_at[i];
  }
  return None;
}

// The virtual nanosecond a wait is due at, never before the clock: -1 when
// nothing is due.
function __wait_due(w: async.Wait, now_ns: i64, cancel: Option[Instant]): i64 {
  let due: i64 = 0 - 1;
  for at in [w.until, cancel] {
    if let Some(i) = at {
      let ns: i64 = time.instant_nanos(i);
      if (due < 0 || ns < due) {
        due = ns;
      }
    }
  }
  if (due >= 0 && due < now_ns) {
    return now_ns;
  }
  return due;
}

function __next_due[T](drv: Sim, out: async.TaskStatus[T][], cancel_at: Option[Instant][]): i32 {
  let now_ns: i64 = drv.clock_ns.get();
  let best: i64 = 0 - 1;
  let ties: i32[] = [];
  for i in 0..out.len() {
    if let Suspended(w) = out[i] {
      let due: i64 = __wait_due(w, now_ns, __cancel_at(cancel_at, i));
      if (due >= 0) {
        if (ties.len() == 0 || due < best) {
          best = due;
          ties = [i];
        } else if (due == best) {
          ties = ties.append(i);
        }
      }
    }
  }
  if (ties.len() == 0) {
    return 0 - 1;
  }
  if (ties.len() == 1) {
    return ties[0];
  }
  return ties[__roll(drv, ties.len())];
}

// A cancellation due at the same instant as the deadline wins.
function __run_due[T](drv: Sim, t: async.Task[T], st: async.TaskStatus[T], cancel: Option[Instant]): async.TaskStatus[T] {
  let Suspended(w) = st else {
    return st;
  };
  let due: i64 = __wait_due(w, drv.clock_ns.get(), cancel);
  drv.clock_ns.set(due);
  if let Some(c) = cancel {
    if (time.instant_nanos(c) <= due) {
      return async.task_resume(t, async.Wake.Cancelled);
    }
  }
  return async.task_resume(t, async.Wake.TimedOut);
}
```

### `10 pub function future_at[T](at: Instant, v: T): async.Future[T]`

```fern
<<10 pub function future_at[T](at: Instant, v: T): async.Future[T]>>=
pub function future_at[T](at: Instant, v: T): async.Future[T] {
  let done: async.Future[T] = Ready(v);
  return __pend(at, done);
}

// Ready at `at`, after `resuspends` further waits `step` apart.
pub function future_chain[T](at: Instant, step: Duration, resuspends: i32, v: T): async.Future[T] {
  let f: async.Future[T] = Ready(v);
  for back in 0..resuspends + 1 {
    f = __pend(at.add(step.mul(resuspends - back)), f);
  }
  return f;
}

function __pend[T](at: Instant, next: async.Future[T]): async.Future[T] {
  let none: i32[] = [];
  return Pending(async.Wait { set: none, until: Some(at) }, (wake: async.Wake): async.Future[T] => next);
}
```
