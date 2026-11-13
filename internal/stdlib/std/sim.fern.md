# Deterministic asynchronous execution

This module runs futures with a deterministic virtual clock.

## Model

A simulation owns a virtual clock, a seeded random state, and scripted readiness. Futures can progress against that state without wall-clock delays.
The virtual clock starts at the instant `sim.at(DURATION_ZERO)`. Every time a test gives the simulation is an `Instant` on that clock.

A simulation is a `worker.Scheduler`. A worker spawned on it with `worker.spawn_on` runs as a task, and its turns come from the seed. At each spawn the seed says whether a parked worker runs before the spawner goes on. A join runs the parked workers in the order of their deadlines until the joined worker is done.

## Contracts

Tests control readiness and time advancement. Seed sweeps repeat a property over deterministic states so a test can reproduce a failed seed.
A worker that parks on the simulation moves its clock when it is resumed. A worker that waits for an event the simulation never gives stops the program with exit 134 at its join.

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

<<11 impl worker.Scheduler for Sim>>
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
import "std/async";
import "std/rand";
import "std/time";
import "std/worker";
```

### `3 pub struct Sim`

```fern
<<3 pub struct Sim>>=
pub struct Sim { clock_ns: Cell[i64], rng: Cell[i64], watched: Cell[string], readiness: Cell[string], workers: Cell[WorkerSlot[]] }

