# Futures and structured concurrency

This module combines futures with structured concurrency.

## Model

A future either contains a result or describes a wait. Drivers connect those waits to readiness events. Task status distinguishes completion, suspension, and cancellation.

## Contracts

Task and call handles carry explicit resume, cancel, and free operations. The gather, race, and deadline helpers coordinate groups of tasks through the same driver model.

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

<<14 function __all_ready[T](fs: Future[T][]): boolean>>

<<15 pub function gather[T](fs_in: Future[T][], on_incomplete: T): T[]>>

<<16 function __drop_losers[D: Driver, T](drv: D, fs: Future[T][], winner: i32): i32>>

<<17 pub function race[T](fs_in: Future[T][], none_val: T): (i32, T)>>

<<18 pub enum TaskStatus[T]>>

<<19 pub function cancelled(): i32>>

<<20 function run_task[T](t: Task[T], mode: i32): TaskStatus[T]>>

<<21 pub struct Call[A, B, T]>>

<<22 pub function call_start[A, B, T](f: (A, B)>>

<<23 function run_task_call[A, B, T](id: i32, f: (A, B)>>

<<24 pub function park_tokens(toks: i32[], timeout_ms: i32): i32>>

<<25 struct __Child[T]>>

<<26 function __no_due(): i64>>

<<27 struct __Union>>

<<28 function __children_union[T](kids: __Child[T][]): __Union>>

<<29 pub function gather_tasks[T](entries: (()>>

<<30 function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32>>

<<31 function __timer_pollable(ns: i64): i32>>
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
import "core/int";
import "std/string";
import "std/time";
```

### `3 pub enum Future[T]`

```fern
<<3 pub enum Future[T]>>=
pub enum Future[T] {
  Ready(T),
  Pending(i32, (i32) => Future[T]),
}

pub trait Driver {
  function poll_ready(self: Self, toks: i32[], timeout_ms: i32): i32;
  function now_ns(self: Self): i64;
  function timer(self: Self, ns: i64): i32;
  function drop_token(self: Self, tok: i32): i32;
  function watch(self: Self, fd: i32, interest: i32): i32;
  function unwatch(self: Self, fd: i32): i32;
  function oneshot(self: Self): boolean {
    return false;
  }
  function keeps(self: Self): boolean {
    return false;
  }
  function wait_into(self: Self, own events: i32[], timeout_ms: i32): i32[];
  function wait(self: Self, max: i32, timeout_ms: i32): i32[] {
    let got: i32[] = self.wait_into(event_room(max), timeout_ms);
    return got.take(ready_pairs(got) * 2);
  }
  function watch_signal(self: Self, sig: i32): i32;
  function unwatch_signal(self: Self, sig: i32): i32;
  function watch_parent(self: Self): i32;
  function recv_into(self: Self, fd: i32, buf: u8[]): i32 {
    return tcp_recv_into(fd, buf);
  }
  function send_zc(self: Self, fd: i32, data: u8[], from: i32): i32 {
    return 0 - 95;
  }
  function zc_result(self: Self, slot: i32): i32 {
    return 0 - 2;
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
  let n: i32 = 0;
  while (n * 2 + 1 < events.len() && events[n * 2 + 1] >= 0) {
    n = n + 1;
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
  let d: RealDriver = real_driver();
  let ring: i64 = __ring_new(1024);
  if (ring >= 0 as i64) {
    d.ring.set(ring);
  }
  return d;
}
```

### `9 function __ring_new(entries: i32): i64`

```fern
<<9 function __ring_new(entries: i32): i64>>=
function __ring_new(entries: i32): i64 {
  if (target_os() == "linux" || target_os() == "android") {
    return ring_new(entries);
  }
  return (0 - 38) as i64;
}

function __ring_ctl(ring: i64, op: i32, fd: i32, arg: i32): i32 {
  if (target_os() == "linux" || target_os() == "android") {
    return ring_ctl(ring, op, fd, arg);
  }
  return 0 - 9;
}

function __ring_wait(ring: i64, events: i32[], timeout_ms: i32): i32 {
  if (target_os() == "linux" || target_os() == "android") {
    return ring_wait(ring, events, timeout_ms);
  }
  return 0 - 9;
}

function __ring_recv(ring: i64, fd: i32, buf: u8[]): i32 {
  if (target_os() == "linux" || target_os() == "android") {
    return ring_recv(ring, fd, buf);
  }
  return 0 - 9;
}

function __ring_send_zc(ring: i64, fd: i32, data: u8[], from: i32): i32 {
  if (target_os() == "linux" || target_os() == "android") {
    return ring_send_zc(ring, fd, data, from);
  }
  return 0 - 9;
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
  let ring: i64 = d.ring.get();
  if (ring >= 0 as i64) {
    return __ring_ctl(ring, op, fd, arg);
  }
  let r: i32 = __real_reactor(d);
  if (r < 0) {
    return r;
  }
  return reactor_ctl(r, op, fd, arg);
}

function __real_wait(d: RealDriver, events: i32[], timeout_ms: i32): i32 {
  let ring: i64 = d.ring.get();
  if (ring >= 0 as i64) {
    return __ring_wait(ring, events, timeout_ms);
  }
  return reactor_wait(__real_reactor(d), events, timeout_ms);
}

function __real_signal_fd(d: RealDriver, sig: i32): i32 {
  for row in d.signals.get().split(";") {
    let parts: string[] = row.split(":");
    if (parts.len() == 2 && parts[0] == int.int_to_string(sig)) {
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
  let r: i32 = d.reactor.get();
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
  let ring: i64 = d.ring.get();
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
  function poll_ready(self: RealDriver, toks: i32[], timeout_ms: i32): i32 {
    if (__task_cur() != 0) {
      return park_tokens(toks, timeout_ms);
    }
    return poll(toks, timeout_ms);
  }
  function now_ns(self: RealDriver): i64 {
    return monotonic_ns();
  }
  function timer(self: RealDriver, ns: i64): i32 {
    return __timer_pollable(ns);
  }
  function drop_token(self: RealDriver, tok: i32): i32 {
    return drop_pollable(tok);
  }
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
  function wait_into(self: RealDriver, own events: i32[], timeout_ms: i32): i32[] {
    if (self.ring.get() < 0 as i64 && __real_reactor(self) < 0 || events.len() < 2) {
      return end_pairs(events, 0);
    }
    let deadline: i64 = monotonic_ns() + timeout_ms as i64 * (1000000 as i64);
    let n: i32 = __real_wait(self, events, timeout_ms);
    while (n == 0 - __eintr()) {
      let wait_ms: i32 = timeout_ms;
      if (timeout_ms >= 0) {
        let ns: i64 = deadline - monotonic_ns();
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
    let sfd: i32 = __real_ctl(self, 4, sig, 0);
    if (sfd < 0) {
      return sfd;
    }
    self.signals.set(self.signals.get() + int.int_to_string(sig) + ":" + int.int_to_string(sfd) + ";");
    return 0;
  }
  function unwatch_signal(self: RealDriver, sig: i32): i32 {
    let sfd: i32 = __real_signal_fd(self, sig);
    if (sfd < 0) {
      return 0;
    }
    let kept: string = "";
    for row in self.signals.get().split(";") {
      if (row.len() > 0 && !row.starts_with(int.int_to_string(sig) + ":")) {
        kept = kept + row + ";";
      }
    }
    self.signals.set(kept);
    return __real_ctl(self, 5, sig, sfd);
  }
  function watch_parent(self: RealDriver): i32 {
    let watched: i32 = self.watch_signal(15);
    if (watched < 0) {
      return watched;
    }
    return __real_ctl(self, 6, 0, 0);
  }
  function recv_into(self: RealDriver, fd: i32, buf: u8[]): i32 {
    let ring: i64 = self.ring.get();
    if (ring >= 0 as i64) {
      return __ring_recv(ring, fd, buf);
    }
    return tcp_recv_into(fd, buf);
  }
  function send_zc(self: RealDriver, fd: i32, data: u8[], from: i32): i32 {
    let ring: i64 = self.ring.get();
    if (ring >= 0 as i64) {
      return __ring_send_zc(ring, fd, data, from);
    }
    return 0 - 95;
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
    let ring: i64 = self.ring.get();
    self.signals.set("");
    if (ring >= 0 as i64) {
      self.ring.set(-(1 as i64));
      return __ring_ctl(ring, 3, 0, 0);
    }
    let r: i32 = self.reactor.get();
    if (r < 0) {
      return 0;
    }
    self.reactor.set(-1);
    return reactor_ctl(r, 3, 0, 0);
  }
}
```

### `14 function __all_ready[T](fs: Future[T][]): boolean`

```fern
<<14 function __all_ready[T](fs: Future[T][]): boolean>>=
function __all_ready[T](fs: Future[T][]): boolean {
  let i: i32 = 0;
  while (i < fs.len()) {
    match (fs[i]) {
      Ready(v) => {},
      Pending(fd, c) => {
        return false;
      }
    }
    i = i + 1;
  }
  return true;
}
```

### `15 pub function gather[T](fs_in: Future[T][], on_incomplete: T): T[]`

```fern
<<15 pub function gather[T](fs_in: Future[T][], on_incomplete: T): T[]>>=
pub function gather[T](fs_in: Future[T][], on_incomplete: T): T[] {
  return gather_on(real_driver(), fs_in, on_incomplete);
}

pub function gather_on[D: Driver, T](drv: D, fs_in: Future[T][], on_incomplete: T): T[] {
  let fs: Future[T][] = fs_in;
  while (true) {
    if (__all_ready(fs)) {
      break;
    }
    let fds: i32[] = [];
    let idxs: i32[] = [];
    let i: i32 = 0;
    while (i < fs.len()) {
      match (fs[i]) {
        Ready(v) => {},
        Pending(fd, c) => {
          fds = fds.append(fd);
          idxs = idxs.append(i);
        }
      }
      i = i + 1;
    }
    let ready: i32 = drv.poll_ready(fds, -1);
    if (ready < 0) {
      let dl: i32 = __drop_losers(drv, fs, -1);
      break;
    }
    let ti: i32 = idxs[ready];
    match (fs[ti]) {
      Ready(v) => {},
      Pending(fd, resume) => {
        fs = fs.with(ti, resume(fd));
      }
    }
  }
  let out: T[] = [];
  let k: i32 = 0;
  while (k < fs.len()) {
    match (fs[k]) {
      Ready(v) => {
        out = out.append(v);
      },
      Pending(fd, c) => {
        out = out.append(on_incomplete);
      }
    }
    k = k + 1;
  }
  return out;
}
```

### `16 function __drop_losers[D: Driver, T](drv: D, fs: Future[T][], winner: i32): i32`

```fern
<<16 function __drop_losers[D: Driver, T](drv: D, fs: Future[T][], winner: i32): i32>>=
function __drop_losers[D: Driver, T](drv: D, fs: Future[T][], winner: i32): i32 {
  let i: i32 = 0;
  while (i < fs.len()) {
    if (i != winner) {
      match (fs[i]) {
        Ready(v) => {},
        Pending(tok, c) => {
          let d: i32 = drv.drop_token(tok);
          match (c(cancelled())) {
            Ready(v) => {},
            Pending(rest, rc) => {
              let dr: i32 = drv.drop_token(rest);
            }
          }
        }
      }
    }
    i = i + 1;
  }
  return 0;
}
```

### `17 pub function race[T](fs_in: Future[T][], none_val: T): (i32, T)`

```fern
<<17 pub function race[T](fs_in: Future[T][], none_val: T): (i32, T)>>=
pub function race[T](fs_in: Future[T][], none_val: T): (i32, T) {
  return race_on(real_driver(), fs_in, none_val);
}

pub function race_on[D: Driver, T](drv: D, fs_in: Future[T][], none_val: T): (i32, T) {
  let fs: Future[T][] = fs_in;
  let j: i32 = 0;
  while (j < fs.len()) {
    match (fs[j]) {
      Ready(v) => {
        let d0: i32 = __drop_losers(drv, fs, j);
        return (j, v);
      },
      Pending(fd, c) => {}
    }
    j = j + 1;
  }
  while (true) {
    let fds: i32[] = [];
    let idxs: i32[] = [];
    let i: i32 = 0;
    while (i < fs.len()) {
      match (fs[i]) {
        Ready(v) => {},
        Pending(fd, c) => {
          fds = fds.append(fd);
          idxs = idxs.append(i);
        }
      }
      i = i + 1;
    }
    if (fds.len() == 0) {
      return (-1, none_val);
    }
    let ready: i32 = drv.poll_ready(fds, -1);
    if (ready < 0) {
      let de: i32 = __drop_losers(drv, fs, -1);
      return (-1, none_val);
    }
    let ti: i32 = idxs[ready];
    match (fs[ti]) {
      Ready(v) => {
        return (ti, v);
      },
      Pending(fd, resume) => {
        let nx: Future[T] = resume(fd);
        match (nx) {
          Ready(v) => {
            let dw: i32 = __drop_losers(drv, fs, ti);
            return (ti, v);
          },
          Pending(nfd, nc) => {
            fs = fs.with(ti, nx);
          }
        }
      }
    }
  }
  return (-1, none_val);
}

pub function with_deadline[T](deadline: Duration, fs_in: Future[T][]): Option[T][] {
  return with_deadline_on(real_driver(), deadline, fs_in);
}

pub function with_deadline_on[D: Driver, T](drv: D, deadline: Duration, fs_in: Future[T][]): Option[T][] {
  let fs: Future[T][] = fs_in;
  let start_ns: i64 = drv.now_ns();
  let ns_per_ms: i64 = 1000000;
  let deadline_ns: i64 = deadline.as_nanos();
  while (true) {
    if (__all_ready(fs)) {
      break;
    }
    let fds: i32[] = [];
    let idxs: i32[] = [];
    let i: i32 = 0;
    while (i < fs.len()) {
      match (fs[i]) {
        Ready(v) => {},
        Pending(fd, c) => {
          fds = fds.append(fd);
          idxs = idxs.append(i);
        }
      }
      i = i + 1;
    }
    let remaining_ns: i64 = deadline_ns - (drv.now_ns() - start_ns);
    if (remaining_ns <= 0) {
      break;
    }
    let remaining_ms: i32 = (remaining_ns / ns_per_ms) as i32;
    if (remaining_ms <= 0) {
      remaining_ms = 1;
    }
    let nfds: i32 = fds.len();
    let timer: i32 = drv.timer(remaining_ns);
    let pollset: i32[] = fds.append(timer);
    let ready: i32 = drv.poll_ready(pollset, remaining_ms);
    if (ready < 0) {
      let dpe: i32 = drv.drop_token(timer);
      break;
    }
    if (ready == nfds) {
      let dpt: i32 = drv.drop_token(timer);
      break;
    }
    let dpr: i32 = drv.drop_token(timer);
    let ti: i32 = idxs[ready];
    match (fs[ti]) {
      Ready(v) => {},
      Pending(fd, resume) => {
        fs = fs.with(ti, resume(fd));
      }
    }
  }
  let dd: i32 = __drop_losers(drv, fs, -1);
  let out: Option[T][] = [];
  let k: i32 = 0;
  while (k < fs.len()) {
    match (fs[k]) {
      Ready(v) => {
        out = out.append(Some(v));
      },
      Pending(fd, c) => {
        out = out.append(None);
      }
    }
    k = k + 1;
  }
  return out;
}
```

### `18 pub enum TaskStatus[T]`

```fern
<<18 pub enum TaskStatus[T]>>=
pub enum TaskStatus[T] { Done(T), Suspended(Wait), Cancelled }

pub struct Wait { task: i32, set: i32[], timeout_ms: i32 }

pub struct Task[T] { id: i32, entry: () => T }
```

### `19 pub function cancelled(): i32`

```fern
<<19 pub function cancelled(): i32>>=
pub function cancelled(): i32 {
  return 0 - 2;
}

pub function task_new[T](entry: () => T): Task[T] {
  return Task { id: __task_new(), entry: entry };
}

pub function task_start[T](t: Task[T]): TaskStatus[T] {
  return run_task(t, 0);
}

pub function in_task(): boolean {
  return __task_cur() != 0;
}

pub function parks(): boolean {
  let t: Task[boolean] = task_new((): boolean => __task_cur() != 0);
  match (task_start(t)) {
    Done(ran) => {
      task_free(t);
      return ran;
    },
    _ => {
      task_free(t);
      return true;
    }
  }
  return false;
}

pub function task_resume[T](t: Task[T], ready: i32): TaskStatus[T] {
  __task_set_ready(t.id, ready);
  return run_task(t, 2);
}

pub function task_cancel[T](t: Task[T]): TaskStatus[T] {
  __task_set_cancelled(t.id);
  let st: TaskStatus[T] = run_task(t, 2);
  return Cancelled;
}

pub function task_cancelled(): boolean {
  return __task_cancelled() != 0;
}

pub function task_free[T](t: Task[T]): void {
  __task_free(t.id);
}
```

### `20 function run_task[T](t: Task[T], mode: i32): TaskStatus[T]`

```fern
<<20 function run_task[T](t: Task[T], mode: i32): TaskStatus[T]>>=
@noinline
function run_task[T](t: Task[T], mode: i32): TaskStatus[T] {
  __task_enter(t.id, mode);
  let v: T = t.entry();
  let m: i32 = __task_leave();
  if (m == 1) {
    return Suspended(Wait { task: t.id, set: __task_wait(t.id), timeout_ms: __task_timeout(t.id) });
  }
  return Done(v);
}
```

### `21 pub struct Call[A, B, T]`

```fern
<<21 pub struct Call[A, B, T]>>=
pub struct Call[A, B, T] { id: i32, f: (A, B) => T, a: A, b: B }
```

### `22 pub function call_start[A, B, T](f: (A, B)`

```fern
<<22 pub function call_start[A, B, T](f: (A, B)>>=
pub function call_start[A, B, T](f: (A, B) => T, a: A, b: B): TaskStatus[T] {
  let id: i32 = __task_new();
  match (run_task_call(id, f, a, b, 0)) {
    Done(v) => {
      __task_free(id);
      return Done(v);
    },
    Suspended(w) => {
      return Suspended(w);
    },
    Cancelled => {
      __task_free(id);
    }
  }
  return Cancelled;
}

pub function call_of[A, B, T](w: Wait, f: (A, B) => T, a: A, b: B): Call[A, B, T] {
  return Call { id: w.task, f: f, a: a, b: b };
}

pub function call_resume[A, B, T](c: Call[A, B, T], ready: i32): TaskStatus[T] {
  __task_set_ready(c.id, ready);
  return run_task_call(c.id, c.f, c.a, c.b, 2);
}

pub function call_cancel[A, B, T](c: Call[A, B, T]): TaskStatus[T] {
  __task_set_cancelled(c.id);
  let st: TaskStatus[T] = run_task_call(c.id, c.f, c.a, c.b, 2);
  return Cancelled;
}

pub function call_free[A, B, T](c: Call[A, B, T]): void {
  __task_free(c.id);
}
```

### `23 function run_task_call[A, B, T](id: i32, f: (A, B)`

```fern
<<23 function run_task_call[A, B, T](id: i32, f: (A, B)>>=
@noinline
function run_task_call[A, B, T](id: i32, f: (A, B) => T, a: A, b: B, mode: i32): TaskStatus[T] {
  __task_enter(id, mode);
  let v: T = f(a, b);
  let m: i32 = __task_leave();
  if (m == 1) {
    return Suspended(Wait { task: id, set: __task_wait(id), timeout_ms: __task_timeout(id) });
  }
  return Done(v);
}
```

### `24 pub function park_tokens(toks: i32[], timeout_ms: i32): i32`

```fern
<<24 pub function park_tokens(toks: i32[], timeout_ms: i32): i32>>=
pub function park_tokens(toks: i32[], timeout_ms: i32): i32 {
  let set: i32[] = [];
  let idxs: i32[] = [];
  let i: i32 = 0;
  while (i < toks.len()) {
    if (toks[i] >= 0) {
      set = set.append(toks[i]);
      set = set.append(1);
      idxs = idxs.append(i);
    }
    i = i + 1;
  }
  let r: i32 = __task_park(set, timeout_ms);
  if (r < 0) {
    return r;
  }
  return idxs[r];
}
```

### `25 struct __Child[T]`

```fern
<<25 struct __Child[T]>>=
struct __Child[T] { task: Task[T], st: TaskStatus[T], due: i64 }
```

### `26 function __no_due(): i64`

```fern
<<26 function __no_due(): i64>>=
function __no_due(): i64 {
  let g: i64 = 1000000000 as i64;
  return g * g * 9;
}

function __child_due[T](st: TaskStatus[T], now: i64): i64 {
  match (st) {
    Suspended(w) => {
      if (w.timeout_ms < 0) {
        return __no_due();
      }
      return now + w.timeout_ms as i64 * (1000000 as i64);
    },
    Done(v) => {
      return __no_due();
    },
    Cancelled => {
      return __no_due();
    }
  }
}

function __children_start[T](entries: (() => T)[]): __Child[T][] {
  let kids: __Child[T][] = [];
  for e in entries {
    let t: Task[T] = task_new(e);
    let st: TaskStatus[T] = task_start(t);
    kids = kids.append(__Child { task: t, st: st, due: __child_due(st, monotonic_ns()) });
  }
  return kids;
}

function __children_parked[T](kids: __Child[T][]): boolean {
  for k in kids {
    match (k.st) {
      Suspended(w) => {
        return true;
      },
      Done(v) => {},
      Cancelled => {}
    }
  }
  return false;
}

function __children_done[T](kids: __Child[T][]): i32 {
  let i: i32 = 0;
  while (i < kids.len()) {
    let k: __Child[T] = kids[i];
    match (k.st) {
      Done(v) => {
        return i;
      },
      Suspended(w) => {},
      Cancelled => {}
    }
    i = i + 1;
  }
  return 0 - 1;
}

function __children_due[T](kids: __Child[T][]): i64 {
  let due: i64 = __no_due();
  for k in kids {
    if (k.due < due) {
      due = k.due;
    }
  }
  return due;
}

function __ms_until(due: i64, now: i64): i32 {
  if (due >= __no_due()) {
    return 0 - 1;
  }
  if (due <= now) {
    return 0;
  }
  let ms: i64 = (due - now + 999999 as i64) / (1000000 as i64);
  if (ms > 2147483647 as i64) {
    return 2147483647;
  }
  return ms as i32;
}

function __child_resume[T](kids: __Child[T][], i: i32, word: i32): __Child[T][] {
  let k: __Child[T] = kids[i];
  let st: TaskStatus[T] = task_resume(k.task, word);
  return kids.with(i, __Child { task: k.task, st: st, due: __child_due(st, monotonic_ns()) });
}

function __children_cancel[T](kids: __Child[T][]): __Child[T][] {
  let out: __Child[T][] = [];
  for k in kids {
    match (k.st) {
      Suspended(w) => {
        let c: TaskStatus[T] = task_cancel(k.task);
        out = out.append(__Child { task: k.task, st: c, due: __no_due() });
      },
      Done(v) => {
        out = out.append(k);
      },
      Cancelled => {
        out = out.append(k);
      }
    }
  }
  return out;
}

function __children_free[T](kids: __Child[T][]): i32 {
  for k in kids {
    task_free(k.task);
  }
  return 0;
}
```

### `27 struct __Union`

```fern
<<27 struct __Union>>=
struct __Union { set: i32[], owner: i32[], at: i32[] }
```

### `28 function __children_union[T](kids: __Child[T][]): __Union`

```fern
<<28 function __children_union[T](kids: __Child[T][]): __Union>>=
function __children_union[T](kids: __Child[T][]): __Union {
  let u: __Union = __Union { set: [], owner: [], at: [] };
  let i: i32 = 0;
  while (i < kids.len()) {
    let k: __Child[T] = kids[i];
    match (k.st) {
      Suspended(w) => {
        let j: i32 = 0;
        while (j + 1 < w.set.len()) {
          u = __Union { set: u.set.append(w.set[j]).append(w.set[j + 1]), owner: u.owner.append(i), at: u.at.append(j / 2) };
          j = j + 2;
        }
      },
      Done(v) => {},
      Cancelled => {}
    }
    i = i + 1;
  }
  return u;
}

function __children_expire[T](kids_in: __Child[T][], now: i64): __Child[T][] {
  let kids: __Child[T][] = kids_in;
  let i: i32 = 0;
  while (i < kids.len()) {
    if (kids[i].due <= now) {
      kids = __child_resume(kids, i, 0 - 1);
    }
    i = i + 1;
  }
  return kids;
}

function __children_drive[T](kids_in: __Child[T][], until_ns: i64, first: boolean): __Child[T][] {
  let kids: __Child[T][] = kids_in;
  while (__children_parked(kids) && !(first && __children_done(kids) >= 0)) {
    let now: i64 = monotonic_ns();
    let due: i64 = __children_due(kids);
    if (due > until_ns) {
      due = until_ns;
    }
    let u: __Union = __children_union(kids);
    let woke: i32 = wait_any(u.set, __ms_until(due, now));
    if (woke == cancelled()) {
      return __children_cancel(kids);
    }
    if (woke >= 0) {
      kids = __child_resume(kids, u.owner[woke], u.at[woke]);
    } else {
      now = monotonic_ns();
      if (now >= until_ns) {
        return __children_cancel(kids);
      }
      kids = __children_expire(kids, now);
    }
  }
  return kids;
}
```

### `29 pub function gather_tasks[T](entries: (()`

```fern
<<29 pub function gather_tasks[T](entries: (()>>=
pub function gather_tasks[T](entries: (() => T)[], on_incomplete: T): T[] {
  let kids: __Child[T][] = __children_drive(__children_start(entries), __no_due(), false);
  let out: T[] = [];
  for k in kids {
    match (k.st) {
      Done(v) => {
        out = out.append(v);
      },
      Suspended(w) => {
        out = out.append(on_incomplete);
      },
      Cancelled => {
        out = out.append(on_incomplete);
      }
    }
  }
  let f: i32 = __children_free(kids);
  return out;
}

pub function race_tasks[T](entries: (() => T)[], none_val: T): (i32, T) {
  let kids: __Child[T][] = __children_drive(__children_start(entries), __no_due(), true);
  let won: i32 = __children_done(kids);
  let v: T = none_val;
  if (won >= 0) {
    let k: __Child[T] = kids[won];
    match (k.st) {
      Done(x) => {
        v = x;
      },
      Suspended(w) => {},
      Cancelled => {}
    }
  }
  let f: i32 = __children_free(__children_cancel(kids));
  return (won, v);
}

pub function with_deadline_tasks[T](deadline: Duration, entries: (() => T)[]): Option[T][] {
  let kids: __Child[T][] = __children_drive(__children_start(entries), monotonic_ns() + deadline.as_nanos(), false);
  let out: Option[T][] = [];
  for k in kids {
    match (k.st) {
      Done(v) => {
        out = out.append(Some(v));
      },
      Suspended(w) => {
        out = out.append(None);
      },
      Cancelled => {
        out = out.append(None);
      }
    }
  }
  let f: i32 = __children_free(kids);
  return out;
}

pub function wait_any(set: i32[], timeout_ms: i32): i32 {
  if (__task_cur() != 0) {
    return __task_park(set, timeout_ms);
  }
  let n: i32 = set.len() / 2;
  if (n <= 0) {
    if (timeout_ms > 0) {
      sleep_ms(timeout_ms as i64);
    }
    return 0 - 1;
  }
  let reads: boolean = true;
  let i: i32 = 0;
  while (i < n) {
    if (interest_bits(set[i * 2 + 1]) != 1) {
      reads = false;
    }
    i = i + 1;
  }
  if (reads) {
    return __wait_readable(set, n, timeout_ms);
  }
  return __wait_watched(set, n, timeout_ms);
}
```

### `30 function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32`

```fern
<<30 function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32>>=
function __wait_readable(set: i32[], n: i32, timeout_ms: i32): i32 {
  let toks: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    toks = toks.append(tcp_pollable(set[i * 2]));
    i = i + 1;
  }
  let timer: i32 = 0 - 1;
  if (timeout_ms >= 0) {
    timer = __timer_pollable(timeout_ms as i64 * 1000000i64);
    toks = toks.append(timer);
  }
  let ready: i32 = poll(toks, timeout_ms);
  i = 0;
  while (i < n) {
    let dropped: i32 = drop_pollable(toks[i]);
    i = i + 1;
  }
  if (timer >= 0) {
    let dtimer: i32 = drop_pollable(timer);
  }
  if (ready >= 0 && ready < n) {
    return ready;
  }
  return 0 - 1;
}

function __wait_watched(set: i32[], n: i32, timeout_ms: i32): i32 {
  let drv: RealDriver = real_driver();
  let i: i32 = 0;
  while (i < n) {
    let watched: i32 = drv.watch(set[i * 2], interest_bits(set[i * 2 + 1]));
    i = i + 1;
  }
  let woke: i32[] = drv.wait(n, timeout_ms);
  i = 0;
  while (i < n) {
    let unwatched: i32 = drv.unwatch(set[i * 2]);
    i = i + 1;
  }
  let closed: i32 = drv.close();
  let hit: i32 = 0 - 1;
  let k: i32 = 0;
  while (k + 1 < woke.len()) {
    i = 0;
    while (i < n) {
      if (set[i * 2] == woke[k] && (hit < 0 || i < hit)) {
        hit = i;
      }
      i = i + 1;
    }
    k = k + 2;
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

pub(package) function drop_pollable(tok: i32): i32 {
  if (target_os() == "wasi" || target_os() == "wasi-http") {
    return wasm_pollable_drop(tok);
  }
  return 0;
}
```
