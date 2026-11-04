# Share-nothing workers

This module runs a closure as a worker and joins its result.

## Model

A worker owns its heap. A value crosses the worker boundary only by copy: the captures of the closure at `spawn`, and the result at `join`.
Both checkers admit only a send-safe value at the boundary (E100). A send-safe value is a scalar, a string, or a struct, enum, array, tuple or map of send-safe values.
A `Cell`, a function value, a `dyn` value, a view, a handle and a `@must_consume` value are not send-safe.

Fern values are immutable, so a copied tree and a shared tree cannot be told apart by any accepted program. The copy is unobservable.

On Linux a worker is a process. `spawn` maps a shared region and forks. The child adopts the region as its heap, runs the body, copies the result into the region, and exits. `join` waits for the child and takes the region's boxes as its own.
Every other engine runs the worker inline at `spawn`, which is one of the schedules a process can produce. The design is `docs/MULTICORE-RESEARCH.md`, and the shipped surface is `docs/WORKERS.md`.

## Contracts

`spawn` takes a `send` closure and returns a `Worker`.
A `Worker` is `@must_consume`: a program joins every worker it spawns (E067).
`join` consumes the worker and returns its result. A second join is refused (E051).
A worker that exits with a signal or without a result stops the program with exit 134.

## Executable example

```fern test name=std-worker-example
import "std/worker";

function main(): i32 {
  let n: i32 = 20;
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
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a closure as a worker and joins its result.
//
// A value crosses the worker boundary only by copy, and only a send-safe
// value is admitted (E100). On Linux a worker is a process that writes its
// result into a shared region. Every other engine runs the worker inline.
```

### `2 pub struct Worker[T]`

A worker holds its result when it ran inline. Otherwise it holds the pid of its process and the region that process writes its result into. The type is `@must_consume`, so a spawned worker is always joined.

```fern
<<2 pub struct Worker[T]>>=
/// A worker whose result `join` returns. Every worker is joined (E067).
@must_consume
pub struct Worker[T] { result: Option[T], pid: i32, region: i64, size: i64 }
```

### `3 pub function spawn[T](send body: () => T): Worker[T]`

Runs `body` as a worker. The closure's captures and its result cross the worker boundary, so both must be send-safe (E100). A native Linux program runs the worker as a process. Every other engine runs the body inline before `spawn` returns. The interpreters map no region, so they take the inline path of `__spawn_process`.

```fern
<<3 pub function spawn[T](send body: () => T): Worker[T]>>=
/// Runs `body` as a worker and returns the handle that `join` takes.
pub function spawn[T](send body: () => T): Worker[T] {
  if (target_os() == "linux") {
    return __spawn_process(body);
  }
  return Worker { result: Some(body()), pid: 0, region: 0i64, size: 0i64 };
}
```

### `4 function __spawn_process[T](own body: () => T): Worker[T]`

The region is a shared anonymous mapping that the fork inherits at the same address. Its header holds the root address of the result and the leak-check counters, and the child's heap starts one line past it. A fork that the kernel refuses runs the body inline.

The child copies its result, because a box the body built can point at a box the parent owns. A copy built from the child's cursor lies inside the region. The copy is wrapped in `Some`, so a scalar result crosses as a box too. The body frees boxes of the parent's image into the child's free lists, and a copy must not land in one. So the lists are cleared again at the mark before the copy. The child exits through `__worker_exit`, so neither the leak census nor the parent's at-exit work runs twice.

```fern
<<4 function __spawn_process[T](own body: () => T): Worker[T]>>=
function __spawn_process[T](own body: () => T): Worker[T] {
  let size: i64 = 1073741824i64;
  let region: i64 = 0i64;
  unsafe {
    region = __worker_region(size);
  }
  if (region <= 0i64) {
    return Worker { result: Some(body()), pid: 0, region: 0i64, size: 0i64 };
  }
  let pid: i32 = proc_fork();
  if (pid < 0) {
    return Worker { result: Some(body()), pid: 0, region: 0i64, size: 0i64 };
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
  return Worker { result: None, pid: pid, region: region, size: size };
}
```

### `5 pub function (own w: Worker[T]) join(): T`

Waits for the worker and returns its result. The call consumes the worker. A worker that ran inline hands back its stored result. A process worker is waited for, and its root becomes a value of this process, which owns every box of the copy from then on.

```fern
<<5 pub function (own w: Worker[T]) join(): T>>=
/// Returns the worker's result. The worker is consumed.
pub function (own w: Worker[T]) join(): T {
  match (w.result) {
    Some(v) => {
      return v;
    },
    None => {}
  }
  if (target_os() != "linux") {
    eprint("worker: a worker without a result on this target");
    exit(134);
  }
  return __join_process(w);
}
```

### `6 function __join_process[T](own w: Worker[T]): T`

Waits for the process and adopts its result. Only a native Linux program reaches it, so the engines without `proc` never see its calls.

```fern
<<6 function __join_process[T](own w: Worker[T]): T>>=
function __join_process[T](own w: Worker[T]): T {
  let status: i32 = proc_waitpid(w.pid);
  unsafe {
    let root: usize = __worker_reap(w.region);
    if (status != 0 || root == 0) {
      eprint("worker: the worker exited with status " + status.to_string() + " and no result");
      exit(134);
    }
    let v: Option[T] = __send_adopt(root);
    match (v) {
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
