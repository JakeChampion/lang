# The arm64 default goes back to the stack machine

The arm64-linux default moved to `-backend ssa` in #9511 on a code-size
argument. This entry is why it moves back, and what has to be true before it
moves again.

## What went wrong

`internal/e2eselfhost` failed two leak-matrix rows on `main`:

```
FAIL: TestSelfHostLeakMatrixIRArm64/str__fnscope__alias_param
    verdict moved: recorded native=clean selfhost=clean,
    measured native=leak selfhost=clean
FAIL: TestSelfHostLeakMatrixIRArm64/str__if_block__alias_param
```

Both reproduce on a clean `origin/main` worktree, so neither belongs to any
open PR. The matrix's native leg compiles with no `-backend` flag — it takes
the default — so the flip is what moved the verdict.

## The cause

The SSA backends run the **single-word** string ABI: `buildArm64SSA` never
sets `ast.TwoWordOverride` (`internal/codegen/arm64ssa/gas.go` says so at
`emitStringAsBytesHelper`). The arm64 stack-machine emitter runs the two-word
ABI.

On the single-word ABI, `internal/ir/rc_analysis.go` deliberately taints a
string local passed to a user function out of reclaim:

> Conservatively taint string-typed idents passed to a user function so they
> are not reclaimed here; the retained copy stays live (a leak at worst, never
> a use-after-free). Gated to native — the two-word ABIs' string reclaim is
> already correct. #4174 follow-up.

So this is not an `arm64ssa` codegen bug. It is a known conservative leak that
x86-64 has always had, which the flip inherited for arm64's default. Dumping
the RC plan for `main` on the failing cell shows it directly — two-word gives
`freeEligible: keep`, single-word gives an empty plan.

The x86-64 matrix rows pin `clean` for the same cells, but the rows' own note
says the native column there is **vacuous**: const-fold plus SSO mean the
string never allocates, so the row records "nothing was allocated to leak"
rather than "the shape is reclaimed".

## What it costs

Retention goes from O(1) to O(input). Measured at exit under
`FERN_LEAKCHECK=1` on `coreutils/uniq.fern`:

| lines | stack machine | `-backend ssa` |
| --- | --- | --- |
| 1,000 | 384 B | 57,856 B |
| 2,000 | 384 B | 33,280 B |
| 4,000 | 416 B | 135,712 B |
| 8,000 | 448 B | 188,992 B |

`sort.fern` at 2,000 lines: 720 B against 53,184 B. `wc.fern`: 480 B against
5,584 B. The alloc and free counts barely differ between the two — 89/80
against 92/81 on `wc` — so it is the size of what is retained, not the number
of objects.

CLAUDE.md puts long-running allocation-heavy programs in scope, and names the
self-host compiler as exactly such a program. A default that retains in
proportion to input is not one to ship on a code-size argument.

## The decision

The default returns to the stack-machine emitter on every target. `-backend
ssa` still selects the SSA backends by name, and `ssaUnservedFlag` still
refuses the flags they cannot serve.

Everything the flip added to `resolveBackend` goes with it. The per-flag
fallback table existed only because the default could be SSA; with no target
defaulting to SSA there is nothing to fall back from, so
`TestArm64DefaultFallsBackForFlagsSSACannotServe` and
`TestBackendHelpNamesEveryFallbackTrigger` are deleted rather than adjusted.

## What has to be true to flip again

One of:

- `arm64ssa` runs the two-word string ABI, as the stack-machine emitter does; or
- #4174's taint is replaced by an interprocedural answer to whether a callee
  retains its string argument, which would let the single-word ABI reclaim
  these buffers and would fix x86-64 at the same time.

`internal/e2e/arm64_default_string_reclaim_test.go` holds the default to that
bar. It measures retention at two input sizes sixteen times apart rather than
against an absolute number, because the absolute figure moves with allocator
and stdlib changes while "does it grow with the input" is the property that
separates the two ABIs.

## What the gate had to get right

Three ways to write that fixture measure nothing, and the first two were
written before the third:

- a concat of two literals is const-folded, so nothing allocates and both
  emitters report zero;
- a string short enough for SSO never reaches the heap;
- a callee that reads its parameter without **aliasing it into a local** is
  reclaimed by both emitters. The alias is what the taint keys on, and it is
  the shape the matrix's `alias_param` cells pin.

The fixture was checked by mutation: with the default restored to `ssa` it
reports 4,000 bytes at 50 rounds and 64,000 at 800; with the stack machine it
reports 0 and 0.

## The pattern, again

This is the fourth entry in this log to end the same way. The flip's gates
asked *whether the default builds and runs*, and the default built and ran. No
gate asked whether it built something with the same memory behaviour. A gate
that names a backend (`-backend ssa`) stops covering the default the moment
the default moves — the same shape that let both SSA backends ship with no
`.eh_frame` (#9495, #9500), and the same shape as the ten missing runtime
helpers. The new test names no backend at all; it asks what `fern -target
arm64-linux` does.
