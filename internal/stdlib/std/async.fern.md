# Futures and structured concurrency

This module combines futures with structured concurrency.

## Model

A future is ready with a value, pending on a wait, or cancelled. A wait is a set of (fd, interest) pairs and an optional deadline. The deadline is an instant on the clock of whatever waits. A task is a call chain that parks on a wait and continues when its scheduler resumes it. `spawn` makes a task into a future.

## Contracts

The combinators `gather`, `race` and `with_deadline` build futures from futures and read no clock. `wait` drives a future on the scheduler of the calling task. `block_on` drives it on the machine's reactor and clock. A `Driver` is the reactor that a scheduler such as `std/serve` watches descriptors with.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Future[T]>>

<<4 pub function event_room(max: i32): i32[]>>

<<5 pub struct RealDriver>>

<<6 pub function interest_bits(interest: i32): i32>>

<<7 function __eintr(): i32>>

<<8 pub function real_driver(): RealDriver>>

<<9 function __ring_new(entries: i32): i64>>

<<10 pub function (d: RealDriver) io_uring(): boolean>>

<<11 function __real_ctl(d: RealDriver, op: i32, fd: i32, arg: i32): i32>>

<<12 pub function (d: RealDriver) handle(): i32>>

<<13 impl Driver for RealDriver>>

<<14 future combinators>>

<<18 pub enum TaskStatus[T]>>

<<19 pub function task_new[T](entry: () => T): Task[T]>>

<<20 function run_task[T](t: Task[T], mode: i32): TaskStatus[T]>>

<<21 pub struct Call[A, B, T]>>

