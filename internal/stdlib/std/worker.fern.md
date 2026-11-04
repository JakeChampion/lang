# Share-nothing workers

This module runs a closure as a worker and joins its result.

## Model

A worker owns its heap. A value crosses the worker boundary only by copy: the captures of the closure at `spawn`, and the result at `join`.
Both checkers admit only a send-safe value at the boundary (E100). A send-safe value is a scalar, a string, or a struct, enum, array or tuple of send-safe values.
A `Cell`, a function value, a `dyn` value, a view, a handle and a `@must_consume` value are not send-safe.

Fern values are immutable, so a copied tree and a shared tree cannot be told apart by any accepted program. The copy is unobservable.

On Linux a worker is a process. `spawn` maps a shared region and forks. The child adopts the region as its heap, runs the body, copies the result into the region, and exits. `join` waits for the child, copies the result into the parent heap, and removes the region.

Every other engine runs the worker on the `Inline` scheduler, which runs the body at `spawn`. That is one of the schedules a process can produce.

`spawn_on` runs a worker on a scheduler the program names, and `std/sim` is one: its seeded turns interleave the workers that park on its clock. The design is `docs/MULTICORE-RESEARCH.md`, and the shipped surface is `docs/WORKERS.md`.

## Contracts

`spawn` takes a `send` closure and returns a `Worker`. `spawn_on` takes a `Scheduler` and the closure.

A `Worker` is `@must_consume`: a program joins every worker it spawns (E067).
`join` consumes the worker and returns its result. A second join is refused (E051).
A worker that exits with a signal or without a result stops the program with exit 134.

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

<<3 pub function spawn[T](send body: () => T): Worker[T]>>

<<4 function __spawn_process[T](own body: () => T): Worker[T]>>

<<5 pub function (own w: Worker[T]) join(): T>>

<<6 function __join_process[T](own w: Worker[T]): T>>

<<7 function __wait_worker(pid: i32): i32>>

<<8 function __adopt_region[T](root: usize): Option[T]>>

<<9 function __unmap_region(region: i64, size: i64, reaped: boolean): void>>

<<10 pub trait Scheduler>>

<<11 pub function spawn_on[T](s: Scheduler, send body: () => T): Worker[T]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a closure as a worker and joins its result.
//
// A value crosses the worker boundary only by copy, and only a send-safe
// value is admitted (E100). On Linux a worker is a process that writes its
// result into a shared region. Every other engine runs the worker on a
// scheduler, and the default scheduler runs it inline.
```

### `2 pub struct Worker[T]`

A worker on a scheduler holds the scheduler and its index there. A process worker holds the pid of its process and the region that process writes its result into. The type is `@must_consume`, so a spawned worker is always joined.

```fern
<<2 pub struct Worker[T]>>=
/// A worker whose result `join` returns. Every worker is joined (E067).
@must_consume
pub struct Worker[T] { sched: Option[dyn Scheduler], index: i32, pid: i32, region: i64, size: i64 }
```

### `3 pub function spawn[T](send body: () => T): Worker[T]`

Runs `body` as a worker. The closure's captures and its result cross the worker boundary, so both must be send-safe (E100). A native Linux program runs the worker as a process. Every other engine runs the body on the `Inline` scheduler before `spawn` returns. The interpreters map no region, so they take the inline path of `__spawn_process`.

```fern
<<3 pub function spawn[T](send body: () => T): Worker[T]>>=
/// Runs `body` as a worker and returns the handle that `join` takes.
pub function spawn[T](send body: () => T): Worker[T] {
  if (target_os() == "linux") {
    return __spawn_process(body);
  }
  return __spawn_on(inline(), body);
}
```

### `4 function __spawn_process[T](own body: () => T): Worker[T]`

The region is a shared anonymous mapping that the fork inherits at the same address. Its header holds the root address of the result and the leak-check counters, and the child's heap starts one line past it. A fork that the kernel refuses runs the body inline.

The child copies its result, because a box the body built can point at a box the parent owns. A copy built from the child's cursor lies inside the region. The copy is wrapped in `Some`, so a scalar result crosses as a box too. The body frees boxes of the parent's image into the child's free lists, and a copy must not land in one. So the lists are cleared again at the mark before the copy. The child exits through `__worker_exit`, so neither the leak census nor the parent's at-exit work runs twice.

```fern
<<4 function __spawn_process[T](own body: () => T): Worker[T]>>=
function __spawn_process[T](own body: () => T): Worker[T] {
  let size: i64 = 4294967296i64;
  let region: i64 = 0i64;
  unsafe {
    region = __worker_region(size);
  }
  if (region <= 0i64) {
    return __spawn_on(inline(), body);
  }
  let pid: i32 = proc_fork();
  if (pid < 0) {
    __unmap_region(region, size, false);
    return __spawn_on(inline(), body);
  }
  if (pid == 0) {
    unsafe {
      __heap_adopt(region + 64i64, region + size);
      let out: T = body();
      __heap_adopt(__heap_mark(), region + size);
      let root: usize = __send_root(__send_copy(Some(out)));
      __worker_exit(region, root);
    }
  }
  return Worker { sched: None, index: 0, pid: pid, region: region, size: size };
}
```

### `5 pub function (own w: Worker[T]) join(): T`

Waits for the worker and returns its result. The call consumes the worker. A worker on a scheduler runs the scheduler until its result is there. A process worker returns a copy in the parent heap after the parent removes the shared region.

```fern
<<5 pub function (own w: Worker[T]) join(): T>>=
/// Returns the worker's result. The worker is consumed.
pub function (own w: Worker[T]) join(): T {
  match (w.sched) {
    Some(s) => {
      let out: T = __join_scheduled(s, w.index);
      return out;
    },
    None => {}
  }
  if (target_os() == "linux") {
    return __join_process(w);
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

### `6 function __join_process[T](own w: Worker[T]): T`

Waits for the process and copies its result into the parent heap. The original tree leaves the worker region through bulk reclamation. It never enters the parent's free lists. Only a native Linux program reaches this function.

```fern
<<6 function __join_process[T](own w: Worker[T]): T>>=
function __join_process[T](own w: Worker[T]): T {
  let status: i32 = __wait_worker(w.pid);
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

### `7 function __wait_worker(pid: i32): i32`

A signal that lands while the parent waits makes `proc_waitpid` return `EINTR`, which is not the worker's exit. The wait starts again.

```fern
<<7 function __wait_worker(pid: i32): i32>>=
function __wait_worker(pid: i32): i32 {
  let status: i32 = proc_waitpid(pid);
  while (status == 0 - 4) {
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
    let rc: i32 = __worker_unmap(region, size, reaped);
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

### `11 pub function spawn_on[T](s: Scheduler, send body: () => T): Worker[T]`

Runs `body` as a worker of scheduler `s`. The thunk the scheduler gets wraps the result in `Some`, so a scalar result crosses as a box too.

```fern
<<11 pub function spawn_on[T](s: Scheduler, send body: () => T): Worker[T]>>=
/// Runs `body` as a worker of `s`, and returns the handle that `join` takes.
pub function spawn_on[T](s: dyn Scheduler, send body: () => T): Worker[T] {
  return __spawn_on(s, body);
}

function __spawn_on[T](s: dyn Scheduler, own body: () => T): Worker[T] {
  let run: () => usize = (): usize => {
    unsafe {
      return __send_root(Some(body()));
    }
  };
  let index: i32 = s.admit(run);
  return Worker { sched: Some(s), index: index, pid: 0, region: 0i64, size: 0i64 };
}
```
