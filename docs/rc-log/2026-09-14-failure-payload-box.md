# Binding the failure payload forfeited the box

#9245, found benchmarking `coreutils/dd.fern`. `rcResultOwned` makes the
per-call Option / Result box of the I/O family the caller's, and the matched
arm frees it — until the failure arm BINDS its payload, which every real
caller does, because the errno text is what a diagnostic is made of.

Two programs differing only in that arm, 1024 calls each:

```
Err(_)   leakcheck: allocs=6153 frees=6149 live_bytes=131200
Err(e)   leakcheck: allocs=6153 frees=5125 live_bytes=163968
```

Same allocation count, 1024 fewer frees, 32 bytes a call — the box, and the
arm is never TAKEN in either run, so it is the free being compiled out
rather than a path that executes.

## Cause

`reclaimableMatchScrutinee` admits a NAMED pointer binding only when
`bindingConfinedInAll` proves the arm cannot let it escape, and that walk
excused a call argument through `borrowingCallArg`. That predicate is the
stage-(b) arg-temp reclaim's, and its first gate is a CONCRETE SCALAR result
(`resultCannotAliasArg`) — which the reclaim needs, because it decs a fresh
temp immediately after the call and the result must not BE the temp.

A confinement asks something weaker: can the pointer outlive the arm? The
escape oracle answers that outright, and covers both halves at once —
`paramEscapesInFn` counts a returned parameter as escaping, so a parameter
it clears is one the callee neither stored nor returned, whatever the
result's shape. Insisting on the scalar result as well refused every
`Err(e) => … strerror(e) …`, where the arm's whole use of the payload is a
helper turning it into a message.

## Change

The predicate is split. `readOnlyCallArg` carries the gates a confinement
needs — not a retain sink, not an `own` or owned-by-default position, and
the parameter cleared by the oracle (or by `copyingBuiltinArg` /
`pureReadReceiverBuiltin`, which state the same fact for a helper with no
Fern body). `borrowingCallArg` is that plus the scalar-result gate, so the
arg-temp reclaim is unchanged. `bindingConfinedToArm` asks the first.

The boundary is pinned in the same test: a helper that RETURNS the payload
(`Other(_, msg) => { return msg; }`) is still refused, because the returned
string and the box the join would free are the same storage.

## What it does not reach

`coreutils/dd.fern` still strands its Result box per record, and correctly:
its arm calls `gnu.io_error_text`, whose `Other(_, msg)` arm returns the
payload, so the oracle's verdict is right. Copying there
(`slice_unchecked(msg, 0, msg.len()) + ""`) does not lift it either — the
oracle still sees the payload reaching the return through the copy — so
seeing through a copy is a separate refinement, and it is only worth the
32 bytes once #9246 stops stranding the whole read buffer beside it.
