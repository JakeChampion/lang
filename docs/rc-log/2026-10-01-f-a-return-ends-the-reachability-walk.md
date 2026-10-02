# 2026-10-01 — a return ends the rc verifier's reachability walk

`irverifyrc.reaches`, `internal/ir/verifyrc.go reaches`. Refs #6639, #10972. Follows
`2026-10-01-e-a-kept-field-is-neither-read-nor-stored.md`.

## What was reported

With the kept-field update (`e`) merged, the self-host compile of the
coreutils multicall binary and of `digest` refused under `FERN_IR_VERIFY`:

    ptx__put: op 530 (call_direct): reuse site takes local 0's box, which the
    site at op 339 already took and never rebound: one allocation handed to
    two recipients

Every coreutils shard and the parity test went red on main with it. The
shape, reduced:

    function put(own l: Line, n: i32): Line {
      if (l.mode == 2) {
        return Line { ...l, buf: l.buf.append(32 as u8) };
      }
      return Line { ...l, cursor: n };
    }

Two reuse sites on one donor, exclusive through the arm's `return`, not
through an `else`. The first replaces a heap field; with a scalar field in its
place the function was accepted.

## Why the walk saw one path

`reaches(from, to, donor)` walks structured control flow from the first claim
outwards: an `else` at its depth skips the sibling arm, an `end` at its depth
leaves the scope and continues, a `store_local donor` ends the path. It did
not read `return`. The arm's `end` was therefore a fall-through onto the
second site.

The walk answers false only at an `else` it can skip or at a store to the
donor's slot, and neither lies between these two sites now. Before the
kept-field update the same source was accepted, so a store to local 0 did lie
between them then (the construction stored the donor's handling before the
second call), or one of the sites was not recognised at all. Either way the
pass was leaning on an emitted op, not on the rule. The IR is right; the walk
was not.

## Change

A `return` at the walk's current depth leaves the function: `reaches` answers
false (#10972, "Stop reuse reachability at a return on the claimed path").
One scope deeper it ends only the nested arm it sits in and is not consulted,
so a return inside an inner `if` still falls out to the second site and that
pair is still reported. Both verifiers carry the two cases (`irverify_run`
221/222, `TestVerifyRcReturnSeparatesClaims`), and the reduced program above
is a row of the IR-verify gate sweep, which compiles every row under the flag
and demands byte-identical output with it off.

## Trap

A verifier that is right by luck fails on the next lowering change, not on
the one that is wrong. The slot zeroing was never part of the double-claim
rule's contract, and the rule had no test with a `return` between its sites.
When a reuse-site rule passes, ask which emitted op it is leaning on.