<<22 pub function call_start[A, B, T](f: (A, B)>>

<<23 function run_task_call[A, B, T](id: i32, f: (A, B)>>

<<24 waiting>>

<<30 function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32>>

<<31 function __timer_pollable(ns: i64): i32>>

<<32 kernel wait>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module combines futures with structured concurrency.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i32";
import "std/string";
import "std/time";
```

### `3 pub enum Future[T]`

```fern
<<3 pub enum Future[T]>>=
// A pending future resumed with Cancelled releases what it holds.
pub enum Future[T] {
  Ready(T),
  Pending(Wait, (Wake) => Future[T]),
  Cancelled,
}

// `set` holds (fd, interest) pairs. `until` is an instant on the clock of
// whatever waits for the set.
pub struct Wait { set: i32[], until: Option[Instant] }

// `Woke` holds the index of the pair that became ready.
pub enum Wake { Woke(i32), TimedOut, Cancelled }

pub trait Driver {
  function watch(self: Self, fd: i32, interest: i32): i32;
  function unwatch(self: Self, fd: i32): i32;
  function oneshot(self: Self): boolean {
    return false;
  }
  function keeps(self: Self): boolean {
    return false;
  }
  function wait_into(self: Self, own events: i32[], timeout: Option[Duration]): i32[];
  function wait(self: Self, max: i32, timeout: Option[Duration]): i32[] {
    let got = self.wait_into(event_room(max), timeout);
    return got.take(ready_pairs(got) * 2);
  }
  function watch_signal(self: Self, sig: i32): i32;
  function unwatch_signal(self: Self, sig: i32): i32;
  function watch_parent(self: Self): i32;
  function recv_into(self: Self, sock: Socket, buf: u8[]): i32 {
    return tcp_recv_into(sock, buf);
  }
  function send_zc(self: Self, sock: Socket, data: u8[], from: i32): i32 {
    return -95;
  }
  function zc_result(self: Self, slot: i32): i32 {
    return -2;
  }
  function zc_notified(self: Self, slot: i32): boolean {
    return true;
  }
  function zc_release(self: Self, slot: i32): i32 {
    return 0;
  }
  function close(self: Self): i32;
}
```

### `4 pub function event_room(max: i32): i32[]`

```fern
<<4 pub function event_room(max: i32): i32[]>>=
pub function event_room(max: i32): i32[] {
  let events: i32[] = [];
  while (events.len() < max * 2) {
    events = events.append(0);
  }
  return end_pairs(events, 0);
}

pub function ready_pairs(events: i32[]): i32 {
  let n = 0;
  while (n * 2 + 1 < events.len() && events[n * 2 + 1] >= 0) {
    n += 1;
  }
  return n;
}

pub function end_pairs(own events: i32[], n: i32): i32[] {
  if (n * 2 + 1 < events.len()) {
    return events.with(n * 2 + 1, -1);
  }
  return events;
}
```

### `5 pub struct RealDriver`

```fern
<<5 pub struct RealDriver>>=
pub struct RealDriver { reactor: Cell[i32], signals: Cell[string], ring: Cell[i64] }

pub const ONESHOT: i32 = 16;

pub const KEEP: i32 = 32;

pub const WATCHED: i32 = 64;
```

### `6 pub function interest_bits(interest: i32): i32`

```fern
<<6 pub function interest_bits(interest: i32): i32>>=
pub function interest_bits(interest: i32): i32 {
  return interest & 31;
}
```

### `7 function __eintr(): i32`

```fern
<<7 function __eintr(): i32>>=
function __eintr(): i32 {
  if (target_os() == "wasi") {
    return 27;
  }
  return 4;
}
```

### `8 pub function real_driver(): RealDriver`

```fern
<<8 pub function real_driver(): RealDriver>>=
pub function real_driver(): RealDriver {
  return RealDriver { reactor: cell_new(-1), signals: cell_new(""), ring: cell_new(-(1 as i64)) };
}

pub function real_driver_io_uring(): RealDriver {
  let d = real_driver();
  let ring = __ring_new(1024);
  if (ring >= 0 as i64) {
    d.ring.set(ring);
  }
  return d;
}
```

### `9 function __ring_new(entries: i32): i64`

Android's app filter forbids io_uring. `real_driver_io_uring()` uses the
reactor there without a forbidden syscall probe.

```fern
<<9 function __ring_new(entries: i32): i64>>=
function __ring_new(entries: i32): i64 {
  if (target_os() == "linux") {
    return ring_new(entries);
  }
  return (-38) as i64;
}

function __ring_ctl(ring: i64, op: i32, fd: i32, arg: i32): i32 {
  if (target_os() == "linux") {
    return ring_ctl(ring, op, fd, arg);
  }
  return -9;
}

function __ring_wait(ring: i64, events: i32[], timeout_ms: i32): i32 {
  if (target_os() == "linux") {
    return ring_wait(ring, events, timeout_ms);
  }
  return -9;
}

function __ring_recv(ring: i64, fd: i32, buf: u8[]): i32 {
  if (target_os() == "linux") {
    return ring_recv(ring, fd, buf);
  }
  return -9;
}

function __ring_send_zc(ring: i64, fd: i32, data: u8[], from: i32): i32 {
  if (target_os() == "linux") {
    return ring_send_zc(ring, fd, data, from);
  }
  return -9;
}
```

### `10 pub function (d: RealDriver) io_uring(): boolean`

```fern
<<10 pub function (d: RealDriver) io_uring(): boolean>>=
pub function (d: RealDriver) io_uring(): boolean {
  return d.ring.get() >= 0 as i64;
}
```

### `11 function __real_ctl(d: RealDriver, op: i32, fd: i32, arg: i32): i32`

```fern
<<11 function __real_ctl(d: RealDriver, op: i32, fd: i32, arg: i32): i32>>=
function __real_ctl(d: RealDriver, op: i32, fd: i32, arg: i32): i32 {
  let ring = d.ring.get();
  if (ring >= 0 as i64) {
    return __ring_ctl(ring, op, fd, arg);
  }
  let r = __real_reactor(d);
  if (r < 0) {
    return r;
  }
  return reactor_ctl(r, op, fd, arg);
}

function __real_wait(d: RealDriver, events: i32[], timeout_ms: i32): i32 {
  let ring = d.ring.get();
  if (ring >= 0 as i64) {
    return __ring_wait(ring, events, timeout_ms);
  }
  return reactor_wait(__real_reactor(d), events, timeout_ms);
}

function __real_signal_fd(d: RealDriver, sig: i32): i32 {
  for row in d.signals.get().split(";") {
    let parts = row.split(":");
    if (parts.len() == 2 && parts[0] == sig.to_string()) {
      match (i32.parse_i32_radix(parts[1], 10)) {
        Ok(v) => {
          return v;
        },
        Err(_) => {
          return -1;
        }
      }
    }
  }
  return -1;
}

function __real_reactor(d: RealDriver): i32 {
  let r = d.reactor.get();
  if (r >= 0) {
    return r;
  }
  r = reactor_new();
  if (r >= 0) {
    d.reactor.set(r);
  }
  return r;
}
```

### `12 pub function (d: RealDriver) handle(): i32`

```fern
<<12 pub function (d: RealDriver) handle(): i32>>=
pub function (d: RealDriver) handle(): i32 {
  let ring = d.ring.get();
  if (ring >= 0 as i64) {
    return __ring_ctl(ring, 7, 0, 0);
  }
  return __real_reactor(d);
}
```

### `13 impl Driver for RealDriver`

```fern
<<13 impl Driver for RealDriver>>=
impl Driver for RealDriver {
  function watch(self: RealDriver, fd: i32, interest: i32): i32 {
    return __real_ctl(self, 1, fd, interest);
  }
  function oneshot(self: RealDriver): boolean {
    return self.ring.get() < 0 as i64 && target_os() != "wasi" && target_os() != "wasi-http";
  }
  function keeps(self: RealDriver): boolean {
    return self.ring.get() < 0 as i64 && target_os() != "wasi" && target_os() != "wasi-http";
  }
  function unwatch(self: RealDriver, fd: i32): i32 {
    if (self.ring.get() < 0 as i64 && self.reactor.get() < 0) {
      return 0;
    }
    return __real_ctl(self, 2, fd, 0);
  }
  function wait_into(self: RealDriver, own events: i32[], timeout: Option[Duration]): i32[] {
    if (self.ring.get() < 0 as i64 && __real_reactor(self) < 0 || events.len() < 2) {
      return end_pairs(events, 0);
    }
    let timeout_ms = time.timeout_millis(timeout);
    let deadline = monotonic_ns() + timeout_ms as i64 * (1000000 as i64);
    let n = __real_wait(self, events, timeout_ms);
    while (n == 0 - __eintr()) {
      let wait_ms = timeout_ms;
      if (timeout_ms >= 0) {
        let ns = deadline - monotonic_ns();
        if (ns <= 0 as i64) {
          return end_pairs(events, 0);
        }
        wait_ms = ((ns + 999999 as i64) / (1000000 as i64)) as i32;
      }
      n = __real_wait(self, events, wait_ms);
    }
    if (n <= 0) {
      return end_pairs(events, 0);
    }
    return end_pairs(events, n);
  }
  function watch_signal(self: RealDriver, sig: i32): i32 {
    if (__real_signal_fd(self, sig) >= 0) {
      return 0;
    }
    let sfd = __real_ctl(self, 4, sig, 0);
    if (sfd < 0) {
      return sfd;
    }
    self.signals.set(self.signals.get() + sig.to_string() + ":" + sfd.to_string() + ";");
    return 0;
  }
  function unwatch_signal(self: RealDriver, sig: i32): i32 {
    let sfd = __real_signal_fd(self, sig);
    if (sfd < 0) {
      return 0;
    }
    let kept = "";
    for row in self.signals.get().split(";") {
      if (row.len() > 0 && !row.starts_with(sig.to_string() + ":")) {
        kept = kept + row + ";";
      }
    }
    self.signals.set(kept);
    return __real_ctl(self, 5, sig, sfd);
  }
  function watch_parent(self: RealDriver): i32 {
    let watched = self.watch_signal(15);
    if (watched < 0) {
      return watched;
    }
    return __real_ctl(self, 6, 0, 0);
  }
  function recv_into(self: RealDriver, sock: Socket, buf: u8[]): i32 {
    let ring = self.ring.get();
    if (ring >= 0 as i64) {
      return __ring_recv(ring, sock.fd, buf);
    }
    return tcp_recv_into(sock, buf);
  }
  function send_zc(self: RealDriver, sock: Socket, data: u8[], from: i32): i32 {
    let ring = self.ring.get();
    if (ring >= 0 as i64) {
      return __ring_send_zc(ring, sock.fd, data, from);
    }
    return -95;
  }
  function zc_result(self: RealDriver, slot: i32): i32 {
    return __ring_ctl(self.ring.get(), 8, slot, 0);
  }
  function zc_notified(self: RealDriver, slot: i32): boolean {
    return __ring_ctl(self.ring.get(), 9, slot, 0) != 0;
  }
  function zc_release(self: RealDriver, slot: i32): i32 {
    return __ring_ctl(self.ring.get(), 10, slot, 0);
  }
  function close(self: RealDriver): i32 {
    let ring = self.ring.get();
    self.signals.set("");
    if (ring >= 0 as i64) {
      self.ring.set(-(1 as i64));
      return __ring_ctl(ring, 3, 0, 0);
    }
    let r = self.reactor.get();
    if (r < 0) {
      return 0;
    }
    self.reactor.set(-1);
    return reactor_ctl(r, 3, 0, 0);
  }
}
```

### `14 future combinators`

A combinator builds a new future from its inputs and waits for nothing
itself. The union of the pending inputs' waits is the combined future's wait:
every (fd, interest) pair of each input, in input order, and the soonest of
their deadlines. A wake goes to the input that owns the pair, and a timeout
goes to each input whose deadline is that soonest one. So a combinator never
reads a clock, and the code that waits on its future supplies the time.

```fern
<<14 future combinators>>=
// Ready with every value, in input order, when all of `fs` are ready.
// Cancelled when one of them ends cancelled: the others are cancelled with it.
pub function gather[T](fs: Future[T][]): Future[T[]] {
  if (__any_cancelled(fs)) {
    let dropped = __cancel_all(fs);
    return Future.Cancelled;
  }
  if (__any_pending(fs)) {
    return Pending(__union(fs, None), (wake: Wake): Future[T[]] => __gather_woken(fs, wake));
  }
  let out: T[] = [];
  for f in fs {
    if let Ready(v) = f {
      out = out.append(v);
    }
  }
  return Ready(out);
}

function __gather_woken[T](fs: Future[T][], wake: Wake): Future[T[]] {
  if let Wake.Cancelled = wake {
    let dropped = __cancel_all(fs);
    return Future.Cancelled;
  }
  return gather(__step(fs, wake));
}

// Ready with the index and value of the first of `fs` to be ready; a tie goes
// to the lower index. The others are cancelled. Cancelled when every one of
// them ends cancelled, and so when `fs` is empty.
pub function race[T](fs: Future[T][]): Future[(i32, T)] {
  for i in 0..fs.len() {
    if let Ready(v) = fs[i] {
      let dropped = __cancel_all(fs);
      return Ready((i, v));
    }
  }
  if (!__any_pending(fs)) {
    return Future.Cancelled;
  }
  return Pending(__union(fs, None), (wake: Wake): Future[(i32, T)] => __race_woken(fs, wake));
}

function __race_woken[T](fs: Future[T][], wake: Wake): Future[(i32, T)] {
  if let Wake.Cancelled = wake {
    let dropped = __cancel_all(fs);
    return Future.Cancelled;
  }
  return race(__step(fs, wake));
}

// Ready with `Some(value)` for each of `fs` that is ready by `until`, and None
// for each that is cancelled or still pending then. The pending ones are
// cancelled at `until`. A deadline equal to an input's own deadline wins.
pub function with_deadline[T](until: Instant, fs: Future[T][]): Future[Option[T][]] {
  if (!__any_pending(fs)) {
    return Ready(__slots(fs));
  }
  return Pending(__union(fs, Some(until)), (wake: Wake): Future[Option[T][]] => __deadline_woken(until, fs, wake));
}

function __deadline_woken[T](until: Instant, fs: Future[T][], wake: Wake): Future[Option[T][]] {
  if let Wake.Cancelled = wake {
    let dropped = __cancel_all(fs);
    return Future.Cancelled;
  }
  if let TimedOut = wake {
    let passed = true;
    if let Some(due) = __soonest(fs) {
      passed = until.cmp(due) <= 0;
    }
    if (passed) {
      let slots = __slots(fs);
      let dropped = __cancel_all(fs);
      return Ready(slots);
    }
  }
  return with_deadline(until, __step(fs, wake));
}

function __any_pending[T](fs: Future[T][]): boolean {
  for f in fs {
    if let Pending(w, k) = f {
      return true;
    }
  }
  return false;
}

function __any_cancelled[T](fs: Future[T][]): boolean {
  for f in fs {
    if let Future.Cancelled = f {
      return true;
    }
  }
  return false;
}

function __slots[T](fs: Future[T][]): Option[T][] {
  let out: Option[T][] = [];
  for f in fs {
    match (f) {
      Ready(v) => {
        out = out.append(Some(v));
      },
      _ => {
        out = out.append(None);
      }
    }
  }
  return out;
}

// A future resumed with Cancelled releases what it holds. What it answers is
// not used.
function __cancel_all[T](fs: Future[T][]): i32 {
  let n = 0;
  for f in fs {
    if let Pending(w, k) = f {
      let gone = k(Wake.Cancelled);
      n += 1;
    }
  }
  return n;
}

function __sooner(a: Option[Instant], b: Option[Instant]): Option[Instant] {
  let Some(x) = a else {
    return b;
  };
  let Some(y) = b else {
    return a;
  };
  if (y.cmp(x) < 0) {
    return b;
  }
  return a;
}

function __soonest[T](fs: Future[T][]): Option[Instant] {
  let due: Option[Instant] = None;
  for f in fs {
    if let Pending(w, k) = f {
      due = __sooner(due, w.until);
    }
  }
  return due;
}

function __union[T](fs: Future[T][], until: Option[Instant]): Wait {
  let set: i32[] = [];
  let due = until;
  for f in fs {
    if let Pending(w, k) = f {
      for x in w.set {
        set = set.append(x);
      }
      due = __sooner(due, w.until);
    }
  }
  return Wait { set: set, until: due };
}

// Hands `wake`, a wake of `__union(fs, …)`, to the inputs it concerns.
function __step[T](fs: Future[T][], wake: Wake): Future[T][] {
  match (wake) {
    Woke(at) => {
      let base = 0;
      for i in 0..fs.len() {
        if let Pending(w, k) = fs[i] {
          let pairs = w.set.len() / 2;
          if (at < base + pairs) {
            return fs.with(i, k(Woke(at - base)));
          }
          base += pairs;
        }
      }
    },
    TimedOut => {
      let Some(due) = __soonest(fs) else {
        return fs;
      };
      let out = fs;
      for i in 0..fs.len() {
        if let Pending(w, k) = fs[i] {
          if let Some(at) = w.until {
            if (at.cmp(due) == 0) {
              out = out.with(i, k(TimedOut));
            }
          }
        }
      }
      return out;
    },
    Cancelled => {
      let dropped = __cancel_all(fs);
    }
  }
  return fs;
}
```

### `18 pub enum TaskStatus[T]`

A task is a call chain that can park. The scheduler that starts a task is the
one that resumes it, with the wake its wait came to. A task that finishes
releases its runtime record, so the scheduler only drops its handle.

```fern
<<18 pub enum TaskStatus[T]>>=
pub enum TaskStatus[T] { Done(T), Suspended(Wait) }

// Each copy of a task's handle shares one TaskShared, so a copy that is used
// after the task moved on reads the task's state, not a freed record.
// `step` counts the parks, and `wait` is what the task waits for now.
pub struct TaskShared { phase: i32, step: i32, wait: Wait }

const TASK_NEW: i32 = 0;

const TASK_RUNNING: i32 = 1;

const TASK_PARKED: i32 = 2;

const TASK_DONE: i32 = 3;

const TASK_CANCELLED: i32 = 4;

pub struct Task[T] { id: i32, entry: () => T, shared: Cell[TaskShared] }
```

### `19 pub function task_new[T](entry: () => T): Task[T]`

```fern
<<19 pub function task_new[T](entry: () => T): Task[T]>>=
pub function task_new[T](entry: () => T): Task[T] {
  unsafe {
    return Task { id: __task_new(), entry: entry, shared: cell_new(__task_shared(TASK_NEW)) };
  }
}

// The handle of a task that another scheduler parked under `id`.
pub function task_adopted[T](id: i32, entry: () => T): Task[T] {
  return Task { id: id, entry: entry, shared: cell_new(__task_shared(TASK_PARKED)) };
}

function __task_shared(phase: i32): TaskShared {
  return TaskShared { phase: phase, step: 0, wait: Wait { set: [], until: None } };
}

pub function task_start[T](t: Task[T]): TaskStatus[T] {
  __task_expect(t.shared.get().phase, TASK_NEW);
  return __task_ran(t, 0);
}

// Cancelled runs the task on to its end with every wait answering Cancelled
// and none parking, so a cancelled task always answers Done.
pub function task_resume[T](t: Task[T], wake: Wake): TaskStatus[T] {
  __task_expect(t.shared.get().phase, TASK_PARKED);
  __task_wake(t.id, wake);
  return __task_ran(t, 2);
}

// Stops the program when a handle is used on a task in another phase: a
// task started two times, or a task or a future resumed when its task
// ended, was not started, or is running. A handler can reach this code, and
// a handler has no ambient log, so there is no message.
function __task_expect(phase: i32, want: i32): void {
  if (phase != want) {
    exit(134);
  }
}

function __task_ran[T](t: Task[T], mode: i32): TaskStatus[T] {
  let before = t.shared.get();
  t.shared.set(TaskShared { ...before, phase: TASK_RUNNING });
  let st = __settled(t.id, run_task(t, mode));
  match (st) {
    Done(v) => {
      t.shared.set(TaskShared { ...before, phase: TASK_DONE });
    },
    Suspended(w) => {
      t.shared.set(TaskShared { ...before, phase: TASK_PARKED, step: before.step + 1, wait: w });
    }
  }
  return st;
}

pub function in_task(): boolean {
  unsafe {
    return __task_cur() != 0;
  }
}

pub function parks(): boolean {
  match (task_start(task_new((): boolean => in_task()))) {
    Done(ran) => {
      return ran;
    },
    Suspended(w) => {
      return true;
    }
  }
  return false;
}

pub function task_cancelled(): boolean {
  unsafe {
    return __task_cancelled() != 0;
  }
}

// Starts `entry` as a task at once. The future is ready when the task
// finishes. Resumed with Cancelled, the task runs on to its end and the
// future ends cancelled.
pub function spawn[T](entry: () => T): Future[T] {
  let t = task_new(entry);
  return __task_future(t, task_start(t));
}

function __task_future[T](t: Task[T], st: TaskStatus[T]): Future[T] {
  match (st) {
    Done(v) => {
      return Ready(v);
    },
    Suspended(w) => {
      let step = t.shared.get().step;
      return Pending(w, (wake: Wake): Future[T] => __task_woken(t, step, wake));
    }
  }
  return Future.Cancelled;
}

// A copy of a future that is polled after the task parked again answers the
// task's present wait, and does not resume the task a second time. A copy
// that is polled after the task ended has no value to give: task_resume
// stops the program with a message.
function __task_woken[T](t: Task[T], step: i32, wake: Wake): Future[T] {
  let now = t.shared.get();
  if (now.phase == TASK_CANCELLED) {
    return Future.Cancelled;
  }
  if (now.phase == TASK_PARKED && now.step != step) {
    return Pending(now.wait, (later: Wake): Future[T] => __task_woken(t, now.step, later));
  }
  let st = task_resume(t, wake);
  if let Wake.Cancelled = wake {
    t.shared.set(TaskShared { ...t.shared.get(), phase: TASK_CANCELLED });
    return Future.Cancelled;
  }
  return __task_future(t, st);
}

function __task_wake(id: i32, wake: Wake): void {
  unsafe {
    match (wake) {
      Woke(at) => {
        __task_set_ready(id, at);
      },
      TimedOut => {
        __task_set_ready(id, -1);
      },
      Cancelled => {
        __task_set_cancelled(id);
      }
    }
  }
}

function __settled[T](id: i32, st: TaskStatus[T]): TaskStatus[T] {
  if let Done(v) = st {
    unsafe {
      __task_free(id);
    }
  }
  return st;
}
```

### `20 function run_task[T](t: Task[T], mode: i32): TaskStatus[T]`

A park keeps its deadline after the pairs of its set, as the deadline's
seconds and nanoseconds, and marks that with a timeout of 1. The runtime's
timeout slot holds 32-bit milliseconds, which cannot hold an instant.

```fern
<<20 function run_task[T](t: Task[T], mode: i32): TaskStatus[T]>>=
@noinline
function run_task[T](t: Task[T], mode: i32): TaskStatus[T] {
  unsafe {
    __task_enter(t.id, mode);
    let v = t.entry();
    let m = __task_leave();
    if (m == 1) {
      return Suspended(__parked(t.id));
    }
    return Done(v);
  }
}

function __parked(id: i32): Wait {
  unsafe {
    let set = __task_wait(id);
    let n = set.len();
    if (__task_timeout(id) < 0 || n < 2) {
      return Wait { set: set, until: None };
    }
    return Wait { set: set.take(n - 2), until: Some(Instant { sec: set[n - 2] as i64, nsec: set[n - 1] }) };
  }
}
```

### `21 pub struct Call[A, B, T]`

A call is a task over a two-argument function. A call that answers without a
wait costs no closure and no handle.

```fern
<<21 pub struct Call[A, B, T]>>=
pub struct Call[A, B, T] { id: i32, f: (A, B) => T, a: A, b: B }

pub enum CallStatus[A, B, T] { Returned(T), Parked(Call[A, B, T], Wait) }
```

### `22 pub function call_start[A, B, T](f: (A, B)`

```fern
<<22 pub function call_start[A, B, T](f: (A, B)>>=
pub function call_start[A, B, T](f: (A, B) => T, a: A, b: B): CallStatus[A, B, T] {
  unsafe {
    let id = __task_new();
    match (run_task_call(id, f, a, b, 0)) {
      Done(v) => {
        __task_free(id);
        return Returned(v);
      },
      Suspended(w) => {
        return Parked(Call { id: id, f: f, a: a, b: b }, w);
      }
    }
    return Parked(Call { id: id, f: f, a: a, b: b }, Wait { set: [], until: None });
  }
}

pub function call_resume[A, B, T](c: Call[A, B, T], wake: Wake): TaskStatus[T] {
  __task_wake(c.id, wake);
  return __settled(c.id, run_task_call(c.id, c.f, c.a, c.b, 2));
}
```

### `23 function run_task_call[A, B, T](id: i32, f: (A, B)`

```fern
<<23 function run_task_call[A, B, T](id: i32, f: (A, B)>>=
@noinline
function run_task_call[A, B, T](id: i32, f: (A, B) => T, a: A, b: B, mode: i32): TaskStatus[T] {
  unsafe {
    __task_enter(id, mode);
    let v = f(a, b);
    let m = __task_leave();
    if (m == 1) {
      return Suspended(__parked(id));
    }
    return Done(v);
  }
}
```

### `24 waiting`

`wait` and `block_on` drive a future to its end. `wait` parks the calling task
on whatever schedules it: the serve loop, `sim.run_tasks`, or a combinator's
own wait. It reaches no clock and no reactor, so a handler can call it. With
no task current it has nothing to wait on. `block_on` waits on the machine's
reactor and clock when no task is current, and parks as `wait` does inside
one.

```fern
<<24 waiting>>=
// None when the calling task is cancelled, and when no task is current and
// `f` must wait. `f` is cancelled in both cases.
pub function wait[T](f: Future[T]): Option[T] {
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
        let wake = __park(w.set, w.until);
        cur = k(wake);
        if let Wake.Cancelled = wake {
          return None;
        }
      }
    }
  }
  return None;
}

// None when the calling task is cancelled, which `f` then is too.
pub function block_on[T](f: Future[T]): Option[T] {
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
        let wake = wait_any(w.set, w.until);
        cur = k(wake);
        if let Wake.Cancelled = wake {
          return None;
        }
      }
    }
  }
  return None;
}

