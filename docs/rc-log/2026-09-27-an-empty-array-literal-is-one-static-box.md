# An empty array literal is one static box

Part of #8920.

## What changed

The semantic lowering (`ssarc`) now emits `[]` as `op_const_empty_array`, a
`const_struct` case. It is one static array box per unit with length 0 and
capacity 0 under the immortal rc. Before this, every evaluation of `[]`
allocated a capacity-4 box (#10408).

Every runtime path already handles such a box:

- **Push.** The fast and slow paths on x86-64, arm64 and wasm test length
  against capacity. Capacity 0 always takes the grow path, which starts a full
  box at capacity 4. That is the box the literal used to allocate, so an array
  that is pushed onto costs the same one allocation, only at the push. The grow
  is not a shared-receiver copy, so `__arr_push_shared_count` does not move.
- **Retain, release and uniqueness.** `rc_inc`, `rc_dec`, `arr_dec` and
  `rc_is_unique` skip a negative rc.
- **In-place updates.** Every in-place update tests uniqueness first, which an
  immortal box fails.

The capacity word is the one thing the existing constant layout got wrong. It
writes `fields + 1`, which for an empty array would be 1, and the fast path
writes into an immortal box that has room. `emit_const_agg_data_from` writes
0 for it. Wasm's block carries length 0 and capacity 0 after its rc and size
words.

The #10408 note said a static empty box was not taken because the lowering
frees `array_new` by size class. `ssarc` has no such path. Every release it
emits goes through an rc-checked helper, so the change is confined to the
semantic lowering; the AST lowering's `[]` is unchanged.

## Measured

Before this change, 2,275,005 of the 5,587,149 `__fern_arr_box` calls in one
compile of `coreutils/tsort.fern` were empty literals. That was counted with
callgrind `--dump-instr` call-site counts over the call sites whose preceding
`mov $4, %rdi` and following zero store to the length word mark the literal.
The largest sources were `ssa_lift.lift_impl` (340,000, mostly `args: []`) and
`parser.parse_type_ref` (195,000).

The self-host compiler built by itself, compiling `coreutils/tsort.fern`
(callgrind, x86-64):

| | static records | with static empty arrays |
|---|---|---|
| instructions | 4,047,464,013 | 3,900,658,503 (−3.6%) |
| stage 2 linked bytes (`-g`) | 13,112,832 | 13,092,720 |

Stage 2 → stage 3 is byte-identical, and the built `tsort` gives the same
output.

`TestSelfHostStaticBoxes` gains two probes. A record holding `args: []` that is
never pushed onto now makes one allocation a round instead of two. The same
record pushed onto later still makes three, with the push taking the allocation
the literal gave up.
