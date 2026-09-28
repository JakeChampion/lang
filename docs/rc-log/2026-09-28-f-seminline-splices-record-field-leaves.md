# 2026-09-28 — seminline splices leaves that read a record parameter's scalar fields (#8920)

Typed path only (`seminline` runs on the semantic source).

## Change

A leaf used to be spliced only when every value in it was a scalar or a scalar
tuple. An accessor such as `span(r: Range): (i32, i32) { return (r.lo, r.hi -
r.lo); }` therefore stayed a call, and its tuple result was boxed on every call.

Such a leaf may now take a record parameter, provided the record appears only as
a parameter and is read only by `record_get` into a scalar. `leaf` checks this
without an extra array: the number of record-typed values must equal the number
of params whose value is a record, because each param defines its own value.
Inside the leaf the record is never built, stored, passed on or returned, so
splicing it at the call site moves no ownership. The caller still holds the
record, and each field read becomes a plain load.

## Witness

`TestSelfHostSemanticInline` / `...Off` gain `span`, a `@noinline` copy
`kept_span`, and a 100-round driver for each (t*1000 plus the allocation count).
With the pass on, `span_rounds` allocates 0 times and `kept_span_rounds` 100
times. With the pass off, or under the old leaf rule, both allocate 100 times.

## Measured (x86-64)

Both compilers were built by themselves: `make selfhost-cli`, then that
compiler compiling `fern.fern`. Each then compiled `coreutils/tsort.fern` under
callgrind.

| | main dc4e9f900 | this change |
|---|---|---|
| instructions, `tsort` compile | 3,769,948,910 | 3,769,170,898 (−778,012, −0.02%) |
| self-built compiler binary | 12,340,800 B | 12,345,312 B (+4,512) |
| `tsort` binary | | byte-identical |

The first draft of `leaf` kept a per-value `params: boolean[]`. On
`selfhost-alloc-bench` (native-built compiler, `checker.fern`), that added
7,595 allocations (81,150,605 → 81,158,200). The count comparison above
replaced it.

Why the gain is this small was not measured: the binaries carry no symbols, so
callgrind cannot say which call sites were spliced. A likely reason, unchecked,
is that most of the compiler's accessors read a string, array or record field
rather than a scalar. A leaf that only measures a borrowed field (`len`) would
be the next widening to try. It must prove that the borrow is not retained
across the splice, which the scalar rule does not need.