// Waits for any (fd, interest) pair of `set` to become ready (1 readable, 2
// writable), or for the clock to reach `until`. Inside a task the task parks
// and its scheduler's clock decides `until`. Outside one the machine's
// monotonic clock does, and nothing answers Cancelled. On a kernel target the
// pairs name IRQ lines, and the machine's wait is the idle loop of chunk 32.
pub function wait_any(set: i32[], until: Option[Instant]): Wake {
  if (in_task()) {
    return __park(set, until);
  }
  if (target_os() == "kernel") {
    return __irq_wait_any(set, until);
  } else {
    return __host_wait_any(set, until);
  }
}

function __host_wait_any(set: i32[], until: Option[Instant]): Wake {
  let timeout_ms = -1;
  let left: i64 = 0;
  if let Some(at) = until {
    left = time.instant_nanos(at) - monotonic_ns();
    timeout_ms = __ms_until(left);
  }
  let woke = __block(set, timeout_ms);
  if (woke >= 0) {
    return Woke(woke);
  }
  // A wait has 2^31-1 ms at most. A deadline past that waits again.
  if (left > 2147483647 as i64 * 1000000) {
    return __host_wait_any(set, until);
  }
  return TimedOut;
}

function __park(set: i32[], until: Option[Instant]): Wake {
  unsafe {
    if (__task_cur() == 0) {
      return Wake.Cancelled;
    }
    let stored = set;
    let marked: i32 = 0 - 1;
    if let Some(at) = until {
      stored = set.append(__sec_word(at.sec)).append(at.nsec);
      marked = 1;
    }
    let word = __task_park(stored, marked);
    if (word >= 0) {
      return Woke(word);
    }
    if (word == 0 - 2) {
      return Wake.Cancelled;
    }
    return TimedOut;
  }
}

