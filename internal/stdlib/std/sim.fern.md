# Deterministic asynchronous execution

This module runs futures with a deterministic virtual clock.

## Model

A simulation owns a virtual clock, a seeded random state, and scripted readiness. Futures can progress against that state without wall-clock delays.

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

<<8 pub function (s: Sim) advance_to(to_ns: i64): boolean>>

<<9 function __settled[T](t: async.Task[T], st: async.TaskStatus[T]): async.TaskStatus[T]>>

<<10 pub function future_at[T](drv: Sim, at_ns: i64, v: T): async.Future[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs futures with a deterministic virtual clock.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/async";
import "std/rand";
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

pub function (s: Sim) ready_at(fd: i32, at_ms: i32, bits: i32): i32 {
  s.readiness.set(s.readiness.get() + int.int_to_string(fd) + ":" + int.int_to_string(at_ms) + ":" + int.int_to_string(bits) + ";");
  return 0;
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
  match (int.parse_int_radix(row[i], 10)) {
    Some(v) => {
      return v;
    },
    None => {
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
impl async.Driver for Sim {
  function poll_ready(self: Sim, toks: i32[], timeout_ms: i32): i32 {
    if (__task_cur() != 0) {
      return async.park_tokens(toks, timeout_ms);
    }
    if (toks.len() == 0) {
      return -1;
    }
    let best: i32 = -1;
    let i: i32 = 0;
    while (i < toks.len()) {
      if (toks[i] >= 0) {
        if (best < 0 || toks[i] < best) {
          best = toks[i];
        }
      }
      i = i + 1;
    }
    let now: i64 = self.clock_ns.get();
    if (timeout_ms >= 0) {
      let timeout_at: i64 = now + timeout_ms as i64 * 1000000;
      if (best < 0 || timeout_at < best as i64 * 1000000) {
        self.clock_ns.set(timeout_at);
        return -1;
      }
    }
    if (best < 0) {
      return -1;
    }
    let ready_ns: i64 = best as i64 * 1000000;
    if (ready_ns > now) {
      self.clock_ns.set(ready_ns);
    }
    let ties: i32[] = [];
    let k: i32 = 0;
    while (k < toks.len()) {
      if (toks[k] == best) {
        ties = ties.append(k);
      }
      k = k + 1;
    }
    if (ties.len() == 1) {
      return ties[0];
    }
    return ties[__roll(self, ties.len())];
  }
  function now_ns(self: Sim): i64 {
    return self.clock_ns.get();
  }
  function timer(self: Sim, ns: i64): i32 {
    return ((self.clock_ns.get() + ns) / 1000000) as i32;
  }
  function drop_token(self: Sim, tok: i32): i32 {
    return 0;
  }
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
  function wait_into(self: Sim, own events: i32[], timeout_ms: i32): i32[] {
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

### `8 pub function (s: Sim) advance_to(to_ns: i64): boolean`

```fern
<<8 pub function (s: Sim) advance_to(to_ns: i64): boolean>>=
pub function (s: Sim) advance_to(to_ns: i64): boolean {
  if (__task_cur() != 0) {
    let set: i32[] = [(to_ns / 1000000) as i32, 1];
    if (async.wait_any(set, 0 - 1) == async.cancelled()) {
      return false;
    }
  }
  if (to_ns > s.clock_ns.get()) {
    s.clock_ns.set(to_ns);
  }
  return true;
}

pub function run_tasks[T](drv: Sim, entries: (() => T)[], cancel_at_ms: i32[]): async.TaskStatus[T][] {
  let tasks: async.Task[T][] = [];
  let out: async.TaskStatus[T][] = [];
  for e in entries {
    let t: async.Task[T] = async.task_new(e);
    tasks = tasks.append(t);
    out = out.append(__settled(t, async.task_start(t)));
  }
  let next: i32 = __next_due(drv, out, cancel_at_ms);
  while (next >= 0) {
    out = out.with(next, __run_due(drv, tasks[next], out[next], __cancel_ms(cancel_at_ms, next)));
    next = __next_due(drv, out, cancel_at_ms);
  }
  return out;
}
```

### `9 function __settled[T](t: async.Task[T], st: async.TaskStatus[T]): async.TaskStatus[T]`

```fern
<<9 function __settled[T](t: async.Task[T], st: async.TaskStatus[T]): async.TaskStatus[T]>>=
function __settled[T](t: async.Task[T], st: async.TaskStatus[T]): async.TaskStatus[T] {
  match (st) {
    Suspended(w) => {},
    Done(v) => {
      async.task_free(t);
    },
    Cancelled => {
      async.task_free(t);
    }
  }
  return st;
}

function __cancel_ms(cancel_at_ms: i32[], i: i32): i32 {
  if (i < cancel_at_ms.len()) {
    return cancel_at_ms[i];
  }
  return 0 - 1;
}

function __now_ms(drv: Sim): i32 {
  return (drv.clock_ns.get() / 1000000) as i32;
}

function __wait_due(w: async.Wait, now_ms: i32, cancel_ms: i32): i32 {
  let due: i32 = cancel_ms;
  if (w.timeout_ms >= 0 && (due < 0 || now_ms + w.timeout_ms < due)) {
    due = now_ms + w.timeout_ms;
  }
  let i: i32 = 0;
  while (i * 2 < w.set.len()) {
    let at: i32 = w.set[i * 2];
    if (at >= 0 && (due < 0 || at < due)) {
      due = at;
    }
    i = i + 1;
  }
  if (due < now_ms) {
    return now_ms;
  }
  return due;
}

function __due_pairs(w: async.Wait, now_ms: i32, due_ms: i32): i32[] {
  let ties: i32[] = [];
  let i: i32 = 0;
  while (i * 2 < w.set.len()) {
    let at: i32 = w.set[i * 2];
    if (at >= 0 && at < now_ms) {
      at = now_ms;
    }
    if (at == due_ms) {
      ties = ties.append(i);
    }
    i = i + 1;
  }
  return ties;
}

function __wait_word(drv: Sim, w: async.Wait, now_ms: i32, due_ms: i32, cancel_ms: i32): i32 {
  if (cancel_ms >= 0 && cancel_ms <= due_ms) {
    return async.cancelled();
  }
  let pairs: i32[] = __due_pairs(w, now_ms, due_ms);
  if (pairs.len() == 1) {
    return pairs[0];
  }
  if (pairs.len() > 1) {
    return pairs[__roll(drv, pairs.len())];
  }
  if (w.timeout_ms >= 0 && now_ms + w.timeout_ms == due_ms) {
    return 0 - 1;
  }
  return async.cancelled();
}

function __next_due[T](drv: Sim, out: async.TaskStatus[T][], cancel_at_ms: i32[]): i32 {
  let now_ms: i32 = __now_ms(drv);
  let best: i32 = 0;
  let ties: i32[] = [];
  let i: i32 = 0;
  while (i < out.len()) {
    match (out[i]) {
      Suspended(w) => {
        let due: i32 = __wait_due(w, now_ms, __cancel_ms(cancel_at_ms, i));
        if (ties.len() == 0 || due < best) {
          best = due;
          ties = [i];
        } else if (due == best) {
          ties = ties.append(i);
        }
      },
      Done(v) => {},
      Cancelled => {}
    }
    i = i + 1;
  }
  if (ties.len() == 0) {
    return 0 - 1;
  }
  if (ties.len() == 1) {
    return ties[0];
  }
  return ties[__roll(drv, ties.len())];
}

function __run_due[T](drv: Sim, t: async.Task[T], st: async.TaskStatus[T], cancel_ms: i32): async.TaskStatus[T] {
  match (st) {
    Suspended(w) => {
      let now_ms: i32 = __now_ms(drv);
      let due: i32 = __wait_due(w, now_ms, cancel_ms);
      if (due > now_ms) {
        drv.clock_ns.set(due as i64 * 1000000);
      }
      let word: i32 = __wait_word(drv, w, now_ms, due, cancel_ms);
      if (word == async.cancelled()) {
        return __settled(t, async.task_cancel(t));
      }
      return __settled(t, async.task_resume(t, word));
    },
    Done(v) => {
      return st;
    },
    Cancelled => {
      return st;
    }
  }
  return st;
}

function __pend[T](tok: i32, next: async.Future[T]): async.Future[T] {
  return Pending(tok, (woken: i32) => next);
}
```

### `10 pub function future_at[T](drv: Sim, at_ns: i64, v: T): async.Future[T]`

```fern
<<10 pub function future_at[T](drv: Sim, at_ns: i64, v: T): async.Future[T]>>=
pub function future_at[T](drv: Sim, at_ns: i64, v: T): async.Future[T] {
  let done: async.Future[T] = Ready(v);
  return __pend((at_ns / 1000000) as i32, done);
}

pub function future_chain[T](drv: Sim, at_ns: i64, step_ns: i64, resuspends: i32, v: T): async.Future[T] {
  let f: async.Future[T] = Ready(v);
  let i: i32 = resuspends;
  while (i >= 0) {
    f = __pend(((at_ns + i as i64 * step_ns) / 1000000) as i32, f);
    i = i - 1;
  }
  return f;
}
```
