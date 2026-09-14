# A local aliasing a borrowed parameter fell between both borrow legs

#9244, found benchmarking `coreutils/dd.fern`: four lines leak one reference
per call on x86-64.

```fern
function f(data: string): i32 {
  var piece: string = data;
  return piece.len();
}
```

Called from a frame that owns the string — a match binding of an owned
payload, which is what every read loop produces — every call strands the
whole buffer. `FERN_LEAKCHECK` over 1024 calls of a 64 KiB read:
`allocs=2049 frees=1024 live_bytes=67125280`, against `frees=2048` with the
alias line deleted. The cost is not only memory: each iteration's buffer is
a fresh mapping, so `ru_minflt` is 17,629 against 252 and `strace -c` puts
33 ms of a 43 ms run inside `read`, at 32 µs a call where the clean variant
is under 1 µs.

## Cause

Two halves that have to agree did not.

- The `*ast.Var` lowering emits the alias transfer inc unless the site is a
  proven borrowed view (`borrowedAliasSites`).
- The exit sweep's string arm touches a local only when it is
  `freeEligible` — its own comment says ineligible strings "are SKIPPED
  entirely", so a view string can never be misread — and an alias never is
  eligible.

`computeBorrowedAliases`' two legs would have cancelled the pair, and both
want an owned rc LOCAL as the source: the bare-ident leg through
`isOwnedRcLocal(x)`, the for-in element leg through the synthetic iterand.
`data` is a borrowed PARAMETER, so the shape was inc'd like an owned alias
and swept like a borrowed view.

## Change

A third leg, for `var y = p` where p is a borrowed parameter. Its safety
argument is the for-in leg's, one step stronger: the CALLER owns p across
the whole call and nothing in this frame releases it — a borrowed parameter
is not in the exit sweep at all — so y reads through a reference already
held and needs none of its own. Marking p a `borrowSource` is the other
half, refusing the precise drops and FBIP donorship so a cancelled inc can
never leave `.with` seeing rc == 1 and mutating the caller's buffer.

Two supporting widenings, each proven load-bearing by knockout:

- `aliasReturnsConfined` (was `forinElemReturnsConfined`) admits a
  DEFINITELY-scalar return value whose every mention of y is an argument to
  a builtin that states both claims — `copyingBuiltinArg` at that position
  or a pure-read receiver at 0 (`identOnlyBorrowedByBuiltin`). Without it
  `return piece.len()` reads as an escape, since the mention is a call
  argument rather than a projection. The builtin half is not optional: a
  bare element handed to a USER callee in return position is the
  argument-death shape `TestForinElemBorrowRefusesReturnEscapes` pins as a
  refusal, and admitting it on the scalar alone turned that test red.
- `borrowingCallArg` admits `copyingBuiltinArg` outright. That table's
  membership asserts exactly this gate's two claims, so it needs neither
  the `resultCannotAliasArg` proxy for the second nor an escape oracle for
  the first — which is what `w.write(s)` and `w.write_some(s)` need, since
  both hand the bytes to `write(2)` and keep nothing yet both answer with a
  boxed Option / Result.

The for-in desugar's own iterand alias is excluded by name
(`ast.ForEachIterPrefix`): claiming it here would widen that optimisation
as a side effect, and the element borrow below is decided against the
inc/dec counts this leg would change.

## What is still leaking

`coreutils/dd.fern` still strands two blocks per record, and neither is
this:

- the per-call Result box, 32 bytes, once the failure arm BINDS its
  payload — #9245, with its own reproducer;
- the read buffer itself, one block per record — #9246, bisected by
  knockout to `out = apply_case(out, tab, s.conv)` in `convert_record`,
  where `apply_case` hands its argument back unchanged when no case
  conversion is asked for. The callee takes the return-transfer inc on
  `return s` and nothing in the caller releases what it received: the
  binding is a call result whose callee can return a parameter, so
  `computeFreeEligible` borrow-taints it and the sweep's string arm stays
  silent. `resultIsCountedStringAlias` already answers the question, and
  `countedArgTemp` consults it for the ARGUMENT temp — nothing consults it
  for the RESULT, which is where the handed-over reference lands.

## The self-host

Its rc port has not taken #4402's dead-alias cancellation at all — the
guard comment in `rc_analysis.go` records it answering 19 and 30 where both
natives answer 99 and 54 — so it does not have this leak today. Whoever
ports the cancellation needs this leg with it, or the leak arrives with the
optimisation.
