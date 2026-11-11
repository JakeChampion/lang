# Share-nothing workers

This module runs a closure as a worker and joins its result.

## Model

A worker owns its heap. A value crosses the worker boundary only by copy: the captures of the closure at `spawn`, and the result at `join`.
Both checkers admit only a send-safe value at the boundary (E100). A send-safe value is a scalar, a string, or a struct, enum, array, tuple or map of send-safe values.
A `Cell`, a function value, a `dyn` value, a view, a handle and a `@must_consume` value are not send-safe.

A type parameter is send-safe when its bound names `Send`. The bound holds for every send-safe type argument (E021 otherwise).

Fern values are immutable, so a copied tree and a shared tree cannot be told apart by any accepted program. The copy is unobservable.

On Linux and Android a worker is a process. `spawn` maps a shared region and forks. The child adopts the region as its heap, runs the body, copies the result into the region, and exits. `join` waits for the child, copies the result into the parent heap, and removes the region.

Every other engine runs the worker on the `Inline` scheduler, which runs the body at `spawn`. That is one of the schedules a process can produce.

`spawn_on` runs a worker on a scheduler the program names, and `std/sim` is one: its seeded turns interleave the workers that park on its clock. The design is `docs/MULTICORE-RESEARCH.md`, and the shipped surface is `docs/WORKERS.md`.

## Contracts

`spawn` takes a `send` closure and returns a `Worker`. `spawn_on` takes a `Scheduler` and the closure.

A `Worker` is `@must_consume`: a program joins every worker it spawns (E067).
`join` consumes the worker and returns its result. A second join is refused (E051).
`poll` never waits. It consumes the worker and returns `Finished` with the result, or `Running` with the worker, which the caller polls or joins later.
A worker that exits with a signal or without a result stops the program with exit 134.
A process worker ends when its parent ends: the kernel sends it SIGTERM.

## Executable example

```fern test name=std-worker-example
import "std/worker";

function main(): i32 {
  let n = 20;
  let w = worker.spawn((): i32 => n + 1);
  if (w.join() != 21) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub struct Worker[T]>>

<<12 pub trait Send>>

<<3 pub function spawn[T: Send](send body: () => T): Worker[T]>>

<<4 function __spawn_process[T: Send](own body: () => T): Worker[T]>>

<<14 function __stat_pid(of_parent: boolean): i32>>

<<5 pub function (own w: Worker[T: Send]) join(): T>>

<<6 function __join_process[T: Send](own w: Worker[T], status: i32): T>>

<<13 pub function (own w: Worker[T: Send]) poll(): Polled[T]>>

<<7 function __wait_worker(pid: i32): i32>>

<<8 function __adopt_region[T](root: usize): Option[T]>>

<<9 function __unmap_region(region: i64, size: i64, reaped: boolean): void>>

<<10 pub trait Scheduler>>

<<11 pub function spawn_on[T: Send](s: Scheduler, send body: () => T): Worker[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a closure as a worker and joins its result.
//
// A value crosses the worker boundary only by copy, and only a send-safe
// value is admitted (E100). On Linux and Android a worker is a process that
// writes its result into a shared region. Every other engine runs the worker
// on a scheduler, and the default scheduler runs it inline.
```

### `2 pub struct Worker[T]`

A worker on a scheduler holds the scheduler and its index there. A process worker holds the pid of its process and the region that process writes its result into. The type is `@must_consume`, so a spawned worker is always joined.

```fern
<<2 pub struct Worker[T]>>=
/// A worker whose result `join` returns. Every worker is joined (E067).
@must_consume
pub struct Worker[T] { sched: Option[dyn Scheduler], index: i32, pid: i32, region: i64, size: i64 }
```

### `3 pub function spawn[T: Send](send body: () => T): Worker[T]`

Runs `body` as a worker. The closure's captures and its result cross the worker boundary, so both must be send-safe (E100). A native Linux or Android program runs the worker as a process. Every other engine runs the body on the `Inline` scheduler before `spawn` returns. The interpreters map no region, so they take the inline path of `__spawn_process`.

```fern
<<3 pub function spawn[T: Send](send body: () => T): Worker[T]>>=
/// Runs `body` as a worker and returns the handle that `join` takes.
pub function spawn[T: Send](send body: () => T): Worker[T] {
  // Android has the syscalls of Linux. The child makes no call into the C
  // library or the Java runtime of the app.
  if (target_os() == "linux" || target_os() == "android") {
    return __spawn_process(body);
  }
  return __spawn_on(inline(), body);
}
```

### `4 function __spawn_process[T: Send](own body: () => T): Worker[T]`

The region is a shared anonymous mapping that the fork inherits at the same address. Its header holds the root address of the result and the leak-check counters, and the child's heap starts one line past it. A fork that the kernel refuses runs the body inline.

The child copies its result, because a box the body built can point at a box the parent owns. A copy built from the child's cursor lies inside the region. The copy is wrapped in `Some`, so a scalar result crosses as a box too. The body frees boxes of the parent's image into the child's free lists, and a copy must not land in one. So the lists are cleared again at the mark before the copy. The child exits through `__worker_exit`, so neither the leak census nor the parent's at-exit work runs twice.

