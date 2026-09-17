# The IoError box never owned its path

Both SSA backends destroyed the string a failing path builtin was given.
`lstat(p)` left `p.len()` reading 0, and the next allocation was handed p's
block. #9543.

## The defect

A failing path builtin returns `Err(IoError)`, and the IoError names the path.
Both SSA backends stored the CALLER's pointer in that box without retaining it:

```
    str x1, [x0, #8]     // arm64ssa, .Lssa_ioe_path
    mov rsi, rbx         // x86_64ssa, ssaIoErr
```

The box outlives the call and its drop releases what it holds, so the drop
freed a buffer the caller still owned. One extra free, and the freed block went
straight back out of the freelist:

```fern
var p: string = mk("abc");
match (lstat(p)) { Ok(_) => {}, Err(_) => {} }
var q: string = mk("XY");
// flat:        p=[abc] q=[XY]
// -backend ssa: p=[XY]  q=[XY]
```

`FERN_LEAKCHECK` names the extra event: `allocs=4 frees=3` against the stack
machine's `3/2`.

## The contract was already there

The sibling error paths — the ones with no path to report — hand
`__fern_io_error` a *freshly allocated* empty string (`emitEmptyString` /
`ssaEmptyString`, rc 1). Those are correct: the helper's contract is that the
caller passes an OWNED reference, and the box takes it over.

So the helper storing without retaining is right. What was wrong is that the
sites with a real path passed a BORROWED one. The fix makes those sites obey
the contract the empty-string sites already did, rather than changing the
contract: 15 sites in arm64ssa through a new `emitIoErrorOwningPath`, and 3 in
x86_64ssa through `ssaRetainPathForIoErr`.

Both retain with the existing `__fern_rc_inc`, which already guards low
addresses and the immortal-literal sentinel, so a literal path costs nothing.
errno rides the stack across the retain because these sites keep the path in
whichever callee-saved register was free, and no one register is spare at all
of them.

## What the evidence actually said

Four mechanisms were proposed before any were measured, and all four were
wrong:

- the four garbage bytes are the loop index — they are constant when the index
  moves;
- the data pointer is off by one word and reads the length header — that would
  print the component's length, which varies; the bytes do not;
- the argument arrives wrong — instrumenting the callee's entry shows it
  arrives correct;
- the heap guard clobbers the copy's registers — it saves and restores x0/x1
  and touches nothing else.

What worked was bisecting by instrumentation (correct at entry, correct before
`lstat`, destroyed after) and then varying one thing at a time. The two results
that settled it were negative ones: a literal path is unaffected (immortal, so
the release is a no-op) and an alias is unaffected (rc stays above one). Both
point at reclaim, and neither is explicable by a bad address.

## Why no gate caught it

`internal/coreutils` compiles with the target's DEFAULT backend, so while
arm64-linux defaulted to SSA (#9511) it was running this miscompile across
`TestCpParity`, `TestInstallParity` and most of `TestSelfHostCoreutilsParity` —
92 failures, reported as wrong output rather than as a crash. Before that flip
the same programs did not compile under `-backend ssa` at all, because
`fn_read_line` had no emitter: the refusal read like coverage.

That is the same shape this log keeps recording. A gate that names a backend
stops covering the default the moment the default moves, and a missing emitter
turns a wrong answer into a refusal that looks like a pass.
`internal/e2e/ssa_ioerror_path_ownership_test.go` names both backends
explicitly and compares each against the stack machine, so neither dodge works
on it.

## Measured

`read_file` is the one path builtin that did not exhibit it on either backend,
confirmed by mutation: with the retains removed, every other case in the new
test flips and `read_file` does not. It stays in the table as a guard, not as
evidence.