// The seconds of a deadline as one word of a wait set. An instant past the
// range of the word is the last instant of the range, which no clock reaches.
function __sec_word(sec: i64): i32 {
  if (sec > 2147483647) {
    return 2147483647;
  }
  return sec as i32;
}

// `ns` in whole milliseconds for a wait: a partial millisecond rounds up, and a
// deadline already past waits for nothing.
function __ms_until(ns: i64): i32 {
  if (ns <= 0) {
    return 0;
  }
  let ms: i64 = (ns + 999999) / 1000000;
  if (ms > 2147483647) {
    return 2147483647;
  }
  return ms as i32;
}

function __block(set: i32[], timeout_ms: i32): i32 {
  let n = set.len() / 2;
  if (n <= 0) {
    if (timeout_ms > 0) {
      sleep_ms(timeout_ms as i64);
    }
    return -1;
  }
  for i in 0..n {
    if (interest_bits(set[i * 2 + 1]) != 1) {
      return __wait_watched(set, n, timeout_ms);
    }
  }
  return __wait_readable(set, n, timeout_ms);
}
```

### `30 function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32`

```fern
<<30 function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32>>=
function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32 {
  let toks: i32[] = [];
  let i = 0;
  while (i < n) {
    toks = toks.append(tcp_pollable(set[i * 2]));
    i += 1;
  }
  let timer: i32 = 0 - 1;
  if (timeout_ms >= 0) {
    timer = __timer_pollable(timeout_ms as i64 * 1000000i64);
    toks = toks.append(timer);
  }
  let ready = poll(toks, timeout_ms);
  i = 0;
  while (i < n) {
    let dropped = __drop_pollable(toks[i]);
    i += 1;
  }
  if (timer >= 0) {
    let dtimer = __drop_pollable(timer);
  }
  if (ready >= 0 && ready < n) {
    return ready;
  }
  return -1;
}

