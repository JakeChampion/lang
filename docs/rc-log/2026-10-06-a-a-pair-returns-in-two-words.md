# A pair returns in two words

2026-10-06: `sempair.pair_type`, `pair_return`, `rebuild_pair`. Slice 6 of
`docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2 (#9853), in part.

## The shape

```fern
function __serve_read(c: __Conns, at: i32, scratch: u8[], opts: Config): (__Conns, boolean) {
  ...
  return (__Conns { ...c, bufs: c.bufs.with(at, buf) }, true);
}
let read: (__Conns, boolean) = __serve_read(c, at, scratch, opts);
return (read.0, at, read.1);
```

A helper with several callers hands its two results back in one tuple, and
every caller takes the tuple apart at once. The tuple was a box per call.
The serve loop paid five of them per request (`__serve_read`,
`__serve_send_out`, `__serve_produce`, `__serve_ready`, and the parse's
helpers before §5.3 spliced them).

## The rule

`sempair` already returned a variant whose payloads fit a word as its
position in the result register and the payload in the word beside it
(`docs/SELFHOST-SSA-BACKEND.md`). A tuple of two values that each fit a word now
returns the same way: the first element is the result and
the second the word. The callee's returns are rewritten in place: a return
of a construction hands its two operands over, and any other return (a join
of two constructions, a parameter) is projected first. The caller rebuilds
the pair with a `tuple_new` of the result and the word, which split then
reads the caller's projections off, so the pair is never built.

The same conditions as a variant's: nothing outside the bodies names the
function, every caller calls it directly and takes the value apart, and it
never suspends. The pair stays in its block when rebuilt, where a variant's
rebuild moves the rest of the block to a join, so `rebuild_calls` reads a
block again after each rebuild: a block with two paired calls in it had kept
the second's tuple type against a contract already changed, and
`__str_critical_factorization`'s two `__str_maxsuf` calls refused the whole
compiler.

## Measured

x86-64. The probes return a tuple 1,000 times from a function marked
`@noinline`; the serve gate is `TestSelfHostServeAllocsPerRequest`.

| Shape | Before | After |
| --- | ---: | ---: |
| `(record, boolean)` built at the return | 1,000 | 0 |
| An update of the record and the tuple | 1,001 | 1 |
| Two field writes and the tuple | 1,003 | 3 |
| The serve loop, per hello request | 15 | 13 |

`TestSelfHostPairReturn` runs the pair program on x86-64, arm64 and wasm
under the leak census with 0 allocations in its loop.

## What stays boxed, and why

`__serve_produce` and `__serve_ready`, both read apart by both their
callers. Each reaches an indirect call (the chunk producer of a streamed
body, the stop callback), and once a program has a park in it the
suspension classifier counts every function with a call through a value as
able to park. A function that may suspend is not paired. That is the
classifier's rule, not the pairing's, and P3 owns it.

## A trap

The launcher runs `bin/fern-selfhost` beside `bin/fern` when it is there, so
`make selfhost-cli` after a compiler change compiles the compiler with the
compiler the previous `make selfhost-cli` built. A change that refuses a
stdlib function then refuses the compiler itself on the next build, and the
refusal names the stdlib function. `FERN_SELFHOST=~/.cache/fern/stage0/.../fern-selfhost-x86-64-linux make selfhost-cli`
rebuilds from the pinned stage0.