```fern
<<4 function __spawn_process[T: Send](own body: () => T): Worker[T]>>=
function __spawn_process[T: Send](own body: () => T): Worker[T] {
  let size = 4294967296i64;
  let region = 0i64;
  unsafe {
    region = __worker_region(size);
  }
  if (region <= 0i64) {
    return __spawn_on(inline(), body);
  }
  let parent = __stat_pid(false);
  let pid = proc_fork();
  if (pid < 0) {
    __unmap_region(region, size, false);
    return __spawn_on(inline(), body);
  }
  if (pid == 0) {
    // The kernel ends the worker when its parent ends, so no worker runs
    // detached. A parent that ended before this call sends nothing, so the
    // worker also ends when its parent is not the process that made it.
    let linked = reactor_ctl(-1, 6, 0, 0);
    let now = __stat_pid(true);
    if (parent > 0 && now > 0 && now != parent) {
      unsafe {
        __worker_exit(region, 0 as usize);
      }
    }
    unsafe {
      __heap_adopt(region + 64i64, region + size);
      let out = body();
      __heap_adopt(__heap_mark(), region + size);
      let root = __send_root(__send_copy(Some(out)));
      __worker_exit(region, root);
    }
  }
  return Worker { sched: None, index: 0, pid: pid, region: region, size: size };
}
```

### `14 function __stat_pid(of_parent: boolean): i32`

`/proc/self/stat` starts with the pid, the command name in parentheses, the state and the pid of the parent. The command name can hold a space or a parenthesis, so the fields after it start at the last `)`. The answer is 0 when the file cannot be read, and for a field that an emulator leaves at 0.

```fern
<<14 function __stat_pid(of_parent: boolean): i32>>=
function __stat_pid(of_parent: boolean): i32 {
  let stat = "";
  match (read_file("/proc/self/stat")) {
    Ok(text) => {
      stat = text;
    },
    Err(_) => {
      return 0;
    }
  }
  if (!of_parent) {
    return __number_at(stat, 0);
  }
  let close = -1;
  for i in 0..stat.len() {
    if (stat[i] == b')') {
      close = i;
    }
  }
  if (close < 0) {
    return 0;
  }
  // After the name: a space, the state letter and a space.
  return __number_at(stat, close + 4);
}

// The decimal number that starts at `at`, or 0.
function __number_at(text: string, at: i32): i32 {
  let n = 0;
  let i = at;
  while (i >= 0 && i < text.len() && text[i] >= b'0' && text[i] <= b'9') {
    n = n * 10 + (text[i] - b'0') as i32;
    i += 1;
  }
  return n;
}
```

### `5 pub function (own w: Worker[T: Send]) join(): T`

Waits for the worker and returns its result. The call consumes the worker. A worker on a scheduler runs the scheduler until its result is there. A process worker returns a copy in the parent heap after the parent removes the shared region.

```fern
<<5 pub function (own w: Worker[T: Send]) join(): T>>=
/// Returns the worker's result. The worker is consumed.
pub function (own w: Worker[T: Send]) join(): T {
  match (w.sched) {
    Some(s) => {
      let out: T = __join_scheduled(s, w.index);
      return out;
    },
    None => {}
  }
  if (target_os() == "linux" || target_os() == "android") {
    let status = __wait_worker(w.pid);
    return __join_process(w, status);
  }
  eprint("worker: a worker without a scheduler on this target");
  exit(134);
}

function __join_scheduled[T](s: dyn Scheduler, index: i32): T {
  let got: Option[T] = __adopt_region(s.finish(index));
  match (got) {
    Some(x) => {
      return x;
    },
    None => {
      eprint("worker: the worker wrote no result");
      exit(134);
    }
  }
}
```

### `6 function __join_process[T: Send](own w: Worker[T], status: i32): T`

Copies the result of the process that exited with `status` into the parent heap. The original tree leaves the worker region through bulk reclamation. It never enters the parent's free lists. Only a native Linux or Android program reaches this function.

```fern
<<6 function __join_process[T: Send](own w: Worker[T], status: i32): T>>=
function __join_process[T: Send](own w: Worker[T], status: i32): T {
  if (status != 0) {
    __unmap_region(w.region, w.size, false);
    eprint("worker: the worker exited with status " + status.to_string() + " and no result");
    exit(134);
  }
  unsafe {
    let root: usize = __worker_reap(w.region);
    if (root == 0) {
      __unmap_region(w.region, w.size, false);
      eprint("worker: the worker wrote no result");
      exit(134);
    }
    let v: Option[T] = __adopt_region(root);
    let copied: Option[T] = __send_copy(v);
    __send_root(v);
    __unmap_region(w.region, w.size, true);
    match (copied) {
      Some(x) => {
        return x;
      },
      None => {
        eprint("worker: the worker wrote no result");
        exit(134);
      }
    }
  }
}
```

### `13 pub function (own w: Worker[T: Send]) poll(): Polled[T]`

