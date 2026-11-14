# 2026-11-14 — a string parameter that a concatenation grows is owned

All targets. `ownership.carried_values`, `ownership.carry`,
`ssasem.owned_shape`, `semsource.kernel_named`, `semsink.lent_to`.
Fixes #15821.

## The shape

```fern
@noinline
function extend(s: string, k: i32): string {
    return s + "ab";
}
```

A caller that runs `s = extend(s, i)` 200 times made 200 allocations. The
plan already grows a string the frame owns in place (`grow_or_concat`,
2026-10-01), but `s` was a borrowed parameter. Ownership inference did not
find a concatenation to consume its left operand, and `owned_shape` did not
accept a string.

## What changed

A concatenation at the last use of its left operand now marks that operand
as consumed (`carried_values`). Inference counts a string parameter that
reaches such a concatenation, a counted slot of a callee, or a phi that one
of these consumes. The caller then gives its last unit, and the callee grows
the buffer in place or into a box with headroom.

A string box changes in place only where it grows. Thus a return, a
container or a store that keeps a string does not consume it
(`carry(..., strings: false)`). The first test of this change counted every
string that escapes, as #15810 did. `http_hello.ir` was 0.33% slower:
`http.__shared_string(..., prev)` returns `prev` on one path, so the counted
`prev` cost a release on the other path, and the retain only moved to the
caller. With the rule, `http_hello` compiles to the same code as on main.

`semsink.lent_to` excludes a string. A borrowed string can still be one that
the callee keeps.

## Bug found on the way

A call to the name of a builtin runs the kernel of the builtin, unless the
module shadows the name (`ssarc.string_method_site`). `std/string` declares
`replace` and `trim` with bodies that return `s` on one path. With strings
counted, inference gave these names a counted slot. Each caller then moved a
unit into a kernel that borrows it. The certifier refused
`fern__marked_snippet` (`leaked where it dies of v219`).

Now such a name keeps its declared modes (`kernel_named`). Its row still
takes its `lends` from the body. A builtin row marks these kernels as kernels
that lend, and then a caller that passes `s.text.trim()` lends `s`. That
demoted the counted `State` of `semsource.expr` and many other `semsource`
functions, and the compiler made 0.6% more allocations.

## Measured

Allocations under `FERN_LEAKCHECK` and `FERN_SANITIZE`, the same on x86-64,
arm64 under qemu and wasm32-wasi unless shown:

| Case | main | this change |
| --- | ---: | ---: |
| 200 calls that extend a string parameter | 200 | 5 |
| a callee loop, a recursion and a branch, 400 extensions | 204 | 11 |
| a string held twice, a literal, a slice, a field, an element | 15 (wasm 16) | 6 (wasm 7) |
| the caller reads its string after the call | 3 | 3 |

The compiler sources of main, built by each compiler with `FERN_LEAKCHECK`,
compile `checker.fern` with 46,253,694 allocations (main) and 46,201,750
(this change, -0.11%). The binary is 17,532,608 bytes and 17,582,320 bytes
(+0.28%): each grow site is a test and two calls.

`selfhost-alloc-bench` (this tree compiling `checker.fern`) measures
46,252,652 on main and 46,268,626 with this change (+0.035%). The compiler
makes more grow sites for the strings of `checker.fern` that are now counted.

`perf-bench`, `perf-bench-selfhost` and `cliff-bench`: no row moves. The
assembly of `map_string` is the same on both, and three runs of one binary
spread its Callgrind count by 0.2%.

Of the compiler's own functions, 366 have a parameter that is now counted.
51 have a record or an array parameter that is now borrowed. Each one gave
out a string element of that parameter through a phi. Inference counted the
container only because the string was marked, which a keep now does not do.

## Traps

- The bootstrap builds stage 1 with the pinned stage 0. `cliff-bench` runs
  stage 1, so it measures the compiler's decisions, not the new modes in the
  compiler's own code. To measure the modes alone, build one set of compiler
  sources with each `bin/fern` and compare.
- `FERN_SEM_DUMP='*'` prints the modes of each function: 1 is a value, 2 is
  borrowed, 3 is counted. Each function prints twice, before and after
  inference.
