# 2026-08-30 — a vacuous case answered the wrong way round (#7798, #7820)

The reusable part is the direction of the mistake, not the one-line fix: the
issue and I both reasoned about vacuity and both got its sign wrong, in
opposite ways, and the runtime settled it.

## The leak

Three lines, one allocation, zero frees on x86-64; clean on arm64:

```fern
function two(a: string, b: string): string { return a + b; }
function ignore(s: string): i32 { return 7; }
function main(): i32 {
    var s: string = two("abcd", "efgh");
    return ignore(s) - 7;
}
```

```
x86-64  leakcheck: allocs=1 frees=0 live_bytes=32   fern-sanitizer: leak 32 bytes in 1 blocks
arm64   leakcheck: allocs=1 frees=1 live_bytes=0
```

Three conditions, all required: a HEAP string (SSO and literals allocate
nothing), a named LOCAL rather than a temporary, and a callee parameter that is
NEVER READ. Reading it once — `s.len()` — makes it clean.

Direction matters: every other x86-64/arm64 leak divergence in the corpus runs
the other way (slice 5g, #6554, where arm64 leaks more). This one is x86-64
only, so neither 5g nor the two-word ABI explains it.

## The chain

`computeFreeEligible` taints a string ident passed to a user function, gated
`!ast.UseTwoWordStrings(b.ptrW)` — native single-word only. Its own comment: the
callee may retain the argument into a container it returns, which the
intraprocedural escape analysis cannot see, so freeing it caller-side would
dangle the retained copy — "a leak at worst, never a use-after-free".

The taint lifts when `paramCountedRetain` says every retention in the callee is
counted. Its three summaries — `stringParamCounted`, `arrayParamCounted`,
`structParamProjectionsSafe` — each ended

```go
return total > 0 && total == len(safe)
```

`total` is the occurrence count. A parameter the body never mentions has
`total == 0`, fails the guard, and reads as *unknown* — so the caller kept a
taint protecting against a retention that provably cannot happen. The release
is then not merely deferred: `emitDec`'s string branch emits NOTHING when
`eligible` is false, on either ABI branch.

Zero occurrences is the STRONGEST form of the property being asked about, not
the absence of evidence for it. `everyOccurrenceSafe` now names the rule once.

The tally is sound for it: `ast.Walk` descends into `Lambda` bodies and
`MakeClosure` captures, so a parameter a nested closure touches is counted, and
a shadowed one is excluded before either summary runs.

## The trap: two wrong readings of the same vacuity

#7798's own "where it probably lives" guessed that vacuity made the summary
TRUE — the caller stops tainting, hands ownership to a callee that emits no
release, and the reference is lost between frames. Same mechanism, opposite
sign: the guard AGAINST vacuity made it false.

Reading the code, I then concluded the opposite error: `total > 0` is present,
therefore `inferParamCountedRetain` is not the site. Posted that on the issue.
Also wrong — it IS the site, just not for the stated reason.

What settled it was a probe, not more reading. Instrumenting
`emitRcDecLocalsAtExitExcept`'s loop over both backends:

```
x86-64  local=s rcTracked=true seen=false moved=false freeEligible=FALSE
arm64   local=s rcTracked=true seen=false moved=false freeEligible=TRUE
```

`seen` and `movedLocals` were both false, which killed my second hypothesis
(that the sweep skipped the local) in one line. The sweep reached it on both;
only `freeEligible` differed.

## A test was asserting the bug

`TestLowerStringPassedToUserFnNotReclaimedNative` required that a string passed
to a user function NOT be reclaimed caller-side, and its example callee was

```fern
function keep(s: string): i32 { return 0; }
```

— an empty body, which is the vacuous case itself. The retained-copy hazard
needs a callee that can retain; this one cannot. So the test pinned the leak as
correct behaviour, which is why it survived. It now uses a callee that stores
its argument into a returned container, with a mirror asserting the
unused-parameter case reclaims.

## Measured, before and after

All ten rows of the issue's bisection, as one `rcCorpus` case
(`unused_param_string_never_freed`) returning `__rc_underflow_count()` so the
over-release direction is gated too:

| | allocs / frees / live_bytes |
|---|---|
| before, x86-64 | 13 / 7 / **192** (6 blocks) |
| after, x86-64 | 13 / 13 / **0** |
| after, arm64 | 13 / 13 / **0** |

Under `FERN_SANITIZE=1`, so the use-after-free and over-release detectors were
on for both. Passes on wasm as well. Both rc corpus leak gates pass unchanged —
no pinned baseline moved, in either direction.

## Next lead

The five shapes that were already clean stay clean, so the taint's remaining
scope is unchanged: a string ident passed to a callee that DOES mention its
parameter but is not counted-retain. That is still a never-reclaim on native
single-word, and it is the population the ownership signature table (#7786,
landed 2026-08-30 as `ir.RcHelperSig` / `ssa.SolveOwnership` /
`ir.Func.ParamConsumed`) can now speak to: "callee consumes parameter i versus
borrows it" is checkable rather than latent. Nobody has measured how many
programs sit in it.