// A worker of the simulation: its task, the root of its thunk while it is
// parked, its wait, and the root of its result once it is done. A worker
// that is running has no root yet.
pub struct WorkerSlot { id: i32, root: i64, set: i32[], until: Option[Instant], done: boolean }
```

### `4 pub function new(seed: i64): Sim`

```fern
<<4 pub function new(seed: i64): Sim>>=
pub function new(seed: i64): Sim {
  let none: WorkerSlot[] = [];
  return Sim { clock_ns: cell_new(0 as i64), rng: cell_new(rand.rng_seed(seed)), watched: cell_new(""), readiness: cell_new(""), workers: cell_new(none) };
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
  s.readiness.set(s.readiness.get() + fd.to_string() + ":" + __ms_of(at).to_string() + ":" + bits.to_string() + ";");
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
  let out = "";
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
    s += 1;
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
    return __sim_rewatch(self, fd, fd.to_string() + ":" + (interest & 3).to_string() + ";");
  }
  function unwatch(self: Sim, fd: i32): i32 {
    __sim_rewatch(self, fd, "");
    let kept = "";
    for r in __sim_rows(self.readiness.get()) {
      if (__sim_field(r, 0) != fd) {
        kept = kept + r[0] + ":" + r[1] + ":" + r[2] + ";";
      }
    }
    self.readiness.set(kept);
    return 0;
  }
  function wait_into(self: Sim, own events: i32[], timeout: Option[Duration]): i32[] {
    let timeout_ms = time.timeout_millis(timeout);
    let room = events.len() / 2;
    if (room <= 0) {
      return async.end_pairs(events, 0);
    }
    let now_ms = (self.clock_ns.get() / 1000000) as i32;
    let best = -1;
    for r in __sim_rows(self.readiness.get()) {
      if ((__sim_field(r, 2) & __sim_interest(self, __sim_field(r, 0))) != 0) {
        let at = __sim_field(r, 1);
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
    let kept = "";
    let n = 0;
    for r in __sim_rows(self.readiness.get()) {
      let fd = __sim_field(r, 0);
      let at = __sim_field(r, 1);
      let bits = __sim_field(r, 2);
      if (at < now_ms) {
        at = now_ms;
      }
      if (at == best && (bits & __sim_interest(self, fd)) != 0 && n < room) {
        events = events.with(n * 2, fd).with(n * 2 + 1, bits);
        n += 1;
      } else {
        kept = kept + r[0] + ":" + r[1] + ":" + r[2] + ";";
      }
    }
    self.readiness.set(kept);
    return async.end_pairs(events, n);
  }
  function watch_signal(self: Sim, sig: i32): i32 {
    return self.watch(-sig, 1);
  }
  function unwatch_signal(self: Sim, sig: i32): i32 {
    return self.unwatch(-sig);
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
  let to_ns = time.instant_nanos(to);
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
    let t = async.task_new(e);
    tasks = tasks.append(t);
    out = out.append(async.task_start(t));
  }
  let next = __next_due(drv, out, cancel_at);
  while (next >= 0) {
    out = out.with(next, __run_due(drv, tasks[next], out[next], __cancel_at(cancel_at, next)));
    next = __next_due(drv, out, cancel_at);
  }
  return out;
}

// Drives `f` on the virtual clock, with no task. None when `f` ends cancelled
// or waits with no deadline.
pub function block_on[T](drv: Sim, own f: async.Future[T]): Option[T] {
  let cur = f;
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
          match (k(async.Wake.Cancelled)) {
            _ => {}
          }
          return None;
        };
        let moved = drv.advance_to(at);
        cur = k(async.Wake.TimedOut);
      }
    }
  }
  // The loop does not end. This match ends the obligation of `cur`.
  match (cur) {
    Ready(v) => {
      return Some(v);
    },
    _ => {
      return None;
    }
  }
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
  let due = -1;
  for at in [w.until, cancel] {
    if let Some(i) = at {
      let ns = time.instant_nanos(i);
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
  let now_ns = drv.clock_ns.get();
  let best = -1;
  let ties: i32[] = [];
  for i in 0..out.len() {
    if let Suspended(w) = out[i] {
      let due = __wait_due(w, now_ns, __cancel_at(cancel_at, i));
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
    return -1;
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
  let due = __wait_due(w, drv.clock_ns.get(), cancel);
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
  let done = Ready(v);
  return __pend(at, done);
}

// Ready at `at`, after `resuspends` further waits `step` apart.
pub function future_chain[T](at: Instant, step: Duration, resuspends: i32, v: T): async.Future[T] {
  let f = Ready(v);
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

### `11 impl worker.Scheduler for Sim`

A worker's body starts at `spawn`, as a process reads its captures at the
fork. A body that parks waits in its slot, and the thunk of its task is
rooted, so the slot holds plain words. A turn adopts the thunk back, moves
the clock to the wait's deadline and resumes the task.

```fern
<<11 impl worker.Scheduler for Sim>>=
impl worker.Scheduler for Sim {
  function admit(self: Sim, run: () => usize): i32 {
    let t: async.Task[usize] = async.task_new(run);
    let index: i32 = __slot_add(self, WorkerSlot { id: async.task_id(t), root: 0i64, set: [], until: None, done: false });
    __slot_set(self, index, __turned(t, async.task_start(t)));
    __worker_turns(self, 0 - 1);
    return index;
  }
  function finish(self: Sim, index: i32): usize {
    __worker_turns(self, index);
    return self.workers.get()[index].root as usize;
  }
}

function __slot_add(s: Sim, slot: WorkerSlot): i32 {
  let slots: WorkerSlot[] = s.workers.get();
  s.workers.set(slots.append(slot));
  return slots.len();
}

function __slot_set(s: Sim, index: i32, slot: WorkerSlot): void {
  s.workers.set(s.workers.get().with(index, slot));
}

// The slot of a task after a turn: the root of its result when it is done,
// or its wait and the root of its thunk while it is parked.
function __turned(t: async.Task[usize], st: async.TaskStatus[usize]): WorkerSlot {
  match (st) {
    Done(root) => {
      return WorkerSlot { id: async.task_id(t), root: root as i64, set: [], until: None, done: true };
    },
    Suspended(w) => {
      unsafe {
        let root: usize = __send_root(async.task_entry(t));
        return WorkerSlot { id: async.task_id(t), root: root as i64, set: w.set, until: w.until, done: false };
      }
    }
  }
}

function __adopt_thunk(root: usize): () => usize {
  unsafe {
    let f: () => usize = __send_adopt(root);
    return f;
  }
}

// Seeded turns of the parked workers. With `until` at a worker, the turns go
// on until that worker is done. Otherwise each turn is the seed's call, and
// the spawner goes on when the seed says so.
function __worker_turns(s: Sim, until: i32): void {
  while (true) {
    if (until >= 0) {
      if (s.workers.get()[until].done) {
        return;
      }
    } else if (__roll(s, 2) == 0) {
      return;
    }
    let next = __next_slot(s);
    if (next < 0) {
      if (until < 0) {
        return;
      }
      eprint("sim: worker " + until.to_string() + " waits for an event the simulation never gives");
      exit(134);
    }
    __resume_slot(s, next);
  }
}

// The parked worker whose wait is due soonest. The seed breaks a tie.
function __next_slot(s: Sim): i32 {
  let now_ns: i64 = s.clock_ns.get();
  let best: i64 = 0 - 1;
  let ties: i32[] = [];
  let slots: WorkerSlot[] = s.workers.get();
  for i in 0..slots.len() {
    if (!slots[i].done && slots[i].root != 0i64) {
      let due: i64 = __wait_due(async.Wait { set: slots[i].set, until: slots[i].until }, now_ns, None);
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
  return ties[__roll(s, ties.len())];
}

function __resume_slot(s: Sim, index: i32): void {
  let slot = s.workers.get()[index];
  unsafe {
    // The slot holds the id and the rooted thunk of the task that parked.
    let t = async.task_adopted(slot.id, __adopt_thunk(slot.root as usize));
    s.clock_ns.set(__wait_due(async.Wait { set: slot.set, until: slot.until }, s.clock_ns.get(), None));
    __slot_set(s, index, __turned(t, async.task_resume(t, async.Wake.TimedOut)));
  }
}
```
