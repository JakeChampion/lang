# The rc plan reads live sets a word at a time

The rc plan asked the liveness rows one value at a time: the edge loop in
`ssaunits.plan` called `live_out_has` and `live_in_has` for every value on
every CFG edge, `live_out_row` spelled a whole row out bit by bit, and
`invariant` tested every value against the block's live-in row. On a
stage-2 compile of `lexer.fern`, `ssalive.bit_has` alone was 53 M
instructions.

`ssalive` now lists a row's set bits with `__ctz64` over each 64-bit word:
`live_in_ids`, `live_out_ids`, and `out_not_in` (live out of one block
and not into another, masked a word at a time). The three callers walk
those lists instead of every value.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, native-built compiler, Ir | 2,607,427,292 | 2,472,197,212 (−5.2%) |
| leakcheck allocations compiling `checker.fern` | 88,822,471 | 89,190,125 (+0.4%) |

The allocation rise is the id lists themselves, one array per query.
`lexer.fern`'s emitted text is byte-identical, and `checker.fern`'s is
byte-identical on x86-64, arm64 and wasm32.

`TestSelfHostSSALifetimeDependencies` checks all three lists against the
per-value predicates on every fixture, and its `wide` fixture has 174
values, so each row spans three words.