An event loop cannot wait in `join`, so it polls. A process worker that still runs comes back as `Running`. `proc_waitpid_nohang` gives -1 for a child that still runs, and its exit status for a child that it reaps. A worker on a scheduler has no process, so its pid is 0, and `poll` joins it: the scheduler has no state that tells a running worker from a finished one.

```fern
<<13 pub function (own w: Worker[T: Send]) poll(): Polled[T]>>=
/// A worker that `poll` found finished, with its result, or still running.
pub enum Polled[T] { Finished(T), Running(Worker[T]) }

/// Returns `Finished` with the result of a finished worker, or `Running` with
/// the worker. It does not wait. The call consumes the worker.
pub function (own w: Worker[T: Send]) poll(): Polled[T] {
  if (target_os() == "linux" || target_os() == "android") {
    if (w.pid > 0) {
      let status = proc_waitpid_nohang(w.pid);
      if (status == -1 || status == -4) {
        return Running(w);
      }
      return Finished(__join_process(w, status));
    }
  }
  return Finished(w.join());
}
```

### `7 function __wait_worker(pid: i32): i32`

A signal that lands while the parent waits makes `proc_waitpid` return `EINTR`, which is not the worker's exit. The wait starts again.

```fern
<<7 function __wait_worker(pid: i32): i32>>=
function __wait_worker(pid: i32): i32 {
  let status = proc_waitpid(pid);
  while (status == -4) {
    status = proc_waitpid(pid);
  }
  return status;
}
```

### `8 function __adopt_region[T](root: usize): Option[T]`

Takes the root as a value. The call is what makes the value an owner the caller can give away.

```fern
<<8 function __adopt_region[T](root: usize): Option[T]>>=
function __adopt_region[T](root: usize): Option[T] {
  unsafe {
    let v: Option[T] = __send_adopt(root);
    return v;
  }
}
```


### `9 function __unmap_region(region: i64, size: i64, reaped: boolean): void`

Removes the region before a fallback, failure, or completed join. A completed join imports the child counters first. Only a successful removal records the reclamation of the live child allocations.

```fern
<<9 function __unmap_region(region: i64, size: i64, reaped: boolean): void>>=
function __unmap_region(region: i64, size: i64, reaped: boolean): void {
  unsafe {
    let rc = __worker_unmap(region, size, reaped);
    if (rc != 0) {
      eprint("worker: cannot remove the worker region: " + rc.to_string());
      exit(134);
    }
  }
}
```

### `10 pub trait Scheduler`

A scheduler runs worker bodies and keeps their results. A body reaches it as a thunk that gives up its result and returns the root, so one scheduler holds workers of every result type. The `Inline` scheduler runs each body at `spawn`, and `std/sim` interleaves the bodies that park on its clock.

```fern
<<10 pub trait Scheduler>>=
/// Runs worker bodies and keeps their results until each is joined.
pub trait Scheduler {
  /// Starts `run` as a worker and returns its index.
  function admit(self: Self, run: () => usize): i32;
  /// Runs the workers until worker `index` has its result, and returns its root.
  function finish(self: Self, index: i32): usize;
}

/// The scheduler of an engine without threads: a body runs at `spawn`.
pub struct Inline { roots: Cell[i64[]] }

pub function inline(): Inline {
  let none: i64[] = [];
  return Inline { roots: cell_new(none) };
}

impl Scheduler for Inline {
  function admit(self: Inline, run: () => usize): i32 {
    let root: usize = run();
    let roots: i64[] = self.roots.get();
    self.roots.set(roots.append(root as i64));
    return roots.len();
  }
  function finish(self: Inline, index: i32): usize {
    return self.roots.get()[index] as usize;
  }
}
```

### `11 pub function spawn_on[T: Send](s: Scheduler, send body: () => T): Worker[T]`

Runs `body` as a worker of scheduler `s`. The thunk the scheduler gets wraps the result in `Some`, so a scalar result crosses as a box too.

```fern
<<11 pub function spawn_on[T: Send](s: Scheduler, send body: () => T): Worker[T]>>=
/// Runs `body` as a worker of `s`, and returns the handle that `join` takes.
pub function spawn_on[T: Send](s: dyn Scheduler, send body: () => T): Worker[T] {
  return __spawn_on(s, body);
}

function __spawn_on[T: Send](s: dyn Scheduler, own body: () => T): Worker[T] {
  let run: () => usize = (): usize => {
    unsafe {
      return __send_root(Some(body()));
    }
  };
  let index = s.admit(run);
  return Worker { sched: Some(s), index: index, pid: 0, region: 0i64, size: 0i64 };
}
```

### `12 pub trait Send`

The marker of a send-safe type. No impl declares it: a type satisfies the bound when every part of it is send-safe, and the checker refuses an `impl Send` (E021). A generic function writes `[T: Send]` to send a value of its type parameter.

```fern
<<12 pub trait Send>>=
/// Satisfied by every send-safe type. Bound a type parameter by it to send a value of that type.
pub trait Send {}
```
