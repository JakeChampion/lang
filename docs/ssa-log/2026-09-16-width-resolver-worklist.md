# Measured 2026-09-16: the width resolver as a worklist

`2026-09-16-x86-strbuf-and-the-self-host-driver.md` left the SSA path
compiling the self-hosted driver in 91.7 s against the flat backend's
18.5 s, and asked for a profile before the flip is proposed. This is that
profile, and the first thing it found.

**Where the 91.7 s went.** `go test -cpuprofile` on a test that calls the
driver's own `run` for `examples/self_host/asm_ir_run.fern` with
`-target x86-64-linux -backend ssa`, on the 4-core container:

| function | flat | cumulative |
| --- | --- | --- |
| `ssa.(*widthResolver).step` | 25.3 s (21.5%) | 45.0 s (38.2%) |
| `ssa.ComputeLivenessWithDependencies` | 1.3 s | 13.7 s (11.6%) |
| Go map access under `step` (`mapaccess1_fast32`, `matchH2`) | 12.4 s | 16.1 s |
| garbage collector (`scanObject`, `scanSpan`, `mallocgc`) | | about 10% |

The width resolver was one global fixpoint: every round walked every op of
every function in the module, and a round ran whenever any function had
learned anything anywhere. Address-ness travels through calls in both
directions, so on a module the size of the driver a fact discovered
late in one function costs a full walk of
the module to carry it one call further, and its per-value state was a Go
map keyed by value ID, so each of those walks was mostly hashing.

**What changed.** `internal/ssa/width.go` is now a per-function worklist.
Every function settles once, to a local fixpoint over its own arithmetic,
phis, loads and stores, then across the calls it makes; a function goes
back on the queue only when something it depends on changes: a callee
starts returning an address, or a parameter it passes an address to, or
one of its own parameters, is marked. The per-value state is a dense slice
indexed by value ID. The rules are the ones the global pass applied; the
new test builds a chain of three returns and a chain of three parameters,
each named so that the settling order visits every caller before the fact
it needs exists, and checks that the queue alone carries the fact the
whole way.

| build | before | after |
| --- | --- | --- |
| x86-64 SSA, driver compile | 91.7 s | 54.6 s |
| `ResolveWidths`, cumulative | 45.0 s | 0.4 s |
| binary | 9,365,243 B | 9,365,243 B |

The binary is byte-for-byte the same size, as it should be: the resolver
reaches the same fixpoint by a different route.

**What the profile shows now.** Total samples fell from 117.7 s to 70.0 s,
and the SSA layer's remaining entry is `ComputeLivenessWithDependencies`
at 13.6 s cumulative, now 19% of the whole, with the garbage collector and
Go map traffic under it. The parser's `ast.Walk` (5.5 s) and the
assembler's line parsing (about 2 s) are what the flat backend pays too.
Liveness is the next lead; nothing in the profile explains the 6% larger
binary, which is a codegen question, not a compile-time one.
