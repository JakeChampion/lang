# Two questions were sharing one summary

#9246, the last of the three leaks benchmarking `coreutils/dd.fern` turned
up, and the one that cost it everything: 40.2 ms against GNU's 3.6 on a
64 MiB `/dev/zero` → `/dev/null` copy, 37.6 ms of it in SYSTEM time.

```fern
function conv(data: string, flag: boolean): string {
  var out: string = data;
  out = ident(out, flag);   // ident can hand its argument straight back
  return out;
}
```

One reference leaked per call. dd's `convert_record` is that shape exactly
(`out = apply_case(out, tab, s.conv)`, where `apply_case` returns its
argument unchanged when no case conversion is asked for), so every record
stranded its whole 64 KiB read buffer: `live_bytes=4329376` over 64 records,
`ru_maxrss` 69,760 kB for a copy that holds 64 KiB at a time, and
`ru_minflt` 17,531 against 41 for a loop that does not leak — each record's
buffer was a fresh mapping instead of the freed block, so the read faulted
sixteen pages in.

## Cause

Two wrong guesses first, both built and measured as exact no-ops, and both
worth recording so nobody repeats them. The RESULT binding was never the
leak — `var out2 = ident(data, flag)`, bound straight from the borrowed
parameter with no intermediate alias, is clean, so the caller's half of
`paramCountedRetain`'s pairing was already wired. A `rhsTainted` carve-out
for a counted string result changed nothing; neither did admitting a counted
handback in `borrowingCallArg`, because the escape oracle's own refusal
still fired behind it.

What leaked was `out`'s own SEED inc. `computeFreeEligible` taints a string
ident passed as a call argument unless the callee is known to retain that
parameter only through counted means, and `paramCountedRetain` answers that
— refusing a bare `return p` on a BORROWED parameter, on the reading that
such a return hands out the caller's own reference.

It does not. `function handout(a: string): string { return a; }` lowers to

```
local.load
rc.inc __fern_rc_inc
return
```

The return-transfer inc fires, and nothing can cancel it: move-on-return
needs an owned rc LOCAL and a parameter is never one, which is the same fact
`rhsTainted`'s slice-header carve-out already states. So the caller receives
a count of its own. While the refusal stood, `out` stayed tainted, the exit
sweep's string arm (which touches a string local only when it is
freeEligible) released nothing, and the seed's inc had no partner.

## Why the refusal is nonetheless right where it stands

Deleting the `if consumed` guard fixes the leak in one line — and turns SIX
tests in five files red, each written deliberately and curated across
several issues: `TestUncountedRetentionStaysUncredited`,
`TestStringParamThatIsRetainedStaysUncredited`,
`TestStringParamConcatAlongsideBareReturnStaysUncredited`,
`TestStringParamSetThenReturnedBareStaysUncredited`,
`TestStringParamPushedThenReturnedBareStaysUncredited`,
`TestStringParamForwardedToARetainingCalleeStaysUncredited`.

The hazards they name are all ONE consumer's: "crediting it double-frees the
caller's temp", "lets the caller free a buffer the result points at". That
is `countedArgTemp` — the caller dec'ing a FRESH TEMP immediately after the
call. `paramCountedRetain` feeds it, and feeds the string-argument taint as
well, and the two ask different questions:

- may the caller dec a fresh temp right after the call?
- may the caller's own LOCAL keep its scope-exit release?

A counted handback answers the second yes regardless of the first.

(For the record, the hazard could not be reproduced either: a program
exercising all six shapes with fresh temps passed and results used after the
call, built `FERN_SANITIZE=1 FERN_LEAKCHECK=1`, gives byte-identical output
with and without the one-line version — `allocs=2200 frees=1800`, the same
400 leaked blocks, and no over-release or use-after-free. That is one
program against six deliberate tests, which is not enough to flip a
soundness contract in this subsystem, and it did not need flipping.)

## Change

Two summaries over one shared walk.
`inferParamCountedRetain` is unchanged and still feeds `countedArgTemp`;
`inferParamNoUncountedAlias` is the same walk with one occurrence more
permissive — a bare `return p` counts — and computeFreeEligible's
string-argument taint reads that one instead. The six refusals stand
untouched.

Both halves are proven load-bearing by knockout: pointing the taint back at
the strict table loses `out`'s reclaim, and dropping the credit makes the
weak summary refuse.

## What it buys

dd, 64 MiB at `bs=64k`: **2.2 ms against GNU's 3.6** — 1.23× rather than
0.09×. `live_bytes` 4,329,376 → 134,048 over 64 records, `ru_maxrss`
69,760 kB → 5,596, `ru_minflt` 17,531 → 158.

## What is still open

`var out = data; var out2 = ident(out); return out2;` — the same handback
with the alias never REASSIGNED — still leaks the seed inc. `countedSeed`
only credits a seed whose local is later overwritten, so that shape has
neither the credit nor #9244's cancellation (whose leg declines: the alias
goes to a user callee, and `paramEscapesInFn` counts `return s` as an
escape). One reference per call, and nothing in the tree is written that
way.

## Native-only, and a debt entry (#4451)

`paramNoUncountedAlias` and the `creditBareReturn` relaxation live entirely
in `internal/ir`. Nothing in `examples/self_host` mirrors them: the
self-host's `counted_handback_*` machinery in `irlower.fern` gates the
SINKSHARE / deep-drop side, which is a different axis. So a program
compiled by the SELF-HOST still keeps the `out = f(out)` handback leak
until the goal-2 rc port reaches this.

The divergence is leak-direction and never a dangle, which is why nothing
red points at it — and `self_host_rcplan_diff_test.go` cannot see it
either, since it compares plans over its own anchored fixtures rather than
over a program with this shape. `docs/NATIVE-CONVERGENCE.md` says a new
native-only feature is a debt entry rather than a free win; this is that
entry.
