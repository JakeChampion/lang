# Reject preallocated liveness offset tables

A private prototype preallocates two offset tables in `ssalive.compute_checked_kept`.
The original source appends one offset per block and a final offset.
The prototype allocates `nb + 1` entries and writes those same offsets by index.
The production source retains its original append operations.

One stage-1 producer builds both stage-2 compilers from the frozen `41879a90` compiler sources.
Only `compiler/ssalive.fern` differs between the two compiler snapshots.
Both compilers use the same fixed checker and standard library.
The producer hash is `e744816112dd5727b225a093ea87c39e37df54ce3d0a145a4e2675784217f26b`.
The checker hash is `11a3b2c1bbd016a7f255ce7a7bc1d40511dd37ec672a9741783217381cf6a99f`.

| Metric | Before stage-2 | Prototype stage-2 |
|---|---:|---:|
| Instructions | 23,404,957,928 | 23,396,259,460 |
| Calls to `__fern_arr_box` | 17,759,927 | 17,787,467 |

The prototype uses 0.037165% fewer instructions but adds 27,540 boxes.
Calls from `__fern_alloc_u8` add 28,000 boxes.
Calls from the two push helpers remove 460 boxes.
The allocation increase outweighs the small instruction reduction for this slice.
The prototype remains rejected for production.

Both full profile runs exit successfully.
Complete checker output remains byte-identical on x86-64, ARM64 and Wasm.
The before compiler hash is `7482a908f7a1546423c23dcc1fd39d84f73307e4de3b322cd80ed534ba4e94a3`.
The prototype hash is `d1da5b2105b18c5d0de7fccc94e99fb43078c3b93ceeb71e1ecb423bb191aa06`.
Each release binary matches its symbol binary's loaded code and data, except for ELF section fields.

A sanitized differential compares every Flow field across 256 cases on each target.
The cases cover dense and sparse IDs, reversed block order, dependencies, phis and array-growth boundaries.
They include 240 accepted helper graphs and 16 empty graphs whose missing entry produces the same refusal.
The differential compares the normal and checked solvers and three keep masks, and preserves the input alias.
These synthetic helper graphs do not replace full typed program tests.

All targets print the same result and report zero live bytes.
Native and ARM64 runs each balance 221,102 allocations.
The Wasm run balances 221,380 allocations.
The [raw evidence](../benchmarks/liveness-offset-rejected-2026-10-28/README.md) preserves the rejected source, full profiles, output bytes and differential.
This record establishes no production optimization or native latency improvement.
