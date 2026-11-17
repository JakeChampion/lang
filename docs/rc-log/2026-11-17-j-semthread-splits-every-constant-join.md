# semthread splits every join of constant tags

2026-11-17. Each target. The change is in `semthread`.

## The gap

`semthread.split_constant_joins` splits a join whose branch reads a phi of
constants. Such a join is the shape of a match on a call result in two
words. Each round of the pass splits one join, and the pass stopped after
16 rounds with no report. A function that splices many such calls keeps the
joins after the 16th. `verify.server` needs 81 rounds, and 15 functions of
`fern_main` need more than 16.

An unsplit join keeps a phi of each payload with a zero, and a branch on
the tag phi after it. The emitted code tests the tag again, and the
certifier cannot connect the tag to its payload. Over `fern_main`,
`verify.server` had 129 unproved values.

## The change

- The pass runs until no join is left to split. Each round removes the join
  that it splits, so the limit is one round for each block of the function.
- When the pass reaches the limit, it reports that it did not settle
  (`util.report_unsettled`), as the other loops of `semthread` do.

## Measured after

Over `compiler/fern_main.fern` on x86-64, 7,616 values are unproved, from
8,014. `verify.server` has 48 unproved values. The mutant rows of
`compiler/checker.fern` do not move.

A compile of `fern_main` takes the same time, about 70 seconds. The
assembly of `fern_main` grows by 118,413 bytes, from 202,572,709 bytes
(0.06%).

`TestSelfHostSemthreadSplitsEveryConstantJoin` matches twenty spliced
probes with a string payload in one function. Before the change, the
certifier leaves 4 values of that function unproved. After it, none.
