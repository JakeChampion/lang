# The unit verifier checks an edge's invariant without building it

`ssaunits.verify` replays the rc plan and, at every edge, compared the
ledger with the target block's entry invariant: `invariant` built a fresh
`nvals`-long boolean array one append at a time, and `same_bits` walked
both. In a stage-2 profile of compiling `lexer.fern`, the 5,310 edge checks
spent 30 M instructions in `invariant` and 9.5 M in `same_bits`.

`holds_invariant` answers the same question from the flow directly: every
owned live-in value, and every owned phi or parameter the block defines,
must be in the ledger, and the ledger must hold nothing else, which a count
of its set bits settles. Block entry still builds the invariant, since the
replay needs it as a starting state.

`boolean[]` has no `concat`, so building `bits(n)` from doubling pieces is
not open to the self-host sources.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,766,063,154 | 1,736,846,896 (−1.7%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical.
`TestSelfHostSSAUnits` gains `edge-drops-live-unit`, where the edge drops a
unit the target still needs; `broken-edge-invariant` already covered a unit
the target does not expect.
