# Three quadratic loops in the rc plan and its checks

A profile of a stage-2 x86-64 compiler compiling `lexer.fern` (callgrind
`--dump-instr` against `nm` of a `-g` build) put three passes of the self-host
Perceus path in its top twenty, each linear per item over something the
size of the whole function:

- `ssalive.input_error` checked every block, predecessor and successor with
  `block_index`, a scan of the block list. It now builds one id-to-position
  map (`first_positions`) that keeps the first block carrying an id, so a
  repeated id still reads as a duplicate.
- `ssaunits.verify` found each step it replays with `find_step`, a scan of
  the whole plan. It now indexes the steps by block once (`step_index`) and
  scans only that block's steps, still answering -1 for a missing or
  repeated one.
- `ssaunits.plan` built, for every instruction, a fresh `bits(nvals)` dead
  set, another for each argument's uses, and a third in `choose` for the
  moved units, then walked all of them. It now lists the instruction's dead
  values by id (`choose_ids`) and sorts its drops, so the steps come out in
  the same order as before.

`checker.fern`'s emitted assembly is byte-identical on x86-64 and arm64
either side.

| | before | after |
|---|---|---|
| `lexer.fern` compile, native-built compiler, Ir | 3,375,707,713 | 2,963,132,001 (−12.2%) |
| `checker.fern` compile, wall clock, three runs | 12.1–12.3 s | 11.1–11.6 s |

The first change alone was −2.0% and the two in `ssaunits` −10.2%.