function __wait_watched(set: i32[], n: i32, timeout_ms: i32): i32 {
  let drv = real_driver();
  let i = 0;
  while (i < n) {
    let watched = drv.watch(set[i * 2], interest_bits(set[i * 2 + 1]));
    i += 1;
  }
  let woke = drv.wait(n, time.millis_timeout(timeout_ms));
  i = 0;
  while (i < n) {
    let unwatched = drv.unwatch(set[i * 2]);
    i += 1;
  }
  let closed = drv.close();
  let hit = -1;
  let k = 0;
  while (k + 1 < woke.len()) {
    i = 0;
    while (i < n) {
      if (set[i * 2] == woke[k] && (hit < 0 || i < hit)) {
        hit = i;
      }
      i += 1;
    }
    k += 2;
  }
  return hit;
}
```

### `31 function __timer_pollable(ns: i64): i32`

A readiness token is a wasi:io pollable on the two wasi worlds and a
descriptor everywhere else. Off wasi a deadline is the wait's own timeout and
a token has no lifetime to end, so these answer as no timer and a drop that
had nothing to do, and the pollable builtins are not in the program.

```fern
<<31 function __timer_pollable(ns: i64): i32>>=
function __timer_pollable(ns: i64): i32 {
  if (target_os() == "wasi" || target_os() == "wasi-http") {
    return wasm_timer_pollable(ns);
  }
  return 0 - 1;
}

