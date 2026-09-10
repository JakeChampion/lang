---
title: Memory
description: How Fern frees memory — Perceus reference counting, unconstructible cycles, in-place reuse, and the ownership modes you can ask for when it pays.
sidebar:
  order: 6
---

Fern has no garbage collector and no borrow checker. Memory is **reference
counted**, the compiler inserts the counting for you, and then it removes
most of it again.

You can write a whole program without thinking about any of this. The rest
of the page is what is happening underneath, and the two annotations —
`own` and `fip` — that let you say more when a hot path is worth it.

## Counted, then elided

Every heap value carries a count. The compiler inserts an increment where a
reference is retained and a decrement where one dies, then runs an analysis
that deletes every pair it can prove unnecessary — a value passed to a
function that only reads it is *borrowed*, not retained, so neither
operation survives. What is left runs at the point the last use goes out of
scope.

This is the Perceus model, and it has three consequences worth knowing:

- **Frees are deterministic.** A value dies at a point in your program you
  can name, not at a collector's convenience.
- **There is no pause and nothing to tune.** No heap size, no generation
  count, no GC flags.
- **The cost is spread out.** Counting is cheap but not free; the analysis
  is what makes it competitive, and in the common shapes it removes most of
  the traffic entirely.

## Cycles do not compile

Reference counting has one classic hole: a cycle keeps itself alive. Most
counted languages patch it with a cycle collector, weak references, or the
advice to be careful.

Fern closes it at the type checker instead. A cycle needs a mutation after
construction, and Fern does not have one:

- **Struct fields are immutable once the struct exists** (`E048`). You build
  a new value from an old one rather than assigning into it.
- **`arr[i] = v` is not a statement** (`E056`). Arrays are updated by
  `.with(i, v)`, which returns the array.
- **`Cell[T]`, the one mutable box, is restricted to scalars and strings**
  (`E057`). `Cell[Node]` is rejected, and so is `Cell` of a function type —
  those are exactly the two shapes a cycle would need.

So there is no cycle collector, no `Weak[T]`, and no leak to be careful
about, because the graph that leaks cannot be built.

The trade is real and worth stating plainly: back-pointers, doubly linked
lists, parent links in a tree and observer graphs all need a different
shape in Fern. The usual answer is an index into an array — the same move
an arena-based Rust program makes, for the same reason.

## Reuse in place

Immutability sounds expensive: if every update builds a new value, every
update allocates. It does not, because the compiler checks whether the old
value was uniquely owned first. When it was, the new value is written over
the old box.

```fern
struct Config {
  host: string,
  port: i32,
}

function on_port(c: Config, p: i32): Config {
  // If `c` is unique here, this writes into c's allocation
  // rather than allocating a second Config.
  return Config { ...c, port: p };
}
```

The same applies to a `match` that consumes its scrutinee and rebuilds a
variant, and to appending to an array nothing else refers to. Uniqueness is
established statically where it can be and checked at runtime where it
cannot; a shared value simply takes the allocating path, so the
optimisation can never turn into a bug.

`fern -append-report FILE` prints every `.append` in a program and says
whether it grew in place or copied, which is the fastest way to find an
accidental quadratic loop.

## Saying it: `own` and `fip`

Most code never mentions ownership. When a function is hot enough to care,
two annotations turn an assumption into something the compiler enforces.

**`own` consumes an argument.** The caller gives up its reference, so the
function is free to write into the buffer it was handed and return it.
Using the value again after the call is an error (`E050`, `E051`).

**`fip` promises the function allocates nothing.** The compiler rejects the
definition if it can allocate at all (`E053`), so the guarantee cannot rot
as the body changes — and a `fip` function may only call other `fip`
functions, so it cannot be undermined one level down. `fbip` is the
reuse-paired tier: allocation is allowed where the compiler can pair it
with a value being consumed, so the function still runs in constant space.
Both grade — `fip(2)` and `fbip(2)` allow that many unpaired allocations,
and `fbip` may call either tier.

```fern
// From the standard library. Insertion sort, deliberately: a merge
// sort needs a scratch buffer, and a `fip` function may not allocate.
pub fip function sort_i32_inplace_asc(own arr: i32[]): i32[] {
  var n: i32 = arr.len();
  var k: i32 = 1;
  while (k < n) {
    var key: i32 = arr[k];
    var j: i32 = k - 1;
    while (j >= 0 && arr[j] > key) {
      arr = arr.with(j + 1, arr[j]);
      j = j - 1;
    }
    arr = arr.with(j + 1, key);
    k = k + 1;
  }
  return arr;
}
```

Two related modes round the set out. An **owned array** is written `T[]`
and a **view** into one is `[T]` (`string` is a view over bytes); passing an
owned array where a view is wanted is fine, and the reverse is an error
(`E063`, `E065`). And `@must_consume` marks a type whose values may not be
dropped on the floor — the checker requires every one of them to be passed
on or explicitly consumed (`E067`).

## When something goes wrong

- **`fern -sanitize`** turns on a heap checker: over-release (double free),
  use-after-free of a quarantined block, and a leak census printed at exit.
  Native targets only, and it says so loudly rather than reporting a clean
  run it did not perform.
- **Heap exhaustion is an exit code, not a crash.** The native targets exit
  `125` with a clear message. On WebAssembly the module traps instead —
  `memory.grow` failure has nowhere else to go.
- **Nothing is atomic.** Reference counts are non-atomic by design, which
  is why Fern has no threads (see [Concurrency](../concurrency/)) and why
  interrupt handlers are the open question for the bare-metal targets.

## Where this is today

The model above is fully implemented in the Go compiler. Porting it to the
[self-hosted compiler](../../compiler/bootstrap/) is the project's live
front, and it is not finished: some value shapes still retain memory rather
than freeing it. That is tracked as a per-fixture leak census with exact
byte counts rather than as a general disclaimer, and the [status
page](../../status/) has the current numbers.

For ordinary programs the practical summary is unchanged: nothing you write
will leak by cycle, most allocations are freed at the point of last use,
and the cases that retain are shapes the project is measuring, not shapes
you have to guess at.
