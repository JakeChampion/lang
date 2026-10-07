# 2026-10-07 — the site table reads its successors by index

`ssa.read_sites`, the table the register allocator builds the first time it
asks whether one value is live where another is defined. Refs #8171. No
emitted byte changes: against a stage 2 built from main at ff8e9341c, the
compiler on this branch builds `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, byte for byte.

## What changed

After #11756 `read_sites` was the one caller left of `successor_positions`,
which built a block's successors as a fresh array of one or two positions,
and #11758 made `read_sites` copy that array into its two successor columns
at once. The columns are now written straight from
`distinct_successor_position`, which already gives a branch by position and
-1 for a missing successor or a second branch to the first's block, and
`successor_positions` is gone with its last caller. The line in
`2026-10-06-x-a-successor-walk-builds-no-list.md` that says `read_sites`
"keeps the list form" no longer holds.

## Measured

Not profiled on its own: it removes one array of one or two entries per block
per site table, a few million instructions at most on a `checker.fern`
compile. The gate was byte identity, the five register-path suites
(`TestSelfHostSSA*`, `TestSelfHostIRVerify*`, `TestSelfHostSpillSlotMates`,
`TestSelfHostRc*`, `TestSelfHostLoop*`) and the x86-64 fixture corpus, 124
tests, all green.