function __drop_pollable(tok: i32): i32 {
  if (target_os() == "wasi" || target_os() == "wasi-http") {
    return wasm_pollable_drop(tok);
  }
  return 0;
}
```

### `32 kernel wait`

A kernel has no descriptors, so on a kernel target a wait's pairs name IRQ
lines, with interest 1. The machine's wait is the idle loop that
`docs/BARE-METAL-PLAN.md` decides. The IRQ region that `__irq_pending()` names
holds one pending word for each 32 lines. The top half of an interrupt handler
sets the bit of its line there.

This loop is the only code that clears a bit, and it runs with IRQs masked.
Thus the two read-modify-write sequences never interleave on one core.
`__irq_wait()` lets one interrupt through, and it returns with IRQs masked
again. Thus the loop sees the bit that the top half set. A kernel has no clock,
so a deadline stops the program.

```fern
<<32 kernel wait>>=
// The lines of the IRQ region.
pub const IRQ_LINES: i32 = 1024;

// A future that is ready with `id` once IRQ line `id` has fired since the last
// wait on it. The top half of the handler sets the bit of the line in the IRQ
// region, with __irq_pending() and __mmio_store32. Only a kernel target grants
// the `privileged` capability that a wait for an interrupt needs, so E066
// refuses this future on every other target.
pub function irq(id: i32): Future[i32] {
  if (target_os() != "kernel") {
    let region = __irq_pending();
  }
  if (id < 0 || id >= IRQ_LINES) {
    eprint("std/async: IRQ line " + id.to_string() + " is outside the IRQ region of " + IRQ_LINES.to_string() + " lines\n");
    exit(134);
  }
  return Pending(Wait { set: [id, 1], until: None }, (wake: Wake): Future[i32] => __irq_woken(id, wake));
}

