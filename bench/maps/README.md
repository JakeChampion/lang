# `bench/maps` — Fern's maps against hashbrown, MultiTable and constmap

Two halves, one workload. `fern/` holds three Fern programs and `rust/` one
Rust binary. Each program builds a table of `n` keys once. It then looks every
key up `rounds` times in one fixed random order and prints the mean cost of
a lookup. The order is the same splitmix64 permutation on both sides. `run.sh`
builds both halves and runs the matrix below.

The directory answers two questions. Must `core/map` take MultiTable's
layout? Is a Fern `std/constmap` worth having? The workloads are the shapes
of `bench/map_int.fern` and `bench/map_string.fern`: `u32` keys `i * 7`, and
string keys `checker__symbol_<i>`.

This directory is not part of the perf corpus `scripts/perf-bench` measures.
The corpus records instruction counts. These are wall-clock comparisons
across languages, which the corpus rules exclude.

## Tables

| Row | What |
|---|---|
| `hashbrown<u32,u32>` | Rust's SwissTable, FxHash, `with_capacity(n)` |
| `multitable<4,4> util=0.9` | MultiTable (git `66f620f`), 4-byte keys and values, utilization 0.9; 0.99 fails to size for this shape |
| `std HashMap<String,u64>` | Rust's default map, SipHash |
| `hashbrown<&str,u64> fx` | the same table with FxHash |
| `constmap::ConstMap`, `VerifiedConstMap` | Lemire's Rust `constmap` 0.3 |
| `Map[i32,i32]`, `Map[string,u64]` | `core/map`, Fern |
| `constmap.ConstMap`, `VerifiedConstMap` | `std/constmap`, Fern |

## Measured 2026-10-21

`bin/fern-selfhost -O -target x86-64-linux` at `a19d08b` plus this branch,
`cargo build --release` with rustc 1.97, on a shared 4-core x86-64 container.
Nanoseconds per lookup. The host is noisy and slow: the Rust rows run two to
three times above the crates' published numbers, so read ratios within a
column, not absolute values. The Fern checksums include one warm-up pass.

| Table | 4 000 keys | 100 000 keys | 1 000 000 keys |
|---|---|---|---|
| hashbrown u32 | 6.0 | 11.5 | 39.9 |
| MultiTable u32 (util 0.9) | 7.2 | 19.7 | 46.8 |
| Fern `Map[i32,i32]` | 46.4 | 169.0 | 291.5 |
| std HashMap String | 41.6 | 340.4 | 442.0 |
| hashbrown &str fx | 19.8 | 52.0 | 203.7 |
| constmap (Rust) | 22.6 | 55.7 | 220.2 |
| VerifiedConstMap (Rust) | 26.0 | 78.2 | 257.0 |
| Fern `Map[string,u64]` | 79.7 | 632.0 | 545.5 |
| Fern `constmap.ConstMap` | 46.7 | 406.6 | 561.4 |
| Fern `VerifiedConstMap` | 117.1 | 471.6 | 631.2 |

The Fern constmap builds in 2 ms, 59 ms and 1.05 s. The Rust crate builds in
0.7 ms, 12 ms and 274 ms.

## What the numbers say

**MultiTable is not a lever for `core/map`.** On this workload MultiTable
looks up 1.2x to 1.7x slower than hashbrown at every size. That is the trade
its paper states: more probes, for a higher load factor and less memory.
Fern's `Map` is 7x to 15x slower than hashbrown on the same layout class
(SwissTable control bytes, SWAR groups, 75% load factor). The gap is in the
runtime path of a lookup, the kind-tag dispatch and the generic `get_or`, not
in the table.

`docs/MAP-SPECIALIZATION.md` names that lever and its two measured attempts.
The layout does not change the answer.

**A Fern constmap is worth having. Its cost is the hash.** In Rust,
`constmap` ties `hashbrown` with FxHash and halves the cost of the SipHash
`HashMap`. The lookup is one hash and three loads at every size. The
published 7x advantage is against the default map.

In Fern, `std/constmap` ties `Map[string,u64]` at 4 000 keys and beats it by
a third at 100 000. At 1 000 000 keys the two tie again, because memory
bounds both. A lookup costs about 47 ns at 4 000 keys. The hash is most of
that cost. `hash.xxh64` over a 20-byte key takes 64 ns through `key.bytes()`
and 54 ns when it reads the string in place. `hash_fnv32` takes about 30 ns.

The Rust crate hashes the same key in a few nanoseconds. The next step is the
`xxh64` kernel the compiler emits for `u64` arithmetic and byte loads. Every
string-keyed table in Fern pays there, so the fix belongs in the compiler,
not in a different structure. A string-input entry point for `xxh64` can save
the copy, a sixth of the cost.

**The build in `main` was quadratic. That is a compiler bug.** The first run
of these programs spent minutes before the measured region. A `Map` filled in
a loop and then read by the closure `bench.run` takes is copied on every
insert (#12927). The programs build their tables in helper functions until
that is fixed. The issue carries the reproducer.

## Running

```
make build                      # bin/fern-selfhost
bench/maps/run.sh               # the matrix above, ~20 s
bench/maps/rust/target/release/fern-maps-comparison 4000 40 0.9
/tmp/fern-maps-bench/map_string_lookup 4000 40
```
