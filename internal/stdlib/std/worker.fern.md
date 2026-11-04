# Share-nothing workers

This module runs a closure as a worker and joins its result.

## Model

A worker owns its heap. A value crosses the worker boundary only by copy: the captures of the closure at `spawn`, and the result at `join`.
Both checkers admit only a send-safe value at the boundary (E100). A send-safe value is a scalar, a string, or a struct, enum, array, tuple or map of send-safe values.
A `Cell`, a function value, a `dyn` value, a view, a handle and a `@must_consume` value are not send-safe.

Fern values are immutable, so a copied tree and a shared tree cannot be told apart by any accepted program. The copy is unobservable.
In this release every engine runs the worker inline at `spawn`, which is one of the schedules a thread can produce. The native targets gain real threads in a later release under the same contract. The design is `docs/MULTICORE-RESEARCH.md`, and the shipped surface is `docs/WORKERS.md`.

## Contracts

`spawn` takes a `send` closure and returns a `Worker`.
A `Worker` is `@must_consume`: a program joins every worker it spawns (E067).
`join` consumes the worker and returns its result. A second join is refused (E051).

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

<<4 pub function (own w: Worker[T]) join(): T>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a closure as a worker and joins its result.
//
// A value crosses the worker boundary only by copy, and only a send-safe
// value is admitted (E100). Every engine runs the worker inline at spawn in
// this release.
```

### `2 pub struct Worker[T]`

A worker that holds its result until `join` takes it. The type is `@must_consume`, so a spawned worker is always joined.

```fern
<<2 pub struct Worker[T]>>=
/// A worker whose result `join` returns. Every worker is joined (E067).
@must_consume
pub struct Worker[T] { result: T }
```

### `3 pub function spawn[T](send body: () => T): Worker[T]`

Runs `body` as a worker. The closure's captures and its result cross the worker boundary, so both must be send-safe (E100).

```fern
<<3 pub function spawn[T](send body: () => T): Worker[T]>>=
/// Runs `body` as a worker and returns the handle that `join` takes.
pub function spawn[T](send body: () => T): Worker[T] {
  return Worker { result: body() };
}
```

### `4 pub function (own w: Worker[T]) join(): T`

Waits for the worker and returns its result. The call consumes the worker.

```fern
<<4 pub function (own w: Worker[T]) join(): T>>=
/// Returns the worker's result. The worker is consumed.
pub function (own w: Worker[T]) join(): T {
  return w.result;
}
```