function __irq_woken(id: i32, wake: Wake): Future[i32] {
  match (wake) {
    Woke(at) => {
      return Ready(id);
    },
    TimedOut => {
      return irq(id);
    },
    Cancelled => {
      return Future.Cancelled;
    }
  }
  return Future.Cancelled;
}

// Woke(i) for the first pair of `set` whose line has fired, with its bit
// cleared. With no pair the wait ends at once, as the hosted wait does.
function __irq_wait_any(set: i32[], until: Option[Instant]): Wake {
  if let Some(at) = until {
    eprint("std/async: a kernel has no clock, so a wait cannot have a deadline\n");
    exit(134);
  }
  let n = set.len() / 2;
  if (n <= 0) {
    return TimedOut;
  }
  __irq_mask();
  while (true) {
    let hit = __irq_take(set, n);
    if (hit >= 0) {
      __irq_unmask();
      return Woke(hit);
    }
    __irq_wait();
  }
  return TimedOut;
}

// The index of the first pair of `set` whose line has fired, or -1. Clears the
// bit it reports. IRQs are masked here.
function __irq_take(set: i32[], n: i32): i32 {
  unsafe {
    let region = __irq_pending();
    let i = 0;
    while (i < n) {
      let id = set[i * 2];
      let word = region + (id / 32 * 4) as usize;
      let bit = 1 << id % 32;
      let bits = __mmio_load32(word);
      if ((bits & bit) != 0) {
        __mmio_store32(word, bits ^ bit);
        return i;
      }
      i += 1;
    }
  }
  return -1;
}
```
