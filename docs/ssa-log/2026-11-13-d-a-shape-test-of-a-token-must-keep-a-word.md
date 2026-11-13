# 2026-11-13 — a shape test of a token must keep a word

x86-64 and arm64. `ssatokenkeep.keeps_word`. A correction to the entry
`2026-11-13-b` of this log.

## The fault

`TestSelfHostRetainedNativeBodies` failed on main from #15649. Its driver
compiles this function and gives the emitted form to the credit checker:

```
function replace(own p: Parent, child: Leaf): Other {
  ...
  return Other { first: child, second: child };
}
```

The construction takes the token of `p`. No block proves the shape of the
box, so `keeps` gave the allocation a block that compares the first word of
the token with the shape of `Other`. The box is a `Parent`, so the compare is
never true. The credit checker does not accept that form, and the driver
stopped with exit code 89.

#15649 merged before its self-host shards ran, so no run showed the failure.

## What changed

A token of an untested shape gets the shape test only when the side with the
box keeps one word or more. A reuse across two record types keeps no word, so
it stays one inline reuse allocation.

## Measured

x86-64, Callgrind instructions. The five Perceus workloads are equal, except
`cfold` at depth 12: 2,182,468 to 2,190,004 (+0.35%). Of the bench rows,
`http_hello.ir` moves most, from 21,868,422 to 22,004,422 (+0.62%). Each row
stays inside its tolerance.

## Trap

An exit through `exit()` leaves the frames above it, so the leak census of
the driver showed 4,692 live blocks. The census line has `from=exit` in that
case. The first line of the output had the cause.
